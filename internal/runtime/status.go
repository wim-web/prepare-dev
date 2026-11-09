package runtime

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/wim-web/prepare-dev/internal/core"
	"github.com/wim-web/prepare-dev/internal/store"
)

// OpenFinding is a review finding nobody has acted on yet.
type OpenFinding struct {
	ReviewID  string   `json:"review_id"`
	FindingID string   `json:"finding_id"`
	Severity  string   `json:"severity"`
	Scope     string   `json:"scope"`
	Summary   string   `json:"summary"`
	Proposed  string   `json:"proposed"`
	Subjects  []string `json:"subjects"`
}

// WaitingQuestion is an open question without answers: the human still has to decide.
type WaitingQuestion struct {
	QuestionID      string   `json:"question_id"`
	Prompt          string   `json:"prompt"`
	Subjects        []string `json:"subjects"`
	CreatedAt       string   `json:"created_at"`
	SubjectsCurrent bool     `json:"subjects_current"`
}

type InflightTx struct {
	TxID       string `json:"txid"`
	AnswerID   string `json:"answer_id"`
	DecisionID string `json:"decision_id"`
}

// RunStatus is a run view with the claims it covers.
type RunStatus struct {
	TraceRun
	Claims []string `json:"claims"`
}

type Counts struct {
	Records   int `json:"records"`
	Questions int `json:"questions"`
	Runs      int `json:"runs"`
	Events    int `json:"events"`
}

type StatusView struct {
	Root              string            `json:"root"`
	Pending           []PendingAnswer   `json:"pending"`
	WaitingQuestions  []WaitingQuestion `json:"waiting_questions"`
	Inflight          []InflightTx      `json:"inflight"`
	RecoveryPending   bool              `json:"recovery_pending"`
	OpenFindings      []OpenFinding     `json:"open_findings"`
	StaleDecisions    []TraceDecision   `json:"stale_decisions"`
	RunsNotApplicable []RunStatus       `json:"runs_not_applicable"`
	RunsCurrent       []RunStatus       `json:"runs_current"`
	// RunsIncomplete were started but never recorded and are not running now: the daemon
	// stopped or failed during the run. Their artifact directories keep what was written.
	RunsIncomplete []string        `json:"runs_incomplete"`
	Jobs           []JobState      `json:"jobs"`
	Problems       []store.Problem `json:"problems"`
	Counts         Counts          `json:"counts"`
}

// Status summarizes what needs attention.
type Status struct {
	store     *store.Store
	questions *Questions
	runs      *Runs
	trace     *Trace
}

// View builds the status.
func (s *Status) View() (*StatusView, error) {
	pending, err := s.questions.Pending()
	if err != nil {
		return nil, err
	}
	qs, err := s.questions.List()
	if err != nil {
		return nil, err
	}
	waiting := []WaitingQuestion{}
	for _, q := range qs {
		if q.State != "open" || len(q.Answers) > 0 {
			continue
		}
		ids := make([]string, len(q.Subjects))
		for i, x := range q.Subjects {
			ids[i] = x.ID
		}
		waiting = append(waiting, WaitingQuestion{QuestionID: q.ID, Prompt: q.Prompt, Subjects: ids, CreatedAt: q.CreatedAt, SubjectsCurrent: len(s.questions.CheckSubjects(q.Subjects)) == 0})
	}
	inflight := []InflightTx{}
	for _, i := range s.store.InflightIntents() {
		inflight = append(inflight, InflightTx{TxID: i.TxID, AnswerID: i.AnswerID, DecisionID: i.DecisionID})
	}
	pub, err := s.trace.PublicDecisions()
	if err != nil {
		return nil, err
	}
	stale := []TraceDecision{}
	for _, d := range pub {
		if v := s.trace.DecisionView(d); !v.ReviewedCurrent {
			stale = append(stale, v)
		}
	}
	reviews, err := s.store.ListRecords("review")
	if err != nil {
		return nil, err
	}
	findings := []OpenFinding{}
	for _, r := range reviews {
		subjects := []string{}
		for _, x := range fmSubjectRefs(r.Frontmatter, "subjects") {
			subjects = append(subjects, x.ID)
		}
		for _, f := range findingsOf(r.Frontmatter) {
			if findingActual(f) != "unaddressed" {
				continue
			}
			findings = append(findings, OpenFinding{
				ReviewID: r.ID, FindingID: fmString(f, "id"), Severity: fmString(f, "severity"), Scope: fmString(f, "scope"),
				Summary: fmString(f, "summary"), Proposed: fmString(f, "proposed"), Subjects: subjects,
			})
		}
	}
	runs, err := s.runs.List()
	if err != nil {
		return nil, err
	}
	notApplicable, current := []RunStatus{}, []RunStatus{}
	for _, r := range runs {
		first := ""
		if len(r.Claims) > 0 {
			first = r.Claims[0].Claim
		}
		claims := make([]string, len(r.Claims))
		for i, c := range r.Claims {
			claims[i] = c.Spec.ID + "/" + c.Claim
		}
		rs := RunStatus{TraceRun: s.trace.RunView(r, first), Claims: claims}
		if rs.Applicable {
			current = append(current, rs)
		} else {
			notApplicable = append(notApplicable, rs)
		}
	}
	recoveryPending, err := s.questions.RecoveryPending()
	if err != nil {
		return nil, err
	}
	problems := s.store.Problems
	if problems == nil {
		problems = []store.Problem{}
	}
	jobs := s.runs.Jobs()
	known := map[string]bool{}
	for _, j := range jobs {
		known[j.RunID] = true
	}
	incomplete := []string{}
	for _, id := range s.store.IncompleteRuns() {
		if !known[id] {
			incomplete = append(incomplete, id)
		}
	}
	return &StatusView{
		Root: s.store.Root, Pending: pending, WaitingQuestions: waiting, Inflight: inflight, RecoveryPending: recoveryPending,
		OpenFindings: findings, StaleDecisions: stale, RunsNotApplicable: notApplicable, RunsCurrent: current,
		RunsIncomplete: incomplete, Jobs: jobs, Problems: problems,
		Counts: Counts{Records: len(s.store.IndexEntries()), Questions: len(s.store.ListQuestionIDs()), Runs: len(runs), Events: len(s.store.Events())},
	}, nil
}

