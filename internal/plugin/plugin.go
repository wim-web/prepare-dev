// Package plugin defines the interface of verification plugins and the helpers they
// share (running tools, hashing inputs).
package plugin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wim-web/prepare-dev/internal/core"
)

// ClaimRequest names a claim of a spec record.
type ClaimRequest struct {
	Spec  string `json:"spec"`
	Claim string `json:"claim"`
}

// JobClaim is a claim resolved against the current spec and requirement files.
type JobClaim struct {
	Spec           string
	Claim          string
	SpecRef        core.SubjectRef
	RequirementRef *core.SubjectRef
	// NativeRef is the reference for this plugin, from the spec's claims[].native.
	NativeRef string
}

// Job is one verification run.
type Job struct {
	RunID   string
	Root    string // absolute project root
	Plugin  string
	Claims  []JobClaim
	Targets []string
	Config  *core.OMap
}

// Context is given to Run.
type Context struct {
	Ctx         context.Context
	ArtifactDir string
	OnProgress  func(message string)
}

// RawOutput is what Run returns; Parse interprets Data.
type RawOutput struct {
	Data      any
	Artifacts []core.Artifact
}

// Capabilities describe a plugin.
type Capabilities struct {
	Languages  []string
	Methods    []string
	Properties []string
	Tools      []string
}

// DetectResult tells whether the tools are installed.
type DetectResult struct {
	Available bool
	Versions  map[string]string
	Reason    string
}

// Inputs are the targets and every file the result depends on, and the directories
// whose set of files the result depends on.
type Inputs struct {
	Targets  []core.InputFile
	Files    []core.InputFile
	Listings []core.InputListing
}

// Plugin is a verification backend. Parse returns a RunRecord without id, plugin and
// environment; the runtime fills those in.
type Plugin interface {
	Name() string
	Version() string
	Capabilities() Capabilities
	Guidance() string
	// Detect reports whether the tools are installed where the job runs (its project_dir).
	Detect(job *Job) DetectResult
	Inputs(job *Job) (Inputs, error)
	Run(job *Job, ctx Context) (RawOutput, error)
	Parse(raw RawOutput, job *Job, inputs Inputs) (*core.RunRecord, error)
}

// ---------- running tools ----------

// ExecResult is the outcome of Exec.
type ExecResult struct {
	Code     int
	Signal   string
	Stdout   string
	Stderr   string
	TimedOut bool
}

// ExecOptions configure Exec.
type ExecOptions struct {
	Dir     string
	Env     map[string]string
	Timeout time.Duration
	Ctx     context.Context
	OnLine  func(line string)
	// DropEnvPrefix leaves out the daemon's own environment variables with this prefix
	// (variables in Env are still passed), so a result does not depend on them.
	DropEnvPrefix string
}

// ToolPath is PATH plus the usual tool directories (~/.elan/bin, ~/.cargo/bin, ~/.local/bin).
func ToolPath() string {
	home, _ := os.UserHomeDir()
	extra := []string{}
	if home != "" {
		extra = append(extra, filepath.Join(home, ".elan", "bin"), filepath.Join(home, ".cargo", "bin"), filepath.Join(home, ".local", "bin"))
	}
	parts := []string{os.Getenv("PATH")}
	parts = append(parts, extra...)
	return strings.Join(parts, string(os.PathListSeparator))
}

// Exec runs a command with stdin closed. On timeout it kills the process group and
// sets TimedOut; on cancellation it sends SIGTERM. It fails only if the command
// cannot be started.
func Exec(name string, args []string, opts ExecOptions) (ExecResult, error) {
	ctx := opts.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	env := map[string]string{"PATH": ToolPath()}
	for k, v := range opts.Env {
		env[k] = v
	}
	cmdPath := name
	if !strings.ContainsRune(name, filepath.Separator) {
		if p, err := lookPath(name, env["PATH"]); err == nil {
			cmdPath = p
		}
	}
	cmd := exec.Command(cmdPath, args...)
	cmd.Args[0] = name
	cmd.Dir = opts.Dir
	base := os.Environ()
	if opts.DropEnvPrefix != "" {
		kept := base[:0:0]
		for _, kv := range base {
			if !strings.HasPrefix(kv, opts.DropEnvPrefix) {
				kept = append(kept, kv)
			}
		}
		base = kept
	}
	cmd.Env = mergeEnv(base, env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout, stderr lineBuffer
	stdout.onLine = opts.OnLine
	stderr.onLine = opts.OnLine
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return ExecResult{}, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var timer <-chan time.Time
	if opts.Timeout > 0 {
		t := time.NewTimer(opts.Timeout)
		defer t.Stop()
		timer = t.C
	}
	res := ExecResult{}
	var werr error
	select {
	case werr = <-done:
	case <-timer:
		res.TimedOut = true
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		werr = <-done
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		werr = <-done
	}
	stdout.flush()
	stderr.flush()
	res.Stdout = stdout.buf.String()
	res.Stderr = stderr.buf.String()
	var ee *exec.ExitError
	if errors.As(werr, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			res.Signal = ws.Signal().String()
			res.Code = -1
		} else {
			res.Code = ee.ExitCode()
		}
	} else if werr != nil {
		return res, werr
	}
	return res, nil
}

