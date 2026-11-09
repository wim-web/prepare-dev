package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wim-web/prepare-dev/internal/core"
	"github.com/wim-web/prepare-dev/internal/plugin"
	"github.com/wim-web/prepare-dev/internal/store"
)

// Plugin is a verification plugin.
type Plugin = plugin.Plugin

// StartRunInput is the body of verify_run.
type StartRunInput struct {
	Plugin  string
	Claims  []plugin.ClaimRequest
	Targets []string
	Config  *core.OMap
}

// JobState is the in-memory state of a verification job of this process.
type JobState struct {
	RunID      string   `json:"run_id"`
	Plugin     string   `json:"plugin"`
	Status     string   `json:"status"` // running | done | failed
	StartedAt  string   `json:"started_at"`
	FinishedAt *string  `json:"finished_at"`
	Progress   []string `json:"progress"`
	Result     *string  `json:"result"`
	Error      *string  `json:"error"`
}

// Mismatch is a dependency of a run that changed since it ran.
type Mismatch struct {
	Kind     string  `json:"kind"` // claim_spec | claim_requirement | target | input | listing | plugin
	Ref      string  `json:"ref"`
	Expected string  `json:"expected"`
	Current  *string `json:"current"`
}

// Applicability tells whether a run still applies to the current versions.
type Applicability struct {
	Applicable bool       `json:"applicable"`
	Mismatches []Mismatch `json:"mismatches"`
}

// Runs registers plugins, runs verification jobs and checks applicability.
type Runs struct {
	store   *store.Store
	lock    *sync.Mutex // the runtime lock, taken when a job persists its record
	plugins map[string]Plugin
	order   []string

	jobsMu   sync.Mutex
	jobs     map[string]*JobState
	jobOrder []string
	done     map[string]chan struct{}
}

func newRuns(st *store.Store, plugins []Plugin, lock *sync.Mutex) *Runs {
	r := &Runs{store: st, lock: lock, plugins: map[string]Plugin{}, jobs: map[string]*JobState{}, done: map[string]chan struct{}{}}
	for _, p := range plugins {
		if _, ok := r.plugins[p.Name()]; !ok {
			r.order = append(r.order, p.Name())
		}
		r.plugins[p.Name()] = p
	}
	return r
}

// PluginNames lists the registered plugins.
func (r *Runs) PluginNames() []string { return append([]string{}, r.order...) }

// Plugin returns a registered plugin.
func (r *Runs) Plugin(name string) (Plugin, error) {
	p, ok := r.plugins[name]
	if !ok {
		avail := make([]any, len(r.order))
		for i, n := range r.order {
			avail[i] = n
		}
		return nil, core.Errf("not_found", fmt.Sprintf("plugin %s is not registered", name), "available", avail)
	}
	return p, nil
}

