package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wim-web/prepare-dev/internal/core"
	"github.com/wim-web/prepare-dev/internal/plugin"
	"github.com/wim-web/prepare-dev/internal/store"
)

func expected(rev int64, sha string) *store.ExpectedVersion {
	return &store.ExpectedVersion{Rev: rev, SHA256: sha}
}

func fakeRun(t *testing.T, rt *Runtime, specID, reqID, input string) *core.RunRecord {
	t.Helper()
	spec := readRec(t, rt.Store, specID)
	req := readRec(t, rt.Store, reqID)
	sha, _ := rt.Store.FileVersion(input)
	run := &core.RunRecord{
		ID:     rt.Store.AllocateID("run"),
		Plugin: core.NameVersion{Name: "fake", Version: "0"},
		Tool:   core.RunTool{Name: "fake", Version: "0", Extra: core.NewOMap()},
		Method: "test",
		Claims: []core.RunClaim{{Spec: core.SubjectRef{ID: spec.ID, Rev: spec.Rev, SHA256: spec.SHA256}, Claim: "C1",
			Requirement: &core.SubjectRef{ID: req.ID, Rev: req.Rev, SHA256: req.SHA256}, Native: core.RunNative{Ref: "x"}, Result: "verified"}},
		Targets:     []core.InputFile{{Path: input, SHA256: sha, Role: "target"}},
		Environment: core.RunEnvironment{Platform: "test", StartedAt: "2026-11-08T00:00:00Z", FinishedAt: core.NowISO(), DurationMS: 1000},
		Result:      "verified",
		Scope:       core.RunScope{Bounded: true, Bounds: core.OM("tests", int64(1))},
		Summary:     "fake",
	}
	noErr(t, rt.Store.WriteRun(run))
	ok(rt.Store.AppendEvent(core.Event{Type: "run.record", RunID: run.ID, Result: run.Result}))
	time.Sleep(2 * time.Millisecond) // distinct finished_at
	return run
}

func kinds(m []Mismatch) []string {
	out := []string{}
	for _, x := range m {
		out = append(out, x.Kind)
	}
	return out
}

type pausedPlugin struct{ gate chan struct{} }

func (p *pausedPlugin) Name() string    { return "paused" }
func (p *pausedPlugin) Version() string { return "1" }
func (p *pausedPlugin) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{Methods: []string{"test"}}
}
func (p *pausedPlugin) Guidance() string                          { return "" }
func (p *pausedPlugin) Inputs(*plugin.Job) (plugin.Inputs, error) { return plugin.Inputs{}, nil }
func (p *pausedPlugin) Detect(*plugin.Job) plugin.DetectResult {
	<-p.gate
	return plugin.DetectResult{Reason: "test probe complete"}
}
func (p *pausedPlugin) Run(*plugin.Job, plugin.Context) (plugin.RawOutput, error) {
	panic("an unavailable tool must not run")
}
func (p *pausedPlugin) Parse(plugin.RawOutput, *plugin.Job, plugin.Inputs) (*core.RunRecord, error) {
	panic("an unavailable tool has no output")
}

