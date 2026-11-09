// Package store is the single-writer persistent store: Markdown records, question and
// run JSON files, and the append-only events journal. File writes are atomic (temp
// file, fsync, rename); journal appends are fsynced. The index is a cache rebuilt
// from the journal.
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/wim-web/prepare-dev/internal/core"
)

// IndexEntry is the current version of a record as known from the journal.
type IndexEntry struct {
	ID      string
	Path    string // relative to root
	Rev     int64
	SHA256  string
	Removed bool
}

// RecordView is a record read from disk.
type RecordView struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	Path        string     `json:"path"`
	Rev         int64      `json:"rev"`
	SHA256      string     `json:"sha256"`
	Frontmatter *core.OMap `json:"frontmatter"`
	Body        string     `json:"body"`
}

// ExpectedVersion is the version a writer read before updating.
type ExpectedVersion struct {
	Rev    int64
	SHA256 string
}

// Problem is a file under the records dir that could not be read as a record.
type Problem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Listener receives "event" (core.Event) and "question" (*core.Question) notifications.
type Listener func(name string, payload any)

// Emitter delivers notifications synchronously to its listeners.
type Emitter struct {
	mu   sync.Mutex
	next int
	subs map[int]Listener
}

// On registers a listener and returns a function that removes it.
func (e *Emitter) On(l Listener) func() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.subs == nil {
		e.subs = map[int]Listener{}
	}
	id := e.next
	e.next++
	e.subs[id] = l
	return func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		delete(e.subs, id)
	}
}