// List reads every run record.
func (r *Runs) List() ([]*core.RunRecord, error) {
	var out []*core.RunRecord
	for _, id := range r.store.ListRunIDs() {
		run, err := r.store.ReadRun(id)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, nil
}

// Get reads one run record.
func (r *Runs) Get(id string) (*core.RunRecord, error) { return r.store.ReadRun(id) }

// Job returns a copy of a job of this process.
func (r *Runs) Job(id string) (JobState, bool) {
	r.jobsMu.Lock()
	defer r.jobsMu.Unlock()
	j, ok := r.jobs[id]
	if !ok {
		return JobState{}, false
	}
	return copyJob(j), true
}

// Jobs lists the jobs of this process.
func (r *Runs) Jobs() []JobState {
	r.jobsMu.Lock()
	defer r.jobsMu.Unlock()
	out := []JobState{}
	for _, id := range r.jobOrder {
		out = append(out, copyJob(r.jobs[id]))
	}
	return out
}

// Running reports whether any job is still running.
func (r *Runs) Running() bool {
	for _, j := range r.Jobs() {
		if j.Status == "running" {
			return true
		}
	}
	return false
}

func copyJob(j *JobState) JobState {
	c := *j
	c.Progress = append([]string{}, j.Progress...)
	return c
}

func claimsOf(spec *store.RecordView) []*core.OMap {
	var out []*core.OMap
	if list, ok := spec.Frontmatter.Get("claims"); ok {
		if arr, ok := list.([]any); ok {
			for _, c := range arr {
				if m, ok := c.(*core.OMap); ok {
					out = append(out, m)
				}
			}
		}
	}
	return out
}

func nativeRefs(claim *core.OMap) []*core.OMap {
	var out []*core.OMap
	if list, ok := claim.Get("native"); ok {
		if arr, ok := list.([]any); ok {
			for _, n := range arr {
				if m, ok := n.(*core.OMap); ok {
					out = append(out, m)
				}
			}
		}
	}
	return out
}

func (r *Runs) resolveClaims(pluginName string, claims []plugin.ClaimRequest) ([]plugin.JobClaim, error) {
	if len(claims) == 0 {
		return nil, core.Errf("invalid", "verify_run needs at least one claim")
	}
	out := make([]plugin.JobClaim, 0, len(claims))
	for _, c := range claims {
		spec, err := r.store.ReadRecord(c.Spec)
		if err != nil {
			return nil, err
		}
		if spec.Kind != "spec" {
			return nil, core.Errf("invalid", fmt.Sprintf("%s is not a spec", c.Spec))
		}
		var claim *core.OMap
		for _, x := range claimsOf(spec) {
			if id, _ := x.Str("id"); id == c.Claim {
				claim = x
				break
			}
		}
		if claim == nil {
			return nil, core.Errf("not_found", fmt.Sprintf("claim %s not found in %s", c.Claim, c.Spec))
		}
		native := ""
		found := false
		for _, n := range nativeRefs(claim) {
			if p, _ := n.Str("plugin"); p == pluginName {
				native, _ = n.Str("ref")
				found = true
				break
			}
		}
		if !found {
			return nil, core.Errf("invalid", fmt.Sprintf("claim %s/%s has no native reference for plugin %s", c.Spec, c.Claim, pluginName))
		}
		var reqRef *core.SubjectRef
		if req, ok := claim.Str("requirement"); ok && req != "" {
			if rr, err := r.store.ReadRecord(req); err == nil {
				reqRef = &core.SubjectRef{ID: rr.ID, Rev: rr.Rev, SHA256: rr.SHA256}
			}
		}
		out = append(out, plugin.JobClaim{
			Spec: c.Spec, Claim: c.Claim,
			SpecRef:        core.SubjectRef{ID: spec.ID, Rev: spec.Rev, SHA256: spec.SHA256},
			RequirementRef: reqRef,
			NativeRef:      native,
		})
	}
	return out, nil
}

// checkConfig refuses a job before it reserves a run id when .prepare-dev/config.json has
// settings for a plugin the runtime does not have, or when the plugin's config (config.json
// merged with the request) has a key the plugin does not declare or a value of another
// kind (SPEC-0003 V5). A misspelled key would otherwise drop inputs from the record and
// leave a run applicable after those inputs change.
func (r *Runs) checkConfig(p Plugin, cfg *core.OMap) error {
	for _, name := range r.store.Config.Plugins.Keys() {
		if _, ok := r.plugins[name]; !ok {
			return core.Errf("invalid", fmt.Sprintf(".prepare-dev/config.json has settings for an unknown plugin %q (plugins: %s)", name, strings.Join(r.order, ", ")))
		}
	}
	if err := plugin.CheckConfig(p.Name(), cfg, p.Capabilities().Config); err != nil {
		return core.Errf("invalid", err.Error())
	}
	return nil
}

// Start resolves the claims, reserves a run id and runs the plugin in the background.
// The caller holds the runtime lock.
func (r *Runs) Start(in StartRunInput) (JobState, error) {
	if err := r.store.AssertAlive(); err != nil {
		return JobState{}, err
	}
	p, err := r.Plugin(in.Plugin)
	if err != nil {
		return JobState{}, err
	}
	claims, err := r.resolveClaims(p.Name(), in.Claims)
	if err != nil {
		return JobState{}, err
	}
	cfg := core.NewOMap()
	if pc, ok := r.store.Config.Plugins.Map(p.Name()); ok {
		cfg = pc.Clone()
	}
	if in.Config != nil {
		cfg = cfg.Merge(in.Config)
	}
	if err := r.checkConfig(p, cfg); err != nil {
		return JobState{}, err
	}
	runID := r.store.AllocateID("run")
	targets := in.Targets
	if targets == nil {
		targets = []string{}
	}
	job := &plugin.Job{RunID: runID, Root: r.store.Root, Plugin: p.Name(), Claims: claims, Targets: targets, Config: cfg}
	state := &JobState{RunID: runID, Plugin: p.Name(), Status: "running", StartedAt: core.NowISO(), Progress: []string{}}
	// reserve the id on disk immediately so a second start cannot reuse it
	artifactDir, err := r.store.RunArtifactDir(runID)
	if err != nil {
		return JobState{}, err
	}
	done := make(chan struct{})
	r.jobsMu.Lock()
	r.jobs[runID] = state
	r.jobOrder = append(r.jobOrder, runID)
	r.done[runID] = done
	snapshot := copyJob(state)
	r.jobsMu.Unlock()
	go func() {
		defer close(done)
		if err := r.execute(p, job, state, artifactDir); err != nil {
			r.jobsMu.Lock()
			msg := err.Error()
			now := core.NowISO()
			state.Status = "failed"
			state.Error = &msg
			state.FinishedAt = &now
			r.jobsMu.Unlock()
		}
	}()
	return snapshot, nil
}

// Platform is "<os>-<arch>" in the naming of Node.js.
func Platform() string {
	osName := goruntime.GOOS
	if osName == "windows" {
		osName = "win32"
	}
	arch := goruntime.GOARCH
	switch arch {
	case "amd64":
		arch = "x64"
	case "386":
		arch = "ia32"
	}
	return osName + "-" + arch
}

func syntheticRecord(p Plugin, job *plugin.Job, result, summary string) *core.RunRecord {
	method := "test"
	if m := p.Capabilities().Methods; len(m) > 0 {
		method = m[0]
	}
	claims := make([]core.RunClaim, len(job.Claims))
	for i, c := range job.Claims {
		claims[i] = core.RunClaim{Spec: c.SpecRef, Claim: c.Claim, Requirement: c.RequirementRef, Native: core.RunNative{Ref: c.NativeRef}, Result: result}
	}
	return &core.RunRecord{
		Tool:    core.RunTool{Name: p.Name(), Version: "unknown", Extra: core.NewOMap()},
		Method:  method,
		Claims:  claims,
		Inputs:  core.RunInputs{Files: []core.InputFile{}, Config: job.Config},
		Result:  result,
		Scope:   core.RunScope{Bounded: false, Bounds: nil},
		Summary: summary,
	}
}

func (r *Runs) execute(p Plugin, job *plugin.Job, state *JobState, artifactDir string) (err error) {
	started := time.Now()
	progress := func(message string) {
		r.jobsMu.Lock()
		state.Progress = append(state.Progress, core.NowISO()+" "+message)
		r.jobsMu.Unlock()
		r.store.Emitter.Emit("job", core.OM("run_id", job.RunID, "message", message))
	}
	var record *core.RunRecord
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				record = syntheticRecord(p, job, "tool_error", fmt.Sprintf("plugin failed: %v", rec))
			}
		}()
		detected := p.Detect(job)
		if !detected.Available {
			reason := detected.Reason
			if reason == "" {
				reason = "unknown"
			}
			record = syntheticRecord(p, job, "unsupported", "tool not available: "+reason)
			return
		}
		progress("collecting inputs")
		inputs, err := p.Inputs(job)
		if err != nil {
			record = syntheticRecord(p, job, "tool_error", "plugin failed: "+err.Error())
			return
		}
		progress("running")
		raw, err := p.Run(job, plugin.Context{Ctx: context.Background(), ArtifactDir: artifactDir, OnProgress: progress})
		if err != nil {
			record = syntheticRecord(p, job, "tool_error", "plugin failed: "+err.Error())
			return
		}
		progress("parsing")
		draft, err := p.Parse(raw, job, inputs)
		if err != nil {
			record = syntheticRecord(p, job, "tool_error", "plugin failed: "+err.Error())
			return
		}
		draft.Inputs.Listings = inputs.Listings
		record = draft
	}()
	record.ID = job.RunID
	record.Plugin = core.NameVersion{Name: p.Name(), Version: p.Version()}
	record.Environment = core.RunEnvironment{
		Platform:   Platform(),
		StartedAt:  core.FormatISO(started),
		FinishedAt: core.NowISO(),
		DurationMS: float64(time.Since(started).Milliseconds()),
	}
	// artifact paths must be relative to root
	for i, a := range record.Artifacts {
		if filepath.IsAbs(a.Path) {
			if rel, err := filepath.Rel(r.store.Root, a.Path); err == nil {
				record.Artifacts[i].Path = rel
			}
		}
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	if err := r.store.WriteRun(record); err != nil {
		return err
	}
	if _, err := r.store.AppendEvent(core.Event{Type: "run.record", RunID: record.ID, Result: record.Result}); err != nil {
		return err
	}
	r.jobsMu.Lock()
	now := core.NowISO()
	result := record.Result
	state.Status = "done"
	state.Result = &result
	state.FinishedAt = &now
	r.jobsMu.Unlock()
	r.store.Emitter.Emit("run", record)
	return nil
}