// ---------- review bundles ----------

// BundleOptions control BuildBundle. Expand (default true) adds public decisions whose
// reviewed subjects or rationale_for mention one of the ids; Out writes the file.
type BundleOptions struct {
	Expand bool
	Out    bool
	Title  string
}

type BundleResult struct {
	Inputs   []core.SubjectRef `json:"inputs"`
	Markdown string            `json:"markdown"`
	Path     *string           `json:"path"`
}

// BuildBundle pins the records under review (and related decisions) in one Markdown
// document for a fresh-context reviewer.
func (t *Trace) BuildBundle(ids []string, opts BundleOptions) (*BundleResult, error) {
	if len(ids) == 0 {
		return nil, core.Errf("invalid", "bundle needs at least one record id")
	}
	var records []*store.RecordView
	seen := map[string]bool{}
	push := func(r *store.RecordView) {
		if !seen[r.ID] {
			seen[r.ID] = true
			records = append(records, r)
		}
	}
	for _, id := range ids {
		r, err := t.store.ReadRecord(id)
		if err != nil {
			return nil, err
		}
		if r.Kind == "decision" && !t.IsPublic(r) {
			return nil, core.Errf("not_found", "decision "+id+" is provisional")
		}
		push(r)
	}
	if opts.Expand {
		decisions, err := t.store.ListRecords("decision")
		if err != nil {
			return nil, err
		}
		for _, d := range decisions {
			if !t.IsPublic(d) {
				continue
			}
			mentioned := false
			for _, s := range fmSubjectRefs(d.Frontmatter, "reviewed") {
				if containsStr(ids, s.ID) {
					mentioned = true
				}
			}
			for _, x := range linkTargets(d.Frontmatter, "rationale_for") {
				if containsStr(ids, x) {
					mentioned = true
				}
			}
			if mentioned {
				push(d)
			}
		}
	}
	inputs := make([]core.SubjectRef, len(records))
	for i, r := range records {
		inputs[i] = core.SubjectRef{ID: r.ID, Rev: r.Rev, SHA256: r.SHA256}
	}
	title := opts.Title
	if title == "" {
		title = "Review bundle"
	}
	lines := []string{
		"# " + title, "", "generated: " + core.NowISO(), "",
		"この資料は prepare-dev の記録を版を固定してまとめたもの。会話の履歴は含まない。レビューはこの資料だけを根拠にする。", "",
		"## 含まれる記録", "",
	}
	for _, r := range records {
		lines = append(lines, "- "+r.ID+" rev "+itoa(r.Rev)+" sha256 "+r.SHA256+" — "+fmString(r.Frontmatter, "title")+" ("+r.Kind+", "+fmString(r.Frontmatter, "status")+")")
	}
	lines = append(lines, "")
	for _, r := range records {
		lines = append(lines, "---", "", "## "+r.ID+" — "+fmString(r.Frontmatter, "title"), "",
			"kind: "+r.Kind+" · rev "+itoa(r.Rev)+" · status "+fmString(r.Frontmatter, "status")+" · sha256 "+r.SHA256, "")
		fm := r.Frontmatter.Clone()
		for _, k := range []string{"body", "id", "kind", "title", "rev", "created", "updated", "status"} {
			fm.Delete(k)
		}
		if fm.Len() > 0 {
			lines = append(lines, "```yaml", strings.TrimRight(core.EmitYAML(fm), " \t\r\n"), "```", "")
		}
		lines = append(lines, strings.TrimSpace(r.Body), "")
	}
	markdown := strings.Join(lines, "\n") + "\n"
	res := &BundleResult{Inputs: inputs, Markdown: markdown}
	if opts.Out {
		stamp := strings.NewReplacer("-", "", ":", "").Replace(core.NowISO())
		if i := strings.LastIndex(stamp, "."); i >= 0 {
			stamp = stamp[:i] + "Z"
		}
		n := len(ids)
		if n > 3 {
			n = 3
		}
		name := "bundle-" + stamp + "-" + strings.Join(ids[:n], "+") + ".md"
		abs := filepath.Join(t.store.StateDir, "reviews", name)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return nil, err
		}
		if err := store.WriteFileAtomic(abs, []byte(markdown)); err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(t.store.Root, abs)
		if err != nil {
			return nil, err
		}
		res.Path = &rel
	}
	return res, nil
}

func itoa(n int64) string {
	data, _ := core.MarshalJSONNoEscape(n)
	return string(data)
}