// SPEC-0003: evidence applicability and run ids.
func TestRuns(t *testing.T) {
	run(t, "[V2] reserves distinct run ids before asynchronous verification finishes and after restart", func(t *testing.T) {
		root := t.TempDir()
		p := &pausedPlugin{gate: make(chan struct{})}
		rt := openRT(t, root, Options{Plugins: []Plugin{p}})
		spec := ok(rt.Store.CreateRecord("spec", core.OM("title", "S", "claims", []any{core.OM("id", "C", "statement", "C", "native", []any{core.OM("plugin", "paused", "ref", "C")})}), "S"))
		in := StartRunInput{Plugin: "paused", Claims: []plugin.ClaimRequest{{Spec: spec.ID, Claim: "C"}}}
		first := ok(rt.Runs.Start(in))
		second := ok(rt.Runs.Start(in))
		if first.RunID == second.RunID {
			t.Fatal("duplicate run id")
		}
		if len(rt.Store.ListRunIDs()) != 0 {
			t.Fatal("run files written before the runs finished")
		}
		reopened := openRT(t, root)
		if id := reopened.Store.AllocateID("run"); id == first.RunID || id == second.RunID {
			t.Fatalf("a reserved run id was allocated again: %s", id)
		}
		close(p.gate)
		ok(rt.Runs.Wait(first.RunID, 5*time.Second))
		ok(rt.Runs.Wait(second.RunID, 5*time.Second))
		if got := rt.Store.ListRunIDs(); !eqStrings(got, []string{first.RunID, second.RunID}) {
			t.Fatalf("run files: %v", got)
		}
		rec := ok(rt.Runs.Get(first.RunID))
		if rec.Result != "unsupported" || !strings.Contains(rec.Summary, "test probe complete") {
			t.Fatalf("record: %s %s", rec.Result, rec.Summary)
		}
	})

	run(t, "[V1] a run stops applying when the spec, the requirement or an input file changes, and the trace shows no current evidence", func(t *testing.T) {
		root := t.TempDir()
		rt := openRT(t, root)
		req := ok(rt.Store.CreateRecord("requirement", core.OM("title", "R"), "r\n"))
		spec := ok(rt.Store.CreateRecord("spec", core.OM("title", "S", "links", core.OM("specifies", []any{req.ID}),
			"claims", []any{core.OM("id", "C1", "statement", "s", "requirement", req.ID, "native", []any{core.OM("plugin", "fake", "ref", "x")})}), "s\n"))
		input := filepath.Join("src", "impl.txt")
		noErr(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
		noErr(t, os.WriteFile(filepath.Join(root, input), []byte("v1"), 0o644))
		run := fakeRun(t, rt, spec.ID, req.ID, input)
		if !rt.Runs.Applicability(run).Applicable {
			t.Fatal("fresh run not applicable")
		}
		current := func() *TraceRun { return ok(rt.Trace.For(req.ID)).Specs[0].Claims[0].Current }
		if c := current(); c == nil || c.RunID != run.ID {
			t.Fatal("trace current evidence")
		}
		s1 := readRec(t, rt.Store, spec.ID)
		ok(rt.Store.WriteRecord(spec.ID, core.NewOMap(), "s v2\n", expected(s1.Rev, s1.SHA256)))
		app := rt.Runs.Applicability(run)
		if app.Applicable || !eqStrings(kinds(app.Mismatches), []string{"claim_spec"}) {
			t.Fatalf("after spec edit: %+v", app)
		}
		if current() != nil {
			t.Fatal("trace still shows current evidence")
		}
		v := ok(rt.Status.View())
		if len(v.RunsNotApplicable) != 1 || v.RunsNotApplicable[0].RunID != run.ID {
			t.Fatal("status runs_not_applicable")
		}
		run2 := fakeRun(t, rt, spec.ID, req.ID, input)
		if c := current(); c == nil || c.RunID != run2.ID {
			t.Fatal("a fresh run should be the current evidence")
		}
		f, err := os.OpenFile(filepath.Join(root, "prepare", "requirements", req.ID+".md"), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString("\n外部編集\n")
		f.Close()
		if k := kinds(rt.Runs.Applicability(run2).Mismatches); !eqStrings(k, []string{"claim_requirement"}) {
			t.Fatalf("after requirement edit: %v", k)
		}
		run3 := fakeRun(t, rt, spec.ID, req.ID, input)
		noErr(t, os.WriteFile(filepath.Join(root, input), []byte("v2"), 0o644))
		if k := kinds(rt.Runs.Applicability(run3).Mismatches); !eqStrings(k, []string{"target"}) {
			t.Fatalf("after target edit: %v", k)
		}
		noErr(t, os.WriteFile(filepath.Join(root, input), []byte("v1"), 0o644))
		if !rt.Runs.Applicability(run3).Applicable {
			t.Fatal("restoring the target should restore applicability")
		}
		lock := "pnpm-lock.yaml"
		noErr(t, os.WriteFile(filepath.Join(root, lock), []byte("lock-v1"), 0o644))
		sha, _ := rt.Store.FileVersion(lock)
		run3.Inputs.Files = append(run3.Inputs.Files, core.InputFile{Path: lock, SHA256: sha, Role: "config"})
		noErr(t, os.WriteFile(filepath.Join(root, lock), []byte("lock-v2"), 0o644))
		m := rt.Runs.Applicability(run3).Mismatches
		if len(m) != 1 || m[0].Kind != "input" || m[0].Ref != lock || m[0].Current == nil {
			t.Fatalf("after input edit: %+v", m)
		}
		noErr(t, os.Remove(filepath.Join(root, lock)))
		m = rt.Runs.Applicability(run3).Mismatches
		if len(m) != 1 || m[0].Kind != "input" || m[0].Current != nil {
			t.Fatalf("after input removal: %+v", m)
		}
		// a file added to a recorded directory makes the run stale, and removing it again restores it
		run4 := fakeRun(t, rt, spec.ID, req.ID, input)
		l, err := plugin.Listing(root, filepath.Join(root, "src"), false)
		noErr(t, err)
		run4.Inputs.Listings = []core.InputListing{l}
		if !rt.Runs.Applicability(run4).Applicable {
			t.Fatal("fresh run with a listing not applicable")
		}
		noErr(t, os.WriteFile(filepath.Join(root, "src", "added.txt"), []byte("new"), 0o644))
		m = rt.Runs.Applicability(run4).Mismatches
		if len(m) != 1 || m[0].Kind != "listing" || m[0].Ref != "src/" {
			t.Fatalf("after adding a file: %+v", m)
		}
		noErr(t, os.Remove(filepath.Join(root, "src", "added.txt")))
		if !rt.Runs.Applicability(run4).Applicable {
			t.Fatal("removing the added file should restore applicability")
		}
	})
}

// M2: review records, open findings, bundles, waiting questions, cancellation, links.
func TestReviews(t *testing.T) {
	run(t, "validates review frontmatter, counts unaddressed findings in status, and lists reviews in trace", func(t *testing.T) {
		rt := openRT(t, t.TempDir())
		req := ok(rt.Store.CreateRecord("requirement", core.OM("title", "R", "status", "proposed"), "要求本文\n"))
		spec := ok(rt.Store.CreateRecord("spec", core.OM("title", "S", "links", core.OM("specifies", []any{req.ID}), "claims", []any{}), "仕様本文\n"))
		ref := func(r *store.RecordView) *core.OMap { return core.OM("id", r.ID, "rev", r.Rev, "sha256", r.SHA256) }
		_, err := rt.Store.CreateRecord("review", core.OM("title", "bad", "stage", "comprehension", "subjects", []any{ref(spec)}), "")
		mustErr(t, err, "reviewer")
		rev := ok(rt.Store.CreateRecord("review", core.OM(
			"title", "SPEC の comprehension レビュー", "stage", "comprehension",
			"subjects", []any{ref(spec)}, "inputs", []any{ref(spec), ref(req)},
			"reviewer", core.OM("persona", "comprehension", "persona_version", "egm@dea7761", "host", "test", "context", "fresh"),
			"paraphrase", "この文書は X を提案している。",
			"findings", []any{
				core.OM("id", "comprehension-1-1", "severity", "blocker", "scope", "§2", "summary", "用語 A が未定義", "proposed", "fix"),
				core.OM("id", "comprehension-1-2", "severity", "nit", "scope", "§3", "summary", "表現が曖昧", "proposed", "decline", "actual", "declined", "actual_ref", nil),
				core.OM("id", "comprehension-1-3", "severity", "major", "scope", "§1", "summary", "前提が書かれていない", "proposed", "escalate"),
			}), "レビュー本文\n"))
		var actual []string
		for _, f := range findingsOf(readRec(t, rt.Store, rev.ID).Frontmatter) {
			actual = append(actual, findingActual(f))
		}
		if !eqStrings(actual, []string{"unaddressed", "declined", "unaddressed"}) {
			t.Fatalf("finding defaults: %v", actual)
		}
		st := ok(rt.Status.View())
		if len(st.OpenFindings) != 2 || st.OpenFindings[0].FindingID != "comprehension-1-1" || st.OpenFindings[1].FindingID != "comprehension-1-3" || !eqStrings(st.OpenFindings[0].Subjects, []string{spec.ID}) {
			t.Fatalf("open findings: %+v", st.OpenFindings)
		}
		tr := ok(rt.Trace.For(spec.ID))
		if len(tr.Reviews) != 1 || tr.Reviews[0].Findings != (FindingCounts{Total: 3, Unaddressed: 2, BlockersOpen: 1}) || !tr.Reviews[0].SubjectsCurrent {
			t.Fatalf("trace reviews: %+v", tr.Reviews)
		}
		cur := readRec(t, rt.Store, spec.ID)
		ok(rt.Store.WriteRecord(spec.ID, core.NewOMap(), "仕様本文 v2\n", expected(cur.Rev, cur.SHA256)))
		if ok(rt.Trace.For(spec.ID)).Reviews[0].SubjectsCurrent {
			t.Fatal("review should refer to an older version")
		}
	})

	run(t, "bundles records at pinned versions with related decisions and writes the file", func(t *testing.T) {
		root := t.TempDir()
		rt := openRT(t, root)
		req := ok(rt.Store.CreateRecord("requirement", core.OM("title", "要求", "status", "accepted"), "要求の本文\n"))
		des := ok(rt.Store.CreateRecord("design", core.OM("title", "設計", "alternatives", []any{core.OM("key", "a", "title", "A", "status", "chosen")}, "chosen", "a"), "設計の本文\n"))
		dec := ok(rt.Store.CreateRecord("decision", core.OM("title", "判断", "status", "accepted", "reviewed", []any{core.OM("id", req.ID, "rev", req.Rev, "sha256", req.SHA256)}, "chosen", "a"), "理由\n"))
		unrelated := ok(rt.Store.CreateRecord("decision", core.OM("title", "無関係", "status", "accepted"), "x\n"))
		b := ok(rt.Trace.BuildBundle([]string{req.ID, des.ID}, BundleOptions{Expand: true, Out: true, Title: "テスト"}))
		var got []string
		for _, i := range b.Inputs {
			got = append(got, i.ID)
		}
		if !eqStrings(got, []string{req.ID, des.ID, dec.ID}) || b.Inputs[0] != (core.SubjectRef{ID: req.ID, Rev: 1, SHA256: req.SHA256}) {
			t.Fatalf("inputs: %+v", b.Inputs)
		}
		for _, want := range []string{"## REQ-0001 — 要求", "要求の本文", "設計の本文", "## " + dec.ID + " — 判断"} {
			if !strings.Contains(b.Markdown, want) {
				t.Fatalf("bundle lacks %q", want)
			}
		}
		if strings.Contains(b.Markdown, unrelated.ID) {
			t.Fatal("bundle contains an unrelated decision")
		}
		if b.Path == nil || !strings.HasPrefix(*b.Path, filepath.Join(".prepare-dev", "reviews", "bundle-")) {
			t.Fatalf("path: %v", b.Path)
		}
		if data, _ := os.ReadFile(filepath.Join(root, *b.Path)); string(data) != b.Markdown {
			t.Fatal("written bundle differs")
		}
		noExpand := ok(rt.Trace.BuildBundle([]string{req.ID}, BundleOptions{}))
		if len(noExpand.Inputs) != 1 || noExpand.Path != nil {
			t.Fatalf("no expand: %+v", noExpand)
		}
	})

	run(t, "status lists questions still waiting for the human, and flags ones whose subjects moved", func(t *testing.T) {
		rt := openRT(t, t.TempDir())
		spec := ok(rt.Store.CreateRecord("spec", core.OM("title", "仕様", "claims", []any{}), "本文\n"))
		askOne := func(prompt string) *core.Question {
			return ok(rt.Questions.Create(CreateQuestionInput{Kind: "single_choice", Prompt: prompt, Subjects: []string{spec.ID}, Options: []core.QuestionOption{{Key: "a", Label: "A", Recommended: ptr(true)}}}))
		}
		q1, q2 := askOne("一つ目"), askOne("二つ目")
		w := ok(rt.Status.View()).WaitingQuestions
		if len(w) != 2 || w[0].QuestionID != q1.ID || w[1].QuestionID != q2.ID || !w[0].SubjectsCurrent || !eqStrings(w[0].Subjects, []string{spec.ID}) {
			t.Fatalf("waiting: %+v", w)
		}
		answerChoice(t, rt, q1.ID, "a")
		st := ok(rt.Status.View())
		if len(st.WaitingQuestions) != 1 || st.WaitingQuestions[0].QuestionID != q2.ID || len(st.Pending) != 1 || st.Pending[0].QuestionID != q1.ID {
			t.Fatalf("after answer: %+v", st)
		}
		ok(rt.Store.WriteRecord(spec.ID, core.NewOMap(), "本文 v2\n", expected(spec.Rev, spec.SHA256)))
		if ok(rt.Status.View()).WaitingQuestions[0].SubjectsCurrent {
			t.Fatal("moved subject not flagged")
		}
	})

	run(t, "re-asks a question at new subject versions by cancelling the unanswered old one", func(t *testing.T) {
		root := t.TempDir()
		rt := openRT(t, root)
		spec := ok(rt.Store.CreateRecord("spec", core.OM("title", "仕様", "claims", []any{}), "本文\n"))
		askOne := func(prompt string) *core.Question {
			return ok(rt.Questions.Create(CreateQuestionInput{Kind: "single_choice", Prompt: prompt, Subjects: []string{spec.ID}, Options: []core.QuestionOption{{Key: "a", Label: "A", Recommended: ptr(true)}}}))
		}
		old := askOne("古い版への質問")
		answered := askOne("回答済みの質問")
		answerChoice(t, rt, answered.ID, "a")
		cur := readRec(t, rt.Store, spec.ID)
		ok(rt.Store.WriteRecord(spec.ID, core.NewOMap(), "本文 v2\n", expected(cur.Rev, cur.SHA256)))
		fresh := askOne("新しい版への質問")
		_, err := rt.Questions.Cancel(old.ID, " ", nil)
		mustErr(t, err, "reason")
		_, err = rt.Questions.Cancel(old.ID, "置き換え", ptr("Q-9999"))
		mustErr(t, err, "")
		_, err = rt.Questions.Cancel(answered.ID, "置き換え", nil)
		mustErr(t, err, "apply or close")
		c := ok(rt.Questions.Cancel(old.ID, "対象の改訂で再質問", ptr(fresh.ID)))
		if c.State != "cancelled" || c.Cancelled == nil || c.Cancelled.Reason != "対象の改訂で再質問" || *c.Cancelled.ReplacedBy != fresh.ID {
			t.Fatalf("cancel: %+v", c)
		}
		if again := ok(rt.Questions.Cancel(old.ID, "別の理由", nil)); again.Cancelled.Reason != "対象の改訂で再質問" {
			t.Fatal("cancel is not idempotent")
		}
		_, _, err = rt.Questions.Answer(old.ID, AnswerInput{Choice: []string{"a"}})
		mustErr(t, err, "cancelled")
		st := ok(rt.Status.View())
		if len(st.WaitingQuestions) != 1 || st.WaitingQuestions[0].QuestionID != fresh.ID || !st.WaitingQuestions[0].SubjectsCurrent || len(st.Pending) != 1 || st.Pending[0].QuestionID != answered.ID {
			t.Fatalf("status: %+v", st)
		}
		n := 0
		for _, e := range rt.Store.Events() {
			if e.Type == "question.cancel" {
				n++
				if e.QuestionID != old.ID || e.ReplacedBy == nil || *e.ReplacedBy != fresh.ID {
					t.Fatalf("event: %+v", e)
				}
			}
		}
		if n != 1 {
			t.Fatalf("cancel events: %d", n)
		}
		rt2 := openRT(t, root)
		if ok(rt2.Questions.Read(old.ID)).State != "cancelled" {
			t.Fatal("cancellation lost on restart")
		}
	})

	run(t, "trace follows links in both directions so DIS → REQ → DES → SPEC can be walked", func(t *testing.T) {
		rt := openRT(t, t.TempDir())
		dis := ok(rt.Store.CreateRecord("discovery", core.OM("title", "問題", "problem", "p"), "本文\n"))
		req := ok(rt.Store.CreateRecord("requirement", core.OM("title", "要求", "links", core.OM("derived_from", []any{dis.ID})), "本文\n"))
		des := ok(rt.Store.CreateRecord("design", core.OM("title", "設計", "links", core.OM("derived_from", []any{req.ID})), "本文\n"))
		spec := ok(rt.Store.CreateRecord("spec", core.OM("title", "仕様", "links", core.OM("specifies", []any{req.ID}), "claims", []any{}), "本文\n"))
		fromDis := ok(rt.Trace.For(dis.ID)).Links
		if len(fromDis) != 1 || fromDis[0].ID != req.ID || fromDis[0].Via != "derived_from" || fromDis[0].Direction != "in" {
			t.Fatalf("from DIS: %+v", fromDis)
		}
		link := func(l TraceLink) string { return l.ID + " " + l.Via + " " + l.Direction }
		var got []string
		for _, l := range ok(rt.Trace.For(req.ID)).Links {
			got = append(got, link(l))
		}
		if !eqStrings(got, []string{dis.ID + " derived_from out", des.ID + " derived_from in", spec.ID + " specifies in"}) {
			t.Fatalf("from REQ: %v", got)
		}
		ok(rt.Store.WriteRecord(req.ID, core.OM("links", core.OM("derived_from", []any{dis.ID}, "references", []any{"docs/x.md"})), "本文\n", expected(req.Rev, req.SHA256)))
		got = nil
		for _, l := range ok(rt.Trace.For(req.ID)).Links {
			got = append(got, l.ID)
		}
		if !eqStrings(got, []string{dis.ID, des.ID, spec.ID}) {
			t.Fatalf("links to non-records must be ignored: %v", got)
		}
	})
}

// A run whose daemon stopped before the record was written shows in status; two record
// files claiming one id are reported and neither is rewritten.
func TestOperationalProblems(t *testing.T) {
	run(t, "a reserved run without a record is reported as incomplete", func(t *testing.T) {
		root := t.TempDir()
		rt := openRT(t, root)
		dir := ok(rt.Store.RunArtifactDir("RUN-0001"))
		noErr(t, os.WriteFile(filepath.Join(dir, "partial.log"), []byte("started\n"), 0o644))
		rt = openRT(t, root)
		rt.Lock()
		v := ok(rt.Status.View())
		rt.Unlock()
		if len(v.RunsIncomplete) != 1 || v.RunsIncomplete[0] != "RUN-0001" {
			t.Fatalf("runs_incomplete: %v", v.RunsIncomplete)
		}
		if id := rt.Store.AllocateID("run"); id == "RUN-0001" {
			t.Fatal("the incomplete run's id was reused")
		}
	})
	run(t, "two files with one id are a problem and neither is rewritten", func(t *testing.T) {
		root := t.TempDir()
		rt := openRT(t, root)
		rec := ok(rt.Store.CreateRecord("requirement", core.OM("title", "A"), "a\n"))
		first := filepath.Join(root, rec.Path)
		data := ok(os.ReadFile(first))
		second := filepath.Join(root, "prepare", "requirements", "REQ-9999.md")
		noErr(t, os.WriteFile(second, []byte(strings.Replace(string(data), "title: A", "title: B", 1)), 0o644))
		rt = openRT(t, root)
		found := false
		for _, p := range rt.Store.Problems {
			if strings.Contains(p.Message, "duplicate id "+rec.ID) {
				found = true
			}
		}
		if !found {
			t.Fatalf("problems: %v", rt.Store.Problems)
		}
		if after := ok(os.ReadFile(first)); string(after) != string(data) {
			t.Fatal("the first file was rewritten")
		}
		if after := ok(os.ReadFile(second)); strings.Contains(string(after), "rev: 2") {
			t.Fatal("the duplicate was rewritten")
		}
	})
}
