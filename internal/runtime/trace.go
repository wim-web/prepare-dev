package runtime

import (
	"github.com/wim-web/prepare-dev/internal/core"
	"github.com/wim-web/prepare-dev/internal/store"
)

// TraceRun is a run as evidence for one claim.
type TraceRun struct {
	RunID       string     `json:"run_id"`
	Plugin      string     `json:"plugin"`
	Method      string     `json:"method"`
	Result      string     `json:"result"`
	ClaimResult string     `json:"claim_result"`
	FinishedAt  string     `json:"finished_at"`
	Applicable  bool       `json:"applicable"`
	Mismatches  []Mismatch `json:"mismatches"`
	Bounded     bool       `json:"bounded"`
	Summary     string     `json:"summary"`
}

// TraceClaim is a spec claim with its runs; Current is the best applicable evidence.
type TraceClaim struct {
	ID          string     `json:"id"`
	Statement   string     `json:"statement"`
	Requirement *string    `json:"requirement"`
	Runs        []TraceRun `json:"runs"`
	Current     *TraceRun  `json:"current"`
}

type TraceSpec struct {
	ID     string       `json:"id"`
	Title  string       `json:"title"`
	Rev    int64        `json:"rev"`
	Status string       `json:"status"`
	Claims []TraceClaim `json:"claims"`
}

// TraceDecision is a decision; ReviewedCurrent tells whether its reviewed versions still match.
type TraceDecision struct {
	ID              string            `json:"id"`
	Title           string            `json:"title"`
	Status          string            `json:"status"`
	Question        *string           `json:"question"`
	Answer          *string           `json:"answer"`
	Committed       bool              `json:"committed"`
	ReviewedCurrent bool              `json:"reviewed_current"`
	Reviewed        []core.SubjectRef `json:"reviewed"`
}

type TraceAnswer struct {
	AnswerID  string  `json:"answer_id"`
	Stale     bool    `json:"stale"`
	Received  bool    `json:"received"`
	Committed bool    `json:"committed"`
	Closed    bool    `json:"closed"`
	AppliedAs *string `json:"applied_as"`
}

type TraceQuestion struct {
	ID      string        `json:"id"`
	State   string        `json:"state"`
	Prompt  string        `json:"prompt"`
	Answers []TraceAnswer `json:"answers"`
}

type FindingCounts struct {
	Total        int `json:"total"`
	Unaddressed  int `json:"unaddressed"`
	BlockersOpen int `json:"blockers_open"`
}

type TraceReview struct {
	ID              string            `json:"id"`
	Title           string            `json:"title"`
	Stage           string            `json:"stage"`
	Persona         string            `json:"persona"`
	PersonaVersion  string            `json:"persona_version"`
	Status          string            `json:"status"`
	Subjects        []core.SubjectRef `json:"subjects"`
	SubjectsCurrent bool              `json:"subjects_current"`
	Findings        FindingCounts     `json:"findings"`
}

// TraceLink is a record connected through links; Direction is "out" when the traced
// record links to it and "in" when it links to the traced record.
type TraceLink struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Rev       int64  `json:"rev"`
	Status    string `json:"status"`
	Via       string `json:"via"`
	Direction string `json:"direction"`
}

type TraceRecord struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Rev    int64  `json:"rev"`
	SHA256 string `json:"sha256"`
	Status string `json:"status"`
}

type TraceView struct {
	Record    TraceRecord     `json:"record"`
	Questions []TraceQuestion `json:"questions"`
	Decisions []TraceDecision `json:"decisions"`
	Specs     []TraceSpec     `json:"specs"`
	Reviews   []TraceReview   `json:"reviews"`
	Links     []TraceLink     `json:"links"`
}

// Trace follows questions, decisions, specs, runs, reviews and links of a record.
type Trace struct {
	store     *store.Store
	questions *Questions
	runs      *Runs
}

// ---------- frontmatter helpers (reads validate only the base keys) ----------

func fmString(fm *core.OMap, key string) string {
	s, _ := fm.Str(key)
	return s
}

func fmStringPtr(fm *core.OMap, key string) *string {
	if s, ok := fm.Str(key); ok {
		return &s
	}
	return nil
}

func fmList(fm *core.OMap, key string) []any {
	v, ok := fm.Get(key)
	if !ok {
		return nil
	}
	list, _ := v.([]any)
	return list
}

func fmSubjectRefs(fm *core.OMap, key string) []core.SubjectRef {
	out := []core.SubjectRef{}
	for _, x := range fmList(fm, key) {
		m, ok := x.(*core.OMap)
		if !ok {
			continue
		}
		id, _ := m.Str("id")
		sha, _ := m.Str("sha256")
		rv, _ := m.Get("rev")
		rev, _ := core.AsInt(rv)
		out = append(out, core.SubjectRef{ID: id, Rev: rev, SHA256: sha})
	}
	return out
}

type linkEntry struct {
	via     string
	targets []string
}