// Emit calls every listener.
func (e *Emitter) Emit(name string, payload any) {
	e.mu.Lock()
	ids := make([]int, 0, len(e.subs))
	for id := range e.subs {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	ls := make([]Listener, 0, len(ids))
	for _, id := range ids {
		ls = append(ls, e.subs[id])
	}
	e.mu.Unlock()
	for _, l := range ls {
		l(name, payload)
	}
}

// Store is not safe for concurrent use; the runtime serializes access.
type Store struct {
	Root         string
	Config       core.ProjectConfig
	RecordsDir   string
	StateDir     string
	EventsFile   string
	IndexFile    string
	QuestionsDir string
	RunsDir      string
	Emitter      *Emitter

	index      map[string]*IndexEntry
	indexOrder []string
	events     []core.Event
	dead       bool
	// Guard, when set, runs before every operation; an error makes the store dead. The
	// daemon sets it to check that it still holds the project's lease.
	Guard func() error
	// Blocked lists conflicting journal entries found when opening (for example after
	// a merge kept both branches' lines). While it is non-empty nothing is written.
	Blocked []string
	// OnDead, when set, is called once when an operation finds the store unusable
	// (the lease or the journal changed under it); the daemon then stops.
	OnDead func(reason string)

	journalInfo os.FileInfo  // the journal file this store read
	journalSize atomic.Int64 // its length as far as this store knows
	deadOnce    sync.Once
	// Problems lists files under the records dir that could not be read as records.
	Problems []Problem
}

// LoadConfig reads .prepare-dev/config.json, or returns the defaults.
func LoadConfig(root string) (core.ProjectConfig, error) {
	data, err := os.ReadFile(filepath.Join(root, ".prepare-dev", "config.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return core.DefaultConfig(), nil
	}
	if err != nil {
		return core.DefaultConfig(), err
	}
	return core.ParseConfig(data)
}

// Open loads the journal, rebuilds the index and scans the records for external changes.
func Open(root string) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	cfg, err := LoadConfig(abs)
	if err != nil {
		return nil, fmt.Errorf("config.json: %w", err)
	}
	s := &Store{
		Root:    abs,
		Config:  cfg,
		Emitter: &Emitter{},
		index:   map[string]*IndexEntry{},
	}
	s.RecordsDir = filepath.Join(abs, cfg.RecordsDir)
	s.StateDir = filepath.Join(abs, ".prepare-dev")
	s.EventsFile = filepath.Join(s.RecordsDir, "events.jsonl")
	s.IndexFile = filepath.Join(s.StateDir, "index.json")
	s.QuestionsDir = filepath.Join(s.RecordsDir, "questions")
	s.RunsDir = filepath.Join(s.RecordsDir, "runs")
	if err := s.ensureDirs(); err != nil {
		return nil, err
	}
	if err := s.loadEvents(); err != nil {
		return nil, err
	}
	s.Blocked = append(ValidateJournal(s.events), s.foreignApplications()...)
	s.rebuildIndex()
	if err := s.ScanAll(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) ensureDirs() error {
	dirs := []string{s.RecordsDir, s.StateDir, s.QuestionsDir, s.RunsDir}
	for _, k := range core.RecordKinds {
		dirs = append(dirs, filepath.Join(s.RecordsDir, core.KindDirs[k]))
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	if !fileExists(s.EventsFile) {
		if err := os.WriteFile(s.EventsFile, nil, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Kill marks the store unusable (a simulated crash). A new Store must be opened.
func (s *Store) Kill() { s.dead = true }

// AssertAlive fails after Kill, when the Guard fails, or when the journal is no
// longer the file this store read (see CheckJournal).
func (s *Store) AssertAlive() error {
	if s.dead {
		return core.Errf("dead", "store instance is dead; reopen it")
	}
	err := s.CheckJournal()
	if err == nil && s.Guard != nil {
		err = s.Guard()
	}
	if err != nil {
		s.dead = true
		s.deadOnce.Do(func() {
			if s.OnDead != nil {
				go s.OnDead(err.Error())
			}
		})
		return core.Errf("dead", err.Error())
	}
	return nil
}

// CheckJournal fails when prepare/events.jsonl was replaced or changed by someone else
// since this store read it. Git replaces the file on a branch switch, stash, reset,
// pull or merge; a store that went on would rewrite the new branch's records from the
// old branch's index.
func (s *Store) CheckJournal() error {
	if s.journalInfo == nil {
		return nil
	}
	cur, err := os.Stat(s.EventsFile)
	if err != nil || !os.SameFile(s.journalInfo, cur) || cur.Size() != s.journalSize.Load() {
		return fmt.Errorf("%s changed under this daemon (a branch switch, stash, reset, pull or merge?); it stops without writing: run the command again", s.EventsFile)
	}
	return nil
}

// assertWritable fails while the journal has conflicting entries.
func (s *Store) assertWritable() error {
	if err := s.AssertAlive(); err != nil {
		return err
	}
	if len(s.Blocked) > 0 {
		return core.Errf("conflict", "the journal has conflicting entries ("+s.Blocked[0]+"): prepare/ was merged from two working copies. Nothing is written until you "+repairHint)
	}
	return nil
}

// ---------- journal ----------

func (s *Store) loadEvents() error {
	f, err := os.Open(s.EventsFile)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		return err
	}
	s.journalInfo = st
	s.journalSize.Store(int64(len(data)))
	s.events = nil
	for i, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev core.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			hint := ""
			if strings.HasPrefix(line, "<<<<<<<") || strings.HasPrefix(line, "=======") || strings.HasPrefix(line, ">>>>>>>") {
				hint = " (a merge conflict marker: records written in two working copies cannot be merged. " + repairHint + ")"
			}
			return fmt.Errorf("%s line %d is not a journal entry%s: %w", s.EventsFile, i+1, hint, err)
		}
		s.events = append(s.events, ev)
	}
	return nil
}

// Events returns the journal.
func (s *Store) Events() []core.Event { return s.events }

// AppendEvent writes one fsynced journal line, updates the index and notifies listeners.
func (s *Store) AppendEvent(ev core.Event) (core.Event, error) {
	if err := s.assertWritable(); err != nil {
		return ev, err
	}
	ev.TS = core.NowISO()
	line, err := ev.MarshalJSON() // json.Marshal would escape <, > and &
	if err != nil {
		return ev, err
	}
	f, err := os.OpenFile(s.EventsFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return ev, err
	}
	n, werr := f.Write(append(line, '\n'))
	s.journalSize.Add(int64(n))
	serr := fsync(f)
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		return ev, err
	}
	s.events = append(s.events, ev)
	s.applyEventToIndex(ev)
	s.writeIndexFile()
	s.Emitter.Emit("event", ev)
	return ev, nil
}

// Intents returns every apply.intent in journal order.
func (s *Store) Intents() []core.Event {
	var out []core.Event
	for _, e := range s.events {
		if e.Type == "apply.intent" {
			out = append(out, e)
		}
	}
	return out
}

func (s *Store) txidsOf(typ string) map[string]bool {
	out := map[string]bool{}
	for _, e := range s.events {
		if e.Type == typ {
			out[e.TxID] = true
		}
	}
	return out
}

// CommittedTxids are the transactions with apply.commit.
func (s *Store) CommittedTxids() map[string]bool { return s.txidsOf("apply.commit") }

// AbortedTxids are the transactions with apply.abort.
func (s *Store) AbortedTxids() map[string]bool { return s.txidsOf("apply.abort") }

// IsCommitted reports whether txid has apply.commit.
func (s *Store) IsCommitted(txid string) bool { return s.CommittedTxids()[txid] }

// InflightIntents are intents with neither commit nor abort.
func (s *Store) InflightIntents() []core.Event {
	committed, aborted := s.CommittedTxids(), s.AbortedTxids()
	var out []core.Event
	for _, i := range s.Intents() {
		if !committed[i.TxID] && !aborted[i.TxID] {
			out = append(out, i)
		}
	}
	return out
}

// CommittedIntentFor returns the committed intent of an answer, if any.
func (s *Store) CommittedIntentFor(answerID string) *core.Event {
	committed := s.CommittedTxids()
	for _, i := range s.Intents() {
		if i.AnswerID == answerID && committed[i.TxID] {
			ev := i
			return &ev
		}
	}
	return nil
}

// ---------- index ----------

func (s *Store) setIndex(e *IndexEntry) {
	if _, ok := s.index[e.ID]; !ok {
		s.indexOrder = append(s.indexOrder, e.ID)
	}
	s.index[e.ID] = e
}

func (s *Store) applyEventToIndex(ev core.Event) {
	switch ev.Type {
	case "record.write", "record.discover":
		s.setIndex(&IndexEntry{ID: ev.ID, Path: ev.Path, Rev: ev.Rev, SHA256: ev.SHA256})
	case "external_change":
		s.setIndex(&IndexEntry{ID: ev.ID, Path: ev.Path, Rev: ev.RevTo, SHA256: ev.SHA256To})
	case "record.remove":
		s.setIndex(&IndexEntry{ID: ev.ID, Path: ev.Path, Rev: 0, SHA256: "", Removed: true})
	}
}

func (s *Store) rebuildIndex() {
	s.index = map[string]*IndexEntry{}
	s.indexOrder = nil
	for _, ev := range s.events {
		s.applyEventToIndex(ev)
	}
	s.writeIndexFile()
}

func (s *Store) writeIndexFile() {
	records := core.NewOMap()
	for _, id := range s.indexOrder {
		e := s.index[id]
		m := core.OM("id", e.ID, "path", e.Path, "rev", e.Rev, "sha256", e.SHA256)
		if e.Removed {
			m.Set("removed", true)
		}
		records.Set(id, m)
	}
	data, err := core.PrettyJSON(core.OM("generated_at", core.NowISO(), "records", records))
	if err != nil {
		return
	}
	// the index is a cache; failing to write it must not fail the operation
	_ = WriteFileAtomic(s.IndexFile, data)
}

// IndexEntries lists the records that were not removed.
func (s *Store) IndexEntries() []IndexEntry {
	var out []IndexEntry
	for _, id := range s.indexOrder {
		if e := s.index[id]; !e.Removed {
			out = append(out, *e)
		}
	}
	return out
}

// Entry returns the index entry of id.
func (s *Store) Entry(id string) (IndexEntry, bool) {
	e, ok := s.index[id]
	if !ok {
		return IndexEntry{}, false
	}
	return *e, true
}

// HasRecord reports whether id is known, not removed, and its file exists.
func (s *Store) HasRecord(id string) bool {
	e, ok := s.index[id]
	return ok && !e.Removed && fileExists(filepath.Join(s.Root, e.Path))
}

func (s *Store) usedIDs() []string {
	ids := map[string]bool{}
	for id := range s.index {
		ids[id] = true
	}
	for _, ev := range s.events {
		switch ev.Type {
		case "apply.intent":
			ids[ev.DecisionID] = true
		case "run.record":
			ids[ev.RunID] = true
		case "question.create":
			ids[ev.QuestionID] = true
		}
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	return out
}

// ---------- records (markdown) ----------

// RecordPath is the relative path of a new record.
func (s *Store) RecordPath(kind, id string) string {
	return filepath.Join(s.Config.RecordsDir, core.KindDirs[kind], id+".md")
}

// ScanAll discovers new record files and detects external changes. A file that cannot
// be read is listed in Problems and never stops the scan.
func (s *Store) ScanAll() error {
	if err := s.AssertAlive(); err != nil {
		return err
	}
	s.Problems = nil
	for _, b := range s.Blocked {
		s.Problems = append(s.Problems, Problem{Path: filepath.Join(s.Config.RecordsDir, "events.jsonl"), Message: b})
	}
	if len(s.Blocked) > 0 {
		s.Problems = append(s.Problems, Problem{Path: filepath.Join(s.Config.RecordsDir, "events.jsonl"),
			Message: "prepare/ was merged from two working copies; nothing is written until you " + repairHint})
	}
	seen := map[string]string{} // id -> first path in this scan
	for _, kind := range core.RecordKinds {
		dir := core.KindDirs[kind]
		entries, err := os.ReadDir(filepath.Join(s.RecordsDir, dir))
		if err != nil {
			continue
		}
		for _, de := range entries {
			name := de.Name()
			if !strings.HasSuffix(name, ".md") || strings.HasPrefix(name, ".") {
				continue
			}
			rel := filepath.Join(s.Config.RecordsDir, dir, name)
			if id := peekID(filepath.Join(s.Root, rel)); id != "" {
				if first, dup := seen[id]; dup {
					// two files claim one id (e.g. records created on two branches and merged):
					// keep the first, report the second, and touch neither
					s.Problems = append(s.Problems, Problem{Path: rel, Message: fmt.Sprintf("duplicate id %s (also in %s); renumber one of them", id, first)})
					continue
				}
				seen[id] = rel
			}
			if _, err := s.scanFile(rel); err != nil {
				var re *core.RuntimeError
				if errors.As(err, &re) && re.Code == "dead" {
					return err
				}
				s.Problems = append(s.Problems, Problem{Path: rel, Message: firstLine(err.Error())})
			}
		}
	}
	return nil
}

// peekID reads the id of a record file, or "" if it has none or cannot be read.
func peekID(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	fm, _, err := core.ParseMarkdown(string(data))
	if err != nil {
		return ""
	}
	id, _ := fm.Str("id")
	return id
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func (s *Store) scanFile(rel string) (*IndexEntry, error) {
	data, err := os.ReadFile(filepath.Join(s.Root, rel))
	if err != nil {
		return nil, err
	}
	text := string(data)
	fm, _, err := core.ParseMarkdown(text)
	if err != nil {
		return nil, err
	}
	id, ok := fm.Str("id")
	if !ok || id == "" {
		return nil, nil
	}
	sha := core.SHA256Hex(data)
	entry, known := s.index[id]
	if !known || entry.Removed {
		rev := int64(1)
		if r, ok := fm.Get("rev"); ok {
			if n, ok := r.(int64); ok && n >= 1 {
				rev = n
			} else if f, ok := r.(float64); ok && f >= 1 {
				rev = int64(f)
			}
		}
		if _, err := s.AppendEvent(core.Event{Type: "record.discover", ID: id, Path: rel, Rev: rev, SHA256: sha}); err != nil {
			return nil, err
		}
		return s.index[id], nil
	}
	if entry.Path != rel {
		entry.Path = rel // moved outside the runtime; keep version bookkeeping
	}
	if entry.SHA256 != sha {
		return s.recordExternalChange(entry, text, fm)
	}
	return entry, nil
}

func (s *Store) recordExternalChange(entry *IndexEntry, text string, fm *core.OMap) (*IndexEntry, error) {
	if err := s.assertWritable(); err != nil {
		return nil, err
	}
	fileRev := int64(0)
	if r, ok := fm.Get("rev"); ok {
		if n, ok := core.AsInt(r); ok {
			fileRev = n
		}
	}
	newRev := entry.Rev
	if fileRev > newRev {
		newRev = fileRev
	}
	newRev++
	_, body, err := core.ParseMarkdown(text)
	if err != nil {
		return nil, err
	}
	newFm := fm.Clone()
	newFm.Set("rev", newRev)
	newFm.Set("updated", core.TodayISO())
	newText, err := core.SerializeMarkdownChecked(newFm, body)
	if err != nil {
		return nil, err
	}
	if err := WriteFileAtomic(filepath.Join(s.Root, entry.Path), []byte(newText)); err != nil {
		return nil, err
	}
	if _, err := s.AppendEvent(core.Event{
		Type: "external_change", ID: entry.ID, Path: entry.Path,
		RevFrom: entry.Rev, RevTo: newRev, SHA256From: entry.SHA256, SHA256To: core.SHA256Hex([]byte(newText)),
	}); err != nil {
		return nil, err
	}
	return s.index[entry.ID], nil
}

// ReadRecord rescans the file (an external edit bumps the rev) and returns it with
// the base keys validated.
func (s *Store) ReadRecord(id string) (*RecordView, error) {
	if err := s.AssertAlive(); err != nil {
		return nil, err
	}
	entry, ok := s.index[id]
	if !ok || entry.Removed {
		if err := s.ScanAll(); err != nil {
			return nil, err
		}
		entry, ok = s.index[id]
		if !ok || entry.Removed {
			return nil, core.Errf("not_found", fmt.Sprintf("record %s not found", id), "id", id)
		}
	}
	abs := filepath.Join(s.Root, entry.Path)
	if !fileExists(abs) {
		return nil, core.Errf("not_found", "record file missing: "+entry.Path, "id", id)
	}
	if e, err := s.scanFile(entry.Path); err != nil {
		var re *core.RuntimeError
		if errors.As(err, &re) {
			return nil, err
		}
		return nil, core.Errf("invalid", fmt.Sprintf("record %s cannot be parsed: %s", id, firstLine(err.Error())), "id", id, "path", entry.Path)
	} else if e != nil {
		entry = e
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	fm, body, err := core.ParseMarkdown(string(data))
	if err != nil {
		return nil, core.Errf("invalid", fmt.Sprintf("record %s cannot be parsed: %s", id, firstLine(err.Error())), "id", id, "path", entry.Path)
	}
	base, err := core.ParseBaseFrontmatter(fm)
	if err != nil {
		return nil, core.Errf("invalid", fmt.Sprintf("record %s has invalid frontmatter: %s", id, err.Error()), "id", id, "path", entry.Path)
	}
	kind, _ := base.Str("kind")
	return &RecordView{ID: id, Kind: kind, Path: entry.Path, Rev: entry.Rev, SHA256: entry.SHA256, Frontmatter: base, Body: body}, nil
}

// ListRecords reads every record, skipping (and reporting) invalid ones.
func (s *Store) ListRecords(kind string) ([]*RecordView, error) {
	if err := s.ScanAll(); err != nil {
		return nil, err
	}
	var out []*RecordView
	for _, e := range s.IndexEntries() {
		if !fileExists(filepath.Join(s.Root, e.Path)) {
			continue
		}
		v, err := s.ReadRecord(e.ID)
		if err != nil {
			var re *core.RuntimeError
			if errors.As(err, &re) && re.Code == "invalid" {
				s.addProblem(e.Path, re.Message)
				continue
			}
			return nil, err
		}
		if kind == "" || v.Kind == kind {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Store) addProblem(path, msg string) {
	for _, p := range s.Problems {
		if p.Path == path {
			return
		}
	}
	s.Problems = append(s.Problems, Problem{Path: path, Message: msg})
}

var runDirRE = regexp.MustCompile(`^RUN-\d+$`)

// AllocateID returns the next unused id of a kind ("question" and "run" included).
func (s *Store) AllocateID(kind string) string {
	prefix := core.IDPrefixes[kind]
	used := s.usedIDs()
	switch kind {
	case "question":
		used = append(used, listJSONIDs(s.QuestionsDir)...)
	case "run":
		used = append(used, listJSONIDs(s.RunsDir)...)
		if entries, err := os.ReadDir(s.RunsDir); err == nil {
			for _, de := range entries {
				if de.IsDir() && runDirRE.MatchString(de.Name()) {
					used = append(used, de.Name())
				}
			}
		}
	}
	return core.NextID(prefix, used)
}

// WriteRecord creates or updates a record. An update needs the expected rev and sha256
// of the current file. The merge is shallow ({...current, ...frontmatter}).
func (s *Store) WriteRecord(id string, frontmatter *core.OMap, body string, expected *ExpectedVersion) (*RecordView, error) {
	if err := s.assertWritable(); err != nil {
		return nil, err
	}
	if frontmatter == nil {
		frontmatter = core.NewOMap()
	}
	rev := int64(1)
	created := core.TodayISO()
	var rel string
	fm := frontmatter.Clone()
	if s.HasRecord(id) {
		cur, err := s.ReadRecord(id)
		if err != nil {
			return nil, err
		}
		if expected == nil {
			return nil, core.Errf("conflict", fmt.Sprintf("record %s exists; expected_rev and expected_sha256 are required", id), "id", id, "rev", cur.Rev, "sha256", cur.SHA256)
		}
		if expected.Rev != cur.Rev || expected.SHA256 != cur.SHA256 {
			return nil, core.Errf("conflict", fmt.Sprintf("record %s changed: expected rev %d, current rev %d", id, expected.Rev, cur.Rev),
				"id", id,
				"expected", core.OM("rev", expected.Rev, "sha256", expected.SHA256),
				"current", core.OM("rev", cur.Rev, "sha256", cur.SHA256))
		}
		rev = cur.Rev + 1
		if c, ok := cur.Frontmatter.Get("created"); ok && c != nil {
			created = fmt.Sprint(c)
		}
		rel = cur.Path
		if k, ok := fm.Get("kind"); ok && k != nil && k != any(cur.Kind) {
			return nil, core.Errf("invalid", "kind cannot change", "id", id)
		}
		fm = cur.Frontmatter.Merge(fm)
	} else {
		kind, _ := fm.Str("kind")
		if !core.IsRecordKind(kind) {
			return nil, core.Errf("invalid", fmt.Sprintf("invalid kind for new record %s", id), "id", id)
		}
		rel = s.RecordPath(kind, id)
		if c, ok := fm.Str("created"); ok {
			created = c
		}
	}
	fm.Set("id", id)
	fm.Set("rev", rev)
	fm.Set("created", created)
	fm.Set("updated", core.TodayISO())
	valid, err := core.ValidateFrontmatter(fm)
	if err != nil {
		return nil, invalidFrontmatter(err)
	}
	text, err := core.SerializeMarkdownChecked(valid, body)
	if err != nil {
		return nil, err
	}
	if err := WriteFileAtomic(filepath.Join(s.Root, rel), []byte(text)); err != nil {
		return nil, err
	}
	sha := core.SHA256Hex([]byte(text))
	if _, err := s.AppendEvent(core.Event{Type: "record.write", ID: id, Path: rel, Rev: rev, SHA256: sha}); err != nil {
		return nil, err
	}
	kind, _ := valid.Str("kind")
	return &RecordView{ID: id, Kind: kind, Path: rel, Rev: rev, SHA256: sha, Frontmatter: valid, Body: body}, nil
}

func invalidFrontmatter(err error) error {
	var ve *core.ValidationError
	if errors.As(err, &ve) {
		issues := make([]any, 0, len(ve.Issues))
		for _, is := range ve.Issues {
			issues = append(issues, core.OM("path", is.Path, "message", is.Message))
		}
		return core.Errf("invalid", "frontmatter is invalid: "+ve.Error(), "issues", issues)
	}
	return err
}

// CreateRecord allocates an id and writes a new record.
func (s *Store) CreateRecord(kind string, frontmatter *core.OMap, body string) (*RecordView, error) {
	id := s.AllocateID(kind)
	fm := core.NewOMap()
	if frontmatter != nil {
		fm = frontmatter.Clone()
	}
	fm.Set("kind", kind)
	return s.WriteRecord(id, fm, body, nil)
}

// RemoveRecord deletes the file and journals the removal.
func (s *Store) RemoveRecord(id, reason string) error {
	if err := s.assertWritable(); err != nil {
		return err
	}
	e, ok := s.index[id]
	if !ok || e.Removed {
		return nil
	}
	abs := filepath.Join(s.Root, e.Path)
	if fileExists(abs) {
		if err := os.Remove(abs); err != nil {
			return err
		}
	}
	_, err := s.AppendEvent(core.Event{Type: "record.remove", ID: id, Path: e.Path, Reason: reason})
	return err
}

// ---------- questions and runs (json) ----------

func listJSONIDs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, de := range entries {
		n := de.Name()
		if strings.HasSuffix(n, ".json") && !strings.HasPrefix(n, ".") {
			out = append(out, strings.TrimSuffix(n, ".json"))
		}
	}
	sort.Strings(out)
	return out
}

// ListQuestionIDs lists the question files.
func (s *Store) ListQuestionIDs() []string { return listJSONIDs(s.QuestionsDir) }

// ReadQuestion reads and validates a question file.
func (s *Store) ReadQuestion(id string) (*core.Question, error) {
	if err := s.AssertAlive(); err != nil {
		return nil, err
	}
	if !core.ValidQuestionID(id) {
		return nil, core.Errf("not_found", fmt.Sprintf("question %s not found", id), "id", id)
	}
	data, err := os.ReadFile(filepath.Join(s.QuestionsDir, id+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, core.Errf("not_found", fmt.Sprintf("question %s not found", id), "id", id)
	}
	if err != nil {
		return nil, err
	}
	var q core.Question
	if err := json.Unmarshal(data, &q); err != nil {
		return nil, core.Errf("invalid", fmt.Sprintf("question %s cannot be read: %s", id, err.Error()), "id", id)
	}
	if err := q.Validate(); err != nil {
		return nil, core.Errf("invalid", fmt.Sprintf("question %s is invalid: %s", id, err.Error()), "id", id)
	}
	return &q, nil
}

// WriteQuestion validates and writes a question file atomically.
func (s *Store) WriteQuestion(q *core.Question) error {
	if err := s.assertWritable(); err != nil {
		return err
	}
	if err := q.Validate(); err != nil {
		return err
	}
	data, err := core.PrettyJSON(q)
	if err != nil {
		return err
	}
	return WriteFileAtomic(filepath.Join(s.QuestionsDir, q.ID+".json"), append(data, '\n'))
}

// ListRunIDs lists the run files.
func (s *Store) ListRunIDs() []string { return listJSONIDs(s.RunsDir) }

// ReadRun reads and validates a run file.
func (s *Store) ReadRun(id string) (*core.RunRecord, error) {
	if p, _, ok := core.ParseID(id); !ok || p != "RUN" {
		return nil, core.Errf("not_found", fmt.Sprintf("run %s not found", id), "id", id)
	}
	data, err := os.ReadFile(filepath.Join(s.RunsDir, id+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, core.Errf("not_found", fmt.Sprintf("run %s not found", id), "id", id)
	}
	if err != nil {
		return nil, err
	}
	var r core.RunRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, core.Errf("invalid", fmt.Sprintf("run %s cannot be read: %s", id, err.Error()), "id", id)
	}
	if err := r.Validate(); err != nil {
		return nil, core.Errf("invalid", fmt.Sprintf("run %s is invalid: %s", id, err.Error()), "id", id)
	}
	return &r, nil
}

// WriteRun validates and writes a run file atomically.
func (s *Store) WriteRun(r *core.RunRecord) error {
	if err := s.assertWritable(); err != nil {
		return err
	}
	if err := r.Validate(); err != nil {
		return err
	}
	data, err := core.PrettyJSON(r)
	if err != nil {
		return err
	}
	return WriteFileAtomic(filepath.Join(s.RunsDir, r.ID+".json"), append(data, '\n'))
}

// RunArtifactDir creates (and so reserves) the artifact directory of a run.
func (s *Store) RunArtifactDir(id string) (string, error) {
	if err := s.assertWritable(); err != nil {
		return "", err
	}
	dir := filepath.Join(s.RunsDir, id)
	return dir, os.MkdirAll(dir, 0o755)
}

// IncompleteRuns are run ids whose artifact directory was reserved but whose record was
// never written (the daemon stopped or failed during the run), in id order.
func (s *Store) IncompleteRuns() []string {
	entries, err := os.ReadDir(s.RunsDir)
	if err != nil {
		return []string{}
	}
	out := []string{}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "RUN-") {
			continue
		}
		if !fileExists(filepath.Join(s.RunsDir, e.Name()+".json")) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// FileVersion is the sha256 of a file relative to root, or "" with false if missing.
func (s *Store) FileVersion(rel string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(s.Root, rel))
	if err != nil {
		return "", false
	}
	return core.SHA256Hex(data), true
}

// ---------- files ----------

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// FileExists reports whether a path exists.
func FileExists(p string) bool { return fileExists(p) }

func fsync(f *os.File) error {
	// plain fsync(2), like Node's fs.fsyncSync (os.File.Sync uses F_FULLFSYNC on macOS)
	return syscall.Fsync(int(f.Fd()))
}

// WriteFileAtomic writes to a temp file in the same directory, fsyncs it and renames it.
func WriteFileAtomic(file string, content []byte) error {
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+filepath.Base(file)+"."+core.RandomID(4)+".tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return err
	}
	_, werr := f.Write(content)
	serr := fsync(f)
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, file)
}

var _ = bytes.MinRead

// repairHint is what to do when prepare/ holds records of two working copies.
const repairHint = "resolve the merge of prepare/ (records of two working copies share numbers: keep one side, and create the other side's records again with new numbers); see \"Branches and worktrees\" in the prepare-dev README"

// foreignApplications finds decisions and applied answers on disk whose transaction is
// not in this journal: they were applied in another working copy and came in with a
// merge, so they would look confirmed here without a commit. It reads the files, not
// the index, and runs before the scan writes anything.
func (s *Store) foreignApplications() []string {
	intents := map[string]bool{} // txid
	for _, e := range s.events {
		if e.Type == "apply.intent" {
			intents[e.TxID] = true
		}
	}
	var out []string
	files, _ := filepath.Glob(filepath.Join(s.RecordsDir, core.KindDirs["decision"], "*.md"))
	sort.Strings(files)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		fm, _, err := core.ParseMarkdown(string(data))
		if err != nil {
			continue
		}
		if tx, _ := fm.Str("txid"); tx != "" && !intents[tx] {
			id, _ := fm.Str("id")
			out = append(out, fmt.Sprintf("decision %s was applied in another working copy (transaction %s is not in this journal)", id, tx))
		}
	}
	for _, id := range listJSONIDs(s.QuestionsDir) {
		q, err := s.ReadQuestion(id)
		if err != nil {
			continue
		}
		for _, a := range q.Answers {
			if a.AppliedAs != nil && a.AppliedAs.TxID != "" && !intents[a.AppliedAs.TxID] {
				out = append(out, fmt.Sprintf("answer %s of %s was applied in another working copy (as %s)", a.AnswerID, q.ID, a.AppliedAs.DecisionID))
			}
		}
	}
	return out
}