// ToolLocation is where Exec finds name (on ToolPath), with the home directory shown as
// "~", for the run record; "" if it is not found. A daemon keeps the PATH of the CLI
// that started it, so this tells which installation produced the evidence.
func ToolLocation(name string) string {
	p := name
	if !strings.ContainsRune(name, filepath.Separator) {
		var err error
		if p, err = lookPath(name, ToolPath()); err != nil {
			return ""
		}
	}
	return HomeRelative(p)
}

// HomeRelative replaces the home directory prefix of an absolute path with "~".
func HomeRelative(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

func lookPath(name, path string) (string, error) {
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", exec.ErrNotFound
}

func mergeEnv(base []string, over map[string]string) []string {
	out := make([]string, 0, len(base)+len(over))
	for _, kv := range base {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		if _, ok := over[k]; ok {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range over {
		out = append(out, k+"="+v)
	}
	return out
}

// lineBuffer collects output and reports complete lines (and the last partial one on flush).
type lineBuffer struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	pending []byte
	onLine  func(string)
}

func (l *lineBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf.Write(p)
	if l.onLine == nil {
		return len(p), nil
	}
	l.pending = append(l.pending, p...)
	for {
		i := bytes.IndexByte(l.pending, '\n')
		if i < 0 {
			break
		}
		l.onLine(string(l.pending[:i]))
		l.pending = l.pending[i+1:]
	}
	return len(p), nil
}

func (l *lineBuffer) flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.onLine != nil && len(l.pending) > 0 {
		l.onLine(string(l.pending))
		l.pending = nil
	}
}

// ---------- inputs ----------

// InputSet collects files with their sha256, once each; the first role wins.
type InputSet struct {
	Root  string
	files []core.InputFile
	seen  map[string]bool
}

// NewInputSet starts an empty set under root.
func NewInputSet(root string) *InputSet { return &InputSet{Root: root, seen: map[string]bool{}} }

// Add hashes a file (path relative to root, or absolute under root) and records its role.
func (s *InputSet) Add(path, role string) error {
	rel := path
	if filepath.IsAbs(path) {
		r, err := filepath.Rel(s.Root, path)
		if err != nil {
			return err
		}
		rel = r
	}
	rel = filepath.ToSlash(filepath.Clean(rel))
	if s.seen[rel] {
		return nil
	}
	sum, err := core.SHA256File(filepath.Join(s.Root, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	s.seen[rel] = true
	s.files = append(s.files, core.InputFile{Path: rel, SHA256: sum, Role: role})
	return nil
}

// Has reports whether a path was added.
func (s *InputSet) Has(rel string) bool { return s.seen[filepath.ToSlash(filepath.Clean(rel))] }

// Files returns the files in insertion order.
func (s *InputSet) Files() []core.InputFile {
	return append([]core.InputFile{}, s.files...)
}

// ConfigString reads a string from a plugin config.
func ConfigString(cfg *core.OMap, key, def string) string {
	if s, ok := cfg.Str(key); ok {
		return s
	}
	return def
}

// ConfigStrings reads a list of strings from a plugin config.
func ConfigStrings(cfg *core.OMap, key string) []string {
	v, ok := cfg.Get(key)
	if !ok {
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := []string{}
	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// ConfigInt reads an integer from a plugin config.
func ConfigInt(cfg *core.OMap, key string, def int64) int64 {
	v, ok := cfg.Get(key)
	if !ok {
		return def
	}
	if i, ok := core.AsInt(v); ok {
		return i
	}
	return def
}

// Severity orders results: verified < unknown < failed < timeout < tool_error < unsupported.
var Severity = map[string]int{"verified": 0, "unknown": 1, "failed": 2, "timeout": 3, "tool_error": 4, "unsupported": 5}

// Worst returns the most severe result.
func Worst(results ...string) string {
	worst := "verified"
	for _, r := range results {
		if Severity[r] > Severity[worst] {
			worst = r
		}
	}
	return worst
}

// ---------- patterns and listings ----------

// skipDir reports the directories ** and recursive listings do not enter: installed
// packages and hidden directories (.git, .lake, ...).
func skipDir(name string) bool { return name == "node_modules" || strings.HasPrefix(name, ".") }

func hasMeta(seg string) bool { return strings.ContainsAny(seg, "*?[") }

// matchSegments matches slash-separated path segments against pattern segments, where
// "**" matches any number of segments.
func matchSegments(pat, path []string) bool {
	if len(pat) == 0 {
		return len(path) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(path); i++ {
			if matchSegments(pat[1:], path[i:]) {
				return true
			}
		}
		return false
	}
	if len(path) == 0 {
		return false
	}
	ok, err := filepathMatch(pat[0], path[0])
	return err == nil && ok && matchSegments(pat[1:], path[1:])
}

var filepathMatch = filepath.Match

// ListingDigest is sha256 over the sorted relative paths ("/"-separated) of the
// regular files in dir, one per line; recursive walks subdirectories except skipped
// ones. Hidden files (".DS_Store", ...) are left out, as Go leaves them out of
// packages and embedded directories. A missing directory has the digest of no files.
func ListingDigest(dir string, recursive bool) string {
	var names []string
	if recursive {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() && p != dir && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			if d.Type().IsRegular() && !strings.HasPrefix(d.Name(), ".") {
				rel, _ := filepath.Rel(dir, p)
				names = append(names, filepath.ToSlash(rel))
			}
			return nil
		})
	} else if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.Type().IsRegular() && !strings.HasPrefix(e.Name(), ".") {
				names = append(names, e.Name())
			}
		}
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n + "\n")
	}
	return core.SHA256Hex([]byte(b.String()))
}