// RunWait is a job state with its record once done.
type RunWait struct {
	JobState
	Record *core.RunRecord `json:"record"`
}

// Wait waits up to timeout for a job of this process; a run not started by this
// process is read from disk. The caller must not hold the runtime lock.
func (r *Runs) Wait(runID string, timeout time.Duration) (RunWait, error) {
	r.jobsMu.Lock()
	_, ok := r.jobs[runID]
	done := r.done[runID]
	r.jobsMu.Unlock()
	if !ok {
		r.lock.Lock()
		defer r.lock.Unlock()
		rec, err := r.store.ReadRun(runID)
		if err != nil {
			return RunWait{}, err
		}
		result := rec.Result
		fin := rec.Environment.FinishedAt
		return RunWait{JobState: JobState{RunID: runID, Plugin: rec.Plugin.Name, Status: "done", StartedAt: rec.Environment.StartedAt, FinishedAt: &fin, Progress: []string{}, Result: &result}, Record: rec}, nil
	}
	timer := time.NewTimer(timeout)
	select {
	case <-done:
	case <-timer.C:
	}
	timer.Stop()
	state, _ := r.Job(runID)
	out := RunWait{JobState: state}
	if state.Status == "done" {
		r.lock.Lock()
		defer r.lock.Unlock()
		rec, err := r.store.ReadRun(runID)
		if err != nil {
			return RunWait{}, err
		}
		out.Record = rec
	}
	return out, nil
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// Applicability checks the spec, requirement, targets and inputs of a run against now.
func (r *Runs) Applicability(run *core.RunRecord) Applicability {
	mismatches := []Mismatch{}
	check := func(kind string, ref *core.SubjectRef) {
		if ref == nil {
			return
		}
		var current *string
		var curRev int64
		var curSHA string
		if rec, err := r.store.ReadRecord(ref.ID); err == nil {
			curRev, curSHA = rec.Rev, rec.SHA256
			s := fmt.Sprintf("rev %d %s", curRev, short(curSHA))
			current = &s
		}
		if current == nil || curRev != ref.Rev || curSHA != ref.SHA256 {
			mismatches = append(mismatches, Mismatch{Kind: kind, Ref: ref.ID, Expected: fmt.Sprintf("rev %d %s", ref.Rev, short(ref.SHA256)), Current: current})
		}
	}
	seen := map[string]bool{}
	for i := range run.Claims {
		c := &run.Claims[i]
		if !seen["s:"+c.Spec.ID] {
			seen["s:"+c.Spec.ID] = true
			spec := c.Spec
			check("claim_spec", &spec)
		}
		if c.Requirement != nil && !seen["r:"+c.Requirement.ID] {
			seen["r:"+c.Requirement.ID] = true
			check("claim_requirement", c.Requirement)
		}
	}
	checkFile := func(kind string, f core.InputFile) {
		cur, ok := r.store.FileVersion(filepath.FromSlash(f.Path))
		if !ok || cur != f.SHA256 {
			var current *string
			if ok {
				s := short(cur)
				current = &s
			}
			mismatches = append(mismatches, Mismatch{Kind: kind, Ref: f.Path, Expected: short(f.SHA256), Current: current})
		}
	}
	for _, t := range run.Targets {
		checkFile("target", t)
	}
	for _, f := range run.Inputs.Files {
		checkFile("input", f)
	}
	// a file added to or removed from a recorded directory
	for _, l := range run.Inputs.Listings {
		if cur := plugin.CurrentDigest(r.store.Root, l); cur != l.SHA256 {
			c := short(cur)
			mismatches = append(mismatches, Mismatch{Kind: "listing", Ref: plugin.ListingRef(l), Expected: short(l.SHA256), Current: &c})
		}
	}
	// another version of the plugin followed other rules (which files it records, how it
	// reads the tool's output), so its runs are not evidence for now
	if p, ok := r.plugins[run.Plugin.Name]; ok && p.Version() != run.Plugin.Version {
		c := p.Version()
		mismatches = append(mismatches, Mismatch{Kind: "plugin", Ref: run.Plugin.Name, Expected: run.Plugin.Version, Current: &c})
	}
	return Applicability{Applicable: len(mismatches) == 0, Mismatches: mismatches}
}

// sortRunViewsDesc puts the newest run first: the run that finished last, and of two
// runs that finished in the same millisecond, the one with the larger number (SPEC-0003
// V6). Runs may finish in another order than they started, since they run concurrently.
func sortRunViewsDesc(v []TraceRun) {
	sort.SliceStable(v, func(i, j int) bool { return newerRun(v[i].FinishedAt, v[i].RunID, v[j].FinishedAt, v[j].RunID) })
}

// newerRun reports whether run a is newer than run b: it finished later, or in the same
// millisecond with a larger number (SPEC-0003 V6).
func newerRun(finA, idA, finB, idB string) bool {
	if finA != finB {
		return finA > finB
	}
	_, a, _ := core.ParseID(idA)
	_, b, _ := core.ParseID(idB)
	return a > b
}