func fmLinks(fm *core.OMap) []linkEntry {
	m, ok := fm.Map("links")
	if !ok {
		return nil
	}
	var out []linkEntry
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		var targets []string
		if list, ok := v.([]any); ok {
			for _, t := range list {
				if s, ok := t.(string); ok {
					targets = append(targets, s)
				}
			}
		}
		out = append(out, linkEntry{via: k, targets: targets})
	}
	return out
}

func linkTargets(fm *core.OMap, via string) []string {
	for _, l := range fmLinks(fm) {
		if l.via == via {
			return l.targets
		}
	}
	return nil
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ---------- views ----------

// IsPublic: a decision whose transaction is committed, or that was not created from an answer.
func (t *Trace) IsPublic(d *store.RecordView) bool {
	txid, ok := d.Frontmatter.Str("txid")
	return !ok || txid == "" || t.store.IsCommitted(txid)
}

// PublicDecisions lists the committed decisions.
func (t *Trace) PublicDecisions() ([]*store.RecordView, error) {
	all, err := t.store.ListRecords("decision")
	if err != nil {
		return nil, err
	}
	var out []*store.RecordView
	for _, d := range all {
		if t.IsPublic(d) {
			out = append(out, d)
		}
	}
	return out, nil
}

// DecisionView compares the reviewed versions with the current files.
func (t *Trace) DecisionView(d *store.RecordView) TraceDecision {
	reviewed := fmSubjectRefs(d.Frontmatter, "reviewed")
	txid, hasTx := d.Frontmatter.Str("txid")
	return TraceDecision{
		ID: d.ID, Title: fmString(d.Frontmatter, "title"), Status: fmString(d.Frontmatter, "status"),
		Question: fmStringPtr(d.Frontmatter, "question"), Answer: fmStringPtr(d.Frontmatter, "answer"),
		Committed:       !hasTx || txid == "" || t.store.IsCommitted(txid),
		ReviewedCurrent: len(t.questions.CheckSubjects(reviewed)) == 0,
		Reviewed:        reviewed,
	}
}

func findingsOf(fm *core.OMap) []*core.OMap {
	var out []*core.OMap
	for _, x := range fmList(fm, "findings") {
		if m, ok := x.(*core.OMap); ok {
			out = append(out, m)
		}
	}
	return out
}

// findingActual applies the schema default for findings written without "actual".
func findingActual(f *core.OMap) string {
	if s, ok := f.Str("actual"); ok {
		return s
	}
	return "unaddressed"
}

// ReviewView summarizes a review record.
func (t *Trace) ReviewView(r *store.RecordView) TraceReview {
	subjects := fmSubjectRefs(r.Frontmatter, "subjects")
	var persona, version string
	if rv, ok := r.Frontmatter.Map("reviewer"); ok {
		persona, _ = rv.Str("persona")
		version, _ = rv.Str("persona_version")
	}
	counts := FindingCounts{}
	for _, f := range findingsOf(r.Frontmatter) {
		counts.Total++
		if findingActual(f) == "unaddressed" {
			counts.Unaddressed++
			if sev, _ := f.Str("severity"); sev == "blocker" {
				counts.BlockersOpen++
			}
		}
	}
	return TraceReview{
		ID: r.ID, Title: fmString(r.Frontmatter, "title"), Stage: fmString(r.Frontmatter, "stage"),
		Persona: persona, PersonaVersion: version, Status: fmString(r.Frontmatter, "status"),
		Subjects: subjects, SubjectsCurrent: len(t.questions.CheckSubjects(subjects)) == 0, Findings: counts,
	}
}

// ReviewsFor lists the reviews of a record (or the review itself).
func (t *Trace) ReviewsFor(id string) ([]TraceReview, error) {
	all, err := t.store.ListRecords("review")
	if err != nil {
		return nil, err
	}
	out := []TraceReview{}
	for _, r := range all {
		match := r.ID == id
		for _, s := range fmSubjectRefs(r.Frontmatter, "subjects") {
			if s.ID == id {
				match = true
			}
		}
		if match {
			out = append(out, t.ReviewView(r))
		}
	}
	return out, nil
}

// RunView shows a run as evidence for one claim.
func (t *Trace) RunView(run *core.RunRecord, claimID string) TraceRun {
	app := t.runs.Applicability(run)
	claimResult := run.Result
	for _, c := range run.Claims {
		if c.Claim == claimID {
			claimResult = c.Result
			break
		}
	}
	return TraceRun{
		RunID: run.ID, Plugin: run.Plugin.Name, Method: run.Method, Result: run.Result, ClaimResult: claimResult,
		FinishedAt: run.Environment.FinishedAt, Applicable: app.Applicable, Mismatches: app.Mismatches,
		Bounded: run.Scope.Bounded, Summary: run.Summary,
	}
}

// SpecView lists the claims of a spec with their runs, newest first.
func (t *Trace) SpecView(spec *store.RecordView, allRuns []*core.RunRecord) TraceSpec {
	claims := []TraceClaim{}
	for _, c := range claimsOf(spec) {
		cid, _ := c.Str("id")
		runs := []TraceRun{}
		for _, r := range allRuns {
			for _, rc := range r.Claims {
				if rc.Spec.ID == spec.ID && rc.Claim == cid {
					runs = append(runs, t.RunView(r, cid))
					break
				}
			}
		}
		sortRunViewsDesc(runs)
		var current *TraceRun
		for i := range runs {
			if runs[i].Applicable && runs[i].ClaimResult == "verified" {
				current = &runs[i]
				break
			}
		}
		if current == nil {
			for i := range runs {
				if runs[i].Applicable {
					current = &runs[i]
					break
				}
			}
		}
		claims = append(claims, TraceClaim{ID: cid, Statement: fmString(c, "statement"), Requirement: fmStringPtr(c, "requirement"), Runs: runs, Current: current})
	}
	return TraceSpec{ID: spec.ID, Title: fmString(spec.Frontmatter, "title"), Rev: spec.Rev, Status: fmString(spec.Frontmatter, "status"), Claims: claims}
}

// LinksFor follows links in both directions; provisional decisions are excluded.
func (t *Trace) LinksFor(rec *store.RecordView) ([]TraceLink, error) {
	list, err := t.store.ListRecords("")
	if err != nil {
		return nil, err
	}
	var all []*store.RecordView
	byID := map[string]*store.RecordView{}
	for _, r := range list {
		if r.Kind != "decision" || t.IsPublic(r) {
			all = append(all, r)
			byID[r.ID] = r
		}
	}
	view := func(r *store.RecordView, via, dir string) TraceLink {
		return TraceLink{ID: r.ID, Kind: r.Kind, Title: fmString(r.Frontmatter, "title"), Rev: r.Rev, Status: fmString(r.Frontmatter, "status"), Via: via, Direction: dir}
	}
	out := []TraceLink{}
	for _, l := range fmLinks(rec.Frontmatter) {
		for _, target := range l.targets {
			if r, ok := byID[target]; ok && r.ID != rec.ID {
				out = append(out, view(r, l.via, "out"))
			}
		}
	}
	for _, r := range all {
		if r.ID == rec.ID {
			continue
		}
		for _, l := range fmLinks(r.Frontmatter) {
			if containsStr(l.targets, rec.ID) {
				out = append(out, view(r, l.via, "in"))
			}
		}
	}
	return out, nil
}

// For builds the trace of a record.
func (t *Trace) For(id string) (*TraceView, error) {
	rec, err := t.store.ReadRecord(id)
	if err != nil {
		return nil, err
	}
	allRuns, err := t.runs.List()
	if err != nil {
		return nil, err
	}
	qs, err := t.questions.List()
	if err != nil {
		return nil, err
	}
	questions := []TraceQuestion{}
	for _, q := range qs {
		has := false
		for _, s := range q.Subjects {
			if s.ID == id {
				has = true
			}
		}
		if !has {
			continue
		}
		tq := TraceQuestion{ID: q.ID, State: q.State, Prompt: q.Prompt, Answers: []TraceAnswer{}}
		for _, a := range q.Answers {
			committed := t.questions.IsCommitted(a.AnswerID)
			var appliedAs *string
			if committed && a.AppliedAs != nil {
				d := a.AppliedAs.DecisionID
				appliedAs = &d
			}
			tq.Answers = append(tq.Answers, TraceAnswer{AnswerID: a.AnswerID, Stale: a.Stale, Received: a.ReceivedAt != nil, Committed: committed, Closed: a.Closed != nil, AppliedAs: appliedAs})
		}
		questions = append(questions, tq)
	}
	pub, err := t.PublicDecisions()
	if err != nil {
		return nil, err
	}
	decisions := []TraceDecision{}
	for _, d := range pub {
		match := d.ID == id || containsStr(linkTargets(d.Frontmatter, "rationale_for"), id)
		for _, s := range fmSubjectRefs(d.Frontmatter, "reviewed") {
			if s.ID == id {
				match = true
			}
		}
		if match {
			decisions = append(decisions, t.DecisionView(d))
		}
	}
	specs := []TraceSpec{}
	switch rec.Kind {
	case "spec":
		specs = append(specs, t.SpecView(rec, allRuns))
	case "requirement":
		all, err := t.store.ListRecords("spec")
		if err != nil {
			return nil, err
		}
		for _, s := range all {
			match := containsStr(linkTargets(s.Frontmatter, "specifies"), id)
			for _, c := range claimsOf(s) {
				if r, _ := c.Str("requirement"); r == id {
					match = true
				}
			}
			if match {
				specs = append(specs, t.SpecView(s, allRuns))
			}
		}
	}
	reviews, err := t.ReviewsFor(id)
	if err != nil {
		return nil, err
	}
	links, err := t.LinksFor(rec)
	if err != nil {
		return nil, err
	}
	return &TraceView{
		Record:    TraceRecord{ID: rec.ID, Kind: rec.Kind, Title: fmString(rec.Frontmatter, "title"), Rev: rec.Rev, SHA256: rec.SHA256, Status: fmString(rec.Frontmatter, "status")},
		Questions: questions, Decisions: decisions, Specs: specs, Reviews: reviews, Links: links,
	}, nil
}