// Listing records dir (absolute, under root) for the run.
func Listing(root, dir string, recursive bool) (core.InputListing, error) {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return core.InputListing{}, err
	}
	return core.InputListing{Path: filepath.ToSlash(rel), Recursive: recursive, SHA256: ListingDigest(dir, recursive)}, nil
}

// ExpandPatterns resolves impl_files entries relative to root. A plain path names one
// file. A pattern may use *, ? and [...] within a path segment and ** for any number of
// directories (** does not enter node_modules or hidden directories). Every entry must
// match a file. Hidden files match only a plain path. It returns the files (absolute,
// sorted, unique) and, for patterns, the listing of the directory they search, so that
// a newly matching file is noticed.
func ExpandPatterns(root string, patterns []string) ([]string, []core.InputListing, error) {
	seen := map[string]bool{}
	var files []string
	var listings []core.InputListing
	for _, pattern := range patterns {
		segs := strings.Split(filepath.ToSlash(filepath.Clean(pattern)), "/")
		fixed := 0
		for fixed < len(segs) && !hasMeta(segs[fixed]) && segs[fixed] != "**" {
			fixed++
		}
		if fixed == len(segs) { // a plain path
			abs := filepath.Join(root, filepath.FromSlash(pattern))
			if st, err := os.Stat(abs); err != nil || !st.Mode().IsRegular() {
				return nil, nil, fmt.Errorf("impl_files %q is not a file", pattern)
			}
			if !seen[abs] {
				seen[abs] = true
				files = append(files, abs)
			}
			continue
		}
		base := filepath.Join(root, filepath.FromSlash(strings.Join(segs[:fixed], "/")))
		rest := segs[fixed:]
		recursive := len(rest) > 1 || rest[0] == "**"
		matched := 0
		_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if p != base && (skipDir(d.Name()) || !recursive) {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() || strings.HasPrefix(d.Name(), ".") {
				return nil
			}
			rel, _ := filepath.Rel(base, p)
			if matchSegments(rest, strings.Split(filepath.ToSlash(rel), "/")) {
				matched++
				if !seen[p] {
					seen[p] = true
					files = append(files, p)
				}
			}
			return nil
		})
		if matched == 0 {
			return nil, nil, fmt.Errorf("impl_files %q matches no file", pattern)
		}
		l, err := Listing(root, base, recursive)
		if err != nil {
			return nil, nil, err
		}
		listings = append(listings, l)
	}
	sort.Strings(files)
	return files, listings, nil
}

// ---------- bounds ----------

// MergeBounds adds the top-level numbers and strings of the JSON object in file to
// bounds (a run's scope.bounds). A missing or invalid file adds nothing.
func MergeBounds(bounds *core.OMap, file string, enabled bool) {
	if !enabled {
		return
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return
	}
	v, err := core.DecodeJSON(data)
	if err != nil {
		return
	}
	m, ok := v.(*core.OMap)
	if !ok {
		return
	}
	for _, k := range m.Keys() {
		x, _ := m.Get(k)
		switch x.(type) {
		case int64, float64, string:
			bounds.Set(k, x)
		}
	}
}

// MergeBoundsDir merges every *.json file of dir, in name order (see MergeBounds).
func MergeBoundsDir(bounds *core.OMap, dir string) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	sort.Strings(files)
	for _, f := range files {
		MergeBounds(bounds, f, true)
	}
}
