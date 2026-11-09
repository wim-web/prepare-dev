package runtime

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wim-web/prepare-dev/internal/core"
	"github.com/wim-web/prepare-dev/internal/store"
)

// ApplyHooks are called after each persistence stage of apply ("intent", "decision",
// "question", "commit"). Returning a *core.SimulatedCrash leaves the store in flight.
type ApplyHooks struct {
	AfterStage func(stage string) error
}

// RecoveryHooks are called after each recovery write ("abort", "retract_decision",
// "retract_question").
type RecoveryHooks struct {
	AfterStage func(stage string) error
}

// CreateQuestionInput is the body of question_create.
type CreateQuestionInput struct {
	Kind          string
	Prompt        string
	Subjects      []string
	Options       []core.QuestionOption
	AllowFreeText *bool
	CreatedBy     core.CreatedBy
}

// AnswerInput is a human answer; a nil Choice is null.
type AnswerInput struct {
	Choice []string
	Text   *string
}

// PendingAnswer is an answer that is neither committed nor closed.
type PendingAnswer struct {
	QuestionID  string            `json:"question_id"`
	AnswerID    string            `json:"answer_id"`
	QuestionRev int64             `json:"question_rev"`
	Kind        string            `json:"kind"`
	Prompt      string            `json:"prompt"`
	Subjects    []core.SubjectRef `json:"subjects"`
	AnsweredAt  string            `json:"answered_at"`
	Stale       bool              `json:"stale"`
	Received    bool              `json:"received"`
	Choice      []string          `json:"choice"`
	Text        *string           `json:"text"`
}

// ApplyResult is returned by apply.
type ApplyResult struct {
	Status     string `json:"status"` // applied | already_applied
	DecisionID string `json:"decision_id"`
	TxID       string `json:"txid"`
}

// RecoveryItem describes one in-flight transaction found at startup.
type RecoveryItem struct {
	TxID       string  `json:"txid"`
	AnswerID   string  `json:"answer_id"`
	DecisionID string  `json:"decision_id"`
	Outcome    string  `json:"outcome"` // forward | abort
	Reason     *string `json:"reason,omitempty"`
}

// RecoveryReport is the result of recovery.
type RecoveryReport struct {
	Recovered []RecoveryItem `json:"recovered"`
	Retracted []string       `json:"retracted"`
}

// Version is a record version that exists now.
type Version struct {
	Rev    int64  `json:"rev"`
	SHA256 string `json:"sha256"`
}

// SubjectMismatch is a pinned subject that differs from the file now.
type SubjectMismatch struct {
	ID       string          `json:"id"`
	Expected core.SubjectRef `json:"expected"`
	Current  *Version        `json:"current"`
}

// Questions implements questions, answers, apply and recovery (SPEC-0001).
type Questions struct {
	store *store.Store
}

// ---------- read ----------

// List returns the public view of every question.
func (qs *Questions) List() ([]*core.Question, error) {
	var out []*core.Question
	for _, id := range qs.store.ListQuestionIDs() {
		q, err := qs.Read(id)
		if err != nil {
			// a file that cannot be read (broken JSON, an invalid question, no permission,
			// one removed meanwhile) is left out, like a record that cannot be parsed:
			// status, the question list and trace go on with the others (Store.ScanAll
			// shows it in the problems). Only an unusable store stops the list.
			var re *core.RuntimeError
			if errors.As(err, &re) && re.Code == "dead" {
				return nil, err
			}
			continue
		}
		out = append(out, q)
	}
	return out, nil
}

// Read returns the public view: applied_as only for journal-committed answers.
func (qs *Questions) Read(id string) (*core.Question, error) {
	q, err := qs.store.ReadQuestion(id)
	if err != nil {
		return nil, err
	}
	return qs.publicView(q), nil
}

func (qs *Questions) publicView(q *core.Question) *core.Question {
	out := q.Clone()
	out.State = qs.deriveState(q)
	for i := range out.Answers {
		if c := qs.store.CommittedIntentFor(out.Answers[i].AnswerID); c != nil {
			out.Answers[i].AppliedAs = &core.AppliedAs{DecisionID: c.DecisionID, TxID: c.TxID}
		} else {
			out.Answers[i].AppliedAs = nil
		}
	}
	return out
}

// IsCommitted reports whether an answer has a committed apply.
func (qs *Questions) IsCommitted(answerID string) bool {
	return qs.store.CommittedIntentFor(answerID) != nil
}

func (qs *Questions) isUnprocessed(a *core.Answer) bool {
	return a.Closed == nil && !qs.IsCommitted(a.AnswerID)
}

// Pending lists answers that are neither committed nor closed (ack and stale do not matter).
func (qs *Questions) Pending() ([]PendingAnswer, error) {
	list, err := qs.List()
	if err != nil {
		return nil, err
	}
	out := []PendingAnswer{}
	for _, q := range list {
		for i := range q.Answers {
			a := &q.Answers[i]
			if !qs.isUnprocessed(a) {
				continue
			}
			out = append(out, PendingAnswer{
				QuestionID: q.ID, AnswerID: a.AnswerID, QuestionRev: q.Rev, Kind: q.Kind, Prompt: q.Prompt,
				Subjects: q.Subjects, AnsweredAt: a.AnsweredAt, Stale: a.Stale, Received: a.ReceivedAt != nil,
				Choice: a.Choice, Text: a.Text,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].AnsweredAt != out[j].AnsweredAt {
			return out[i].AnsweredAt < out[j].AnsweredAt
		}
		return out[i].AnswerID < out[j].AnswerID
	})
	return out, nil
}

// PendingFor lists the unprocessed answers of one question.
func (qs *Questions) PendingFor(questionID string) ([]core.Answer, error) {
	q, err := qs.Read(questionID)
	if err != nil {
		return nil, err
	}
	out := []core.Answer{}
	for i := range q.Answers {
		if qs.isUnprocessed(&q.Answers[i]) {
			out = append(out, q.Answers[i])
		}
	}
	return out, nil
}

// ---------- write ----------

func (qs *Questions) deriveState(q *core.Question) string {
	for _, a := range q.Answers {
		if qs.IsCommitted(a.AnswerID) {
			return "applied"
		}
	}
	if len(q.Answers) > 0 {
		all := true
		for _, a := range q.Answers {
			if a.Closed == nil {
				all = false
				break
			}
		}
		if all {
			return "closed"
		}
	}
	for _, a := range q.Answers {
		if a.ReceivedAt != nil {
			return "received"
		}
	}
	if len(q.Answers) > 0 {
		return "answered"
	}
	if q.State == "cancelled" {
		return "cancelled"
	}
	return "open"
}

func (qs *Questions) write(q *core.Question) error {
	q.State = qs.deriveState(q)
	if err := qs.store.WriteQuestion(q); err != nil {
		return err
	}
	qs.store.Emitter.Emit("question", qs.publicView(q))
	return nil
}

// Snapshot pins the current versions of records.
func (qs *Questions) Snapshot(ids []string) ([]core.SubjectRef, error) {
	out := make([]core.SubjectRef, 0, len(ids))
	for _, id := range ids {
		r, err := qs.store.ReadRecord(id)
		if err != nil {
			return nil, err
		}
		out = append(out, core.SubjectRef{ID: id, Rev: r.Rev, SHA256: r.SHA256})
	}
	return out, nil
}

// CheckSubjects compares a snapshot with the actual files now (rev and sha256).
func (qs *Questions) CheckSubjects(subjects []core.SubjectRef) []SubjectMismatch {
	out := []SubjectMismatch{}
	for _, s := range subjects {
		var current *Version
		if r, err := qs.store.ReadRecord(s.ID); err == nil {
			current = &Version{Rev: r.Rev, SHA256: r.SHA256}
		}
		if current == nil || current.Rev != s.Rev || current.SHA256 != s.SHA256 {
			out = append(out, SubjectMismatch{ID: s.ID, Expected: s, Current: current})
		}
	}
	return out
}

// Create pins the subjects and writes a new question.
func (qs *Questions) Create(in CreateQuestionInput) (*core.Question, error) {
	if err := qs.store.AssertAlive(); err != nil {
		return nil, err
	}
	if err := qs.assertQuiescent(); err != nil {
		return nil, err
	}
	if len(in.Subjects) == 0 {
		return nil, core.Errf("invalid", "a question needs at least one subject")
	}
	if !contains(core.QuestionKinds, in.Kind) {
		return nil, core.Errf("invalid", fmt.Sprintf("invalid question kind %q", in.Kind))
	}
	subjects, err := qs.Snapshot(in.Subjects)
	if err != nil {
		return nil, err
	}
	id := qs.store.AllocateID("question")
	allow := true
	if in.AllowFreeText != nil {
		allow = *in.AllowFreeText
	}
	options := in.Options
	if options == nil {
		options = []core.QuestionOption{}
	}
	q := &core.Question{
		ID: id, Rev: 1, State: "open", Kind: in.Kind, CreatedAt: core.NowISO(), CreatedBy: in.CreatedBy,
		Subjects: subjects, Prompt: in.Prompt, Options: options, AllowFreeText: allow,
		Answers: []core.Answer{}, Cancelled: nil,
	}
	if err := qs.write(q); err != nil {
		return nil, err
	}
	if _, err := qs.store.AppendEvent(core.Event{Type: "question.create", QuestionID: id, Subjects: subjects}); err != nil {
		return nil, err
	}
	return q, nil
}

// Answer saves a human answer, marking it stale if the subjects changed since asked.
func (qs *Questions) Answer(questionID string, in AnswerInput) (*core.Question, *core.Answer, error) {
	if err := qs.store.AssertAlive(); err != nil {
		return nil, nil, err
	}
	if err := qs.assertQuiescent(); err != nil {
		return nil, nil, err
	}
	q, err := qs.store.ReadQuestion(questionID)
	if err != nil {
		return nil, nil, err
	}
	if q.State == "cancelled" {
		return nil, nil, core.Errf("closed", fmt.Sprintf("question %s is cancelled", questionID))
	}
	hasText := in.Text != nil && strings.TrimSpace(*in.Text) != ""
	if len(in.Choice) == 0 && !hasText {
		return nil, nil, core.Errf("invalid", "answer needs a choice or text")
	}
	if (q.Kind == "single_choice" || q.Kind == "review") && len(in.Choice) > 1 {
		return nil, nil, core.Errf("invalid", "choose only one option")
	}
	if q.Kind != "free_text" && !q.AllowFreeText && hasText {
		return nil, nil, core.Errf("invalid", "free text is not allowed for this question")
	}
	if in.Choice != nil {
		keys := map[string]bool{}
		for _, o := range q.Options {
			keys[o.Key] = true
		}
		for _, c := range in.Choice {
			if !keys[c] {
				return nil, nil, core.Errf("invalid", "unknown option "+c)
			}
		}
	}
	ids := make([]string, len(q.Subjects))
	for i, s := range q.Subjects {
		ids[i] = s.ID
	}
	now, err := qs.Snapshot(ids)
	if err != nil {
		return nil, nil, err
	}
	stale := false
	for i, s := range q.Subjects {
		if now[i].Rev != s.Rev || now[i].SHA256 != s.SHA256 {
			stale = true
		}
	}
	a := core.Answer{
		AnswerID: core.AnswerID(q.ID, len(q.Answers)+1), AnsweredAt: core.NowISO(), SubjectsAtAnswer: now,
		Stale: stale, Choice: in.Choice, Text: in.Text,
	}
	q.Answers = append(q.Answers, a)
	if err := qs.write(q); err != nil { // persist first, then notify
		return nil, nil, err
	}
	if _, err := qs.store.AppendEvent(core.Event{Type: "answer.save", QuestionID: q.ID, AnswerID: a.AnswerID, Stale: stale}); err != nil {
		return nil, nil, err
	}
	return q, &q.Answers[len(q.Answers)-1], nil
}

// Ack records that an agent received an answer. It does not process it.
func (qs *Questions) Ack(questionID, answerID string) (*core.Answer, error) {
	if err := qs.store.AssertAlive(); err != nil {
		return nil, err
	}
	if err := qs.assertQuiescent(); err != nil {
		return nil, err
	}
	q, err := qs.store.ReadQuestion(questionID)
	if err != nil {
		return nil, err
	}
	a, err := findAnswer(q, answerID)
	if err != nil {
		return nil, err
	}
	if a.ReceivedAt == nil {
		now := core.NowISO()
		a.ReceivedAt = &now
		if err := qs.write(q); err != nil {
			return nil, err
		}
		if _, err := qs.store.AppendEvent(core.Event{Type: "answer.ack", QuestionID: q.ID, AnswerID: a.AnswerID}); err != nil {
			return nil, err
		}
	}
	return a, nil
}

// Close marks an unprocessed answer as handled without a decision.
func (qs *Questions) Close(questionID, answerID, reason string) (*core.Answer, error) {
	if err := qs.store.AssertAlive(); err != nil {
		return nil, err
	}
	if err := qs.assertQuiescent(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(reason) == "" {
		return nil, core.Errf("invalid", "close needs a reason")
	}
	q, err := qs.store.ReadQuestion(questionID)
	if err != nil {
		return nil, err
	}
	a, err := findAnswer(q, answerID)
	if err != nil {
		return nil, err
	}
	if qs.IsCommitted(a.AnswerID) {
		return nil, core.Errf("conflict", fmt.Sprintf("answer %s is already applied", a.AnswerID))
	}
	if a.Closed == nil {
		a.Closed = &core.ClosedInfo{At: core.NowISO(), Reason: reason}
		if err := qs.write(q); err != nil {
			return nil, err
		}
		if _, err := qs.store.AppendEvent(core.Event{Type: "answer.close", QuestionID: q.ID, AnswerID: a.AnswerID, Reason: reason}); err != nil {
			return nil, err
		}
	}
	return a, nil
}

// Cancel withdraws a question nobody has answered yet. Idempotent.
func (qs *Questions) Cancel(questionID, reason string, replacedBy *string) (*core.Question, error) {
	if err := qs.store.AssertAlive(); err != nil {
		return nil, err
	}
	if err := qs.assertQuiescent(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(reason) == "" {
		return nil, core.Errf("invalid", "cancel needs a reason")
	}
	q, err := qs.store.ReadQuestion(questionID)
	if err != nil {
		return nil, err
	}
	if q.Cancelled != nil {
		return q, nil
	}
	if len(q.Answers) > 0 {
		ids := make([]any, len(q.Answers))
		for i, a := range q.Answers {
			ids[i] = a.AnswerID
		}
		return nil, core.Errf("conflict", fmt.Sprintf("question %s already has answers; apply or close each answer instead", q.ID), "answers", ids)
	}
	if replacedBy != nil {
		if *replacedBy == q.ID {
			return nil, core.Errf("invalid", "a question cannot replace itself")
		}
		if _, err := qs.Read(*replacedBy); err != nil {
			return nil, err
		}
	}
	q.State = "cancelled"
	q.Cancelled = &core.Cancelled{At: core.NowISO(), Reason: reason, ReplacedBy: replacedBy}
	if err := qs.write(q); err != nil {
		return nil, err
	}
	if _, err := qs.store.AppendEvent(core.Event{Type: "question.cancel", QuestionID: q.ID, Reason: reason, ReplacedBy: replacedBy}); err != nil {
		return nil, err
	}
	return q, nil
}

// ---------- apply (SPEC-0001 §2.1) ----------

func (qs *Questions) assertQuiescent() error {
	pending, err := qs.RecoveryPending()
	if err != nil {
		return err
	}
	if pending {
		txids := []any{}
		for _, i := range qs.store.InflightIntents() {
			txids = append(txids, i.TxID)
		}
		return core.Errf("inflight", "an apply transaction is in flight; recovery must run first", "txids", txids)
	}
	return nil
}

// RecoveryPending reports an in-flight intent or an aborted one not cleaned up yet.
func (qs *Questions) RecoveryPending() (bool, error) {
	if len(qs.store.InflightIntents()) > 0 {
		return true, nil
	}
	aborted := qs.store.AbortedTxids()
	for _, i := range qs.store.Intents() {
		if !aborted[i.TxID] {
			continue
		}
		if qs.store.HasRecord(i.DecisionID) {
			d, err := qs.store.ReadRecord(i.DecisionID)
			if err != nil {
				return false, err
			}
			if tx, _ := d.Frontmatter.Str("txid"); tx == i.TxID {
				return true, nil
			}
		}
		q, err := qs.store.ReadQuestion(i.QuestionID)
		if err != nil {
			var re *core.RuntimeError
			if errors.As(err, &re) && re.Code == "dead" {
				return false, err
			}
			return true, nil // its clean-up cannot be checked; status shows why (Unrecoverable)
		}
		for _, a := range q.Answers {
			if a.AnswerID == i.AnswerID && ((a.AppliedAs != nil && a.AppliedAs.TxID == i.TxID) || !a.Stale) {
				return true, nil
			}
		}
	}
	return false, nil
}

func findAnswer(q *core.Question, answerID string) (*core.Answer, error) {
	for i := range q.Answers {
		if q.Answers[i].AnswerID == answerID {
			return &q.Answers[i], nil
		}
	}
	return nil, core.Errf("not_found", fmt.Sprintf("answer %s not found in %s", answerID, q.ID))
}

// Apply turns an answer into a decision: intent → decision file → question file →
// commit. Only the commit confirms it; a retry returns the original decision.
func (qs *Questions) Apply(questionID, answerID string, decision core.DecisionDraft, expectedQuestionRev *int64, hooks *ApplyHooks) (ApplyResult, error) {
	if err := qs.store.AssertAlive(); err != nil {
		return ApplyResult{}, err
	}
	if err := qs.assertQuiescent(); err != nil {
		return ApplyResult{}, err
	}
	q, err := qs.store.ReadQuestion(questionID)
	if err != nil {
		return ApplyResult{}, err
	}
	a, err := findAnswer(q, answerID)
	if err != nil {
		return ApplyResult{}, err
	}
	if expectedQuestionRev != nil && *expectedQuestionRev != q.Rev {
		return ApplyResult{}, core.Errf("question_rev", fmt.Sprintf("question %s is at rev %d, expected %d", q.ID, q.Rev, *expectedQuestionRev))
	}
	// 1. idempotent retry
	if c := qs.store.CommittedIntentFor(a.AnswerID); c != nil {
		return ApplyResult{Status: "already_applied", DecisionID: c.DecisionID, TxID: c.TxID}, nil
	}
	// 2. closed
	if a.Closed != nil {
		return ApplyResult{}, core.Errf("closed", fmt.Sprintf("answer %s was closed: %s", a.AnswerID, a.Closed.Reason))
	}
	// 3. version check against the actual files
	mismatches := qs.CheckSubjects(q.Subjects)
	if a.Stale || len(mismatches) > 0 {
		if !a.Stale {
			a.Stale = true
			if err := qs.write(q); err != nil {
				return ApplyResult{}, err
			}
		}
		list := make([]any, 0, len(mismatches))
		for _, m := range mismatches {
			v, _ := core.ToOMapValue(m)
			list = append(list, v)
		}
		return ApplyResult{}, core.Errf("stale", fmt.Sprintf("answer %s targets an older version of its subjects", a.AnswerID), "mismatches", list)
	}
	// 4. stage a: intent (allocates the decision id exactly once)
	if decision.Alternatives == nil {
		decision.Alternatives = []string{}
	}
	d := decision
	intent, err := qs.store.AppendEvent(core.Event{
		Type: "apply.intent", TxID: core.RandomID(8), QuestionID: q.ID, AnswerID: a.AnswerID,
		DecisionID: qs.store.AllocateID("decision"), Subjects: q.Subjects, Decision: &d,
	})
	if err != nil {
		return ApplyResult{}, err
	}
	if err := qs.afterStage(hookOf(hooks), "intent"); err != nil {
		return ApplyResult{}, err
	}
	return qs.rollForward(intent, hooks)
}

func hookOf(h *ApplyHooks) func(string) error {
	if h == nil {
		return nil
	}
	return h.AfterStage
}

func recoveryHookOf(h *RecoveryHooks) func(string) error {
	if h == nil {
		return nil
	}
	return h.AfterStage
}

func (qs *Questions) afterStage(hook func(string) error, stage string) error {
	if hook == nil {
		return nil
	}
	err := hook(stage)
	var crash *core.SimulatedCrash
	if errors.As(err, &crash) {
		qs.store.Kill()
	}
	return err
}

// rollForward runs stages b, c and d; each skips work already done.
func (qs *Questions) rollForward(intent core.Event, hooks *ApplyHooks) (ApplyResult, error) {
	hook := hookOf(hooks)
	// the question's file holds the answers, which the journal does not; without it the
	// transaction cannot be finished, so nothing is written for it
	if _, err := qs.store.ReadQuestion(intent.QuestionID); err != nil {
		return ApplyResult{}, qs.missingQuestion(intent, err)
	}
	// stage b: decision file (provisional until commit)
	if !qs.store.HasRecord(intent.DecisionID) {
		d := intent.Decision
		if d == nil {
			d = &core.DecisionDraft{Alternatives: []string{}}
		}
		alts := make([]any, len(d.Alternatives))
		for i, x := range d.Alternatives {
			alts[i] = x
		}
		reviewed := make([]any, len(intent.Subjects))
		ids := make([]any, len(intent.Subjects))
		for i, s := range intent.Subjects {
			reviewed[i] = core.OM("id", s.ID, "rev", s.Rev, "sha256", s.SHA256)
			ids[i] = s.ID
		}
		fm := core.OM("kind", "decision", "title", d.Title, "status", "accepted",
			"question", intent.QuestionID, "answer", intent.AnswerID, "txid", intent.TxID)
		if d.Chosen != nil {
			fm.Set("chosen", *d.Chosen)
		}
		fm.Set("alternatives", alts)
		fm.Set("reviewed", reviewed)
		fm.Set("links", core.OM("rationale_for", ids))
		if _, err := qs.store.WriteRecord(intent.DecisionID, fm, d.Body, nil); err != nil {
			return ApplyResult{}, err
		}
	}
	if err := qs.afterStage(hook, "decision"); err != nil {
		return ApplyResult{}, err
	}
	// stage c: applied_as on the answer (provisional until commit)
	q, err := qs.store.ReadQuestion(intent.QuestionID)
	if err != nil {
		return ApplyResult{}, err
	}
	a, err := findAnswer(q, intent.AnswerID)
	if err != nil {
		return ApplyResult{}, err
	}
	if a.AppliedAs == nil || a.AppliedAs.TxID != intent.TxID {
		a.AppliedAs = &core.AppliedAs{DecisionID: intent.DecisionID, TxID: intent.TxID}
		if err := qs.write(q); err != nil {
			return ApplyResult{}, err
		}
	}
	if err := qs.afterStage(hook, "question"); err != nil {
		return ApplyResult{}, err
	}
	// stage d: commit — the only confirmation point
	if !qs.store.IsCommitted(intent.TxID) {
		if _, err := qs.store.AppendEvent(core.Event{Type: "apply.commit", TxID: intent.TxID}); err != nil {
			return ApplyResult{}, err
		}
	}
	if err := qs.afterStage(hook, "commit"); err != nil {
		return ApplyResult{}, err
	}
	q2, err := qs.store.ReadQuestion(intent.QuestionID)
	if err != nil {
		return ApplyResult{}, err
	}
	if err := qs.write(q2); err != nil { // refresh derived state
		return ApplyResult{}, err
	}
	return ApplyResult{Status: "applied", DecisionID: intent.DecisionID, TxID: intent.TxID}, nil
}

// ---------- recovery (SPEC-0001 §2.2) ----------

// Recover rolls in-flight intents forward when their subjects are unchanged and aborts
// them otherwise, then repeats the idempotent clean-up for every past transaction.
func (qs *Questions) Recover(hooks *RecoveryHooks) (RecoveryReport, error) {
	report := RecoveryReport{Recovered: []RecoveryItem{}, Retracted: []string{}}
	if err := qs.store.AssertAlive(); err != nil {
		return report, err
	}
	for _, intent := range qs.store.InflightIntents() {
		mismatches := qs.CheckSubjects(intent.Subjects)
		if len(mismatches) == 0 {
			if _, err := qs.rollForward(intent, nil); err != nil {
				return report, err
			}
			report.Recovered = append(report.Recovered, RecoveryItem{TxID: intent.TxID, AnswerID: intent.AnswerID, DecisionID: intent.DecisionID, Outcome: "forward"})
			continue
		}
		ids := make([]string, len(mismatches))
		for i, m := range mismatches {
			ids[i] = m.ID
		}
		if _, err := qs.store.AppendEvent(core.Event{Type: "apply.abort", TxID: intent.TxID, Reason: "subjects changed: " + strings.Join(ids, ", ")}); err != nil {
			return report, err
		}
		if err := qs.afterStage(recoveryHookOf(hooks), "abort"); err != nil {
			return report, err
		}
		if _, err := qs.retract(intent, hooks); err != nil {
			return report, err
		}
		reason := strings.Join(ids, ", ")
		report.Recovered = append(report.Recovered, RecoveryItem{TxID: intent.TxID, AnswerID: intent.AnswerID, DecisionID: intent.DecisionID, Outcome: "abort", Reason: &reason})
	}
	aborted, committed := qs.store.AbortedTxids(), qs.store.CommittedTxids()
	for _, intent := range qs.store.Intents() {
		if aborted[intent.TxID] {
			changed, err := qs.retract(intent, hooks)
			if err != nil {
				return report, err
			}
			if changed {
				report.Retracted = append(report.Retracted, intent.TxID)
			}
		} else if committed[intent.TxID] {
			if err := qs.ensureCommittedFiles(intent); err != nil {
				return report, err
			}
		}
	}
	return report, nil
}

// retract removes the provisional files of an aborted transaction.
func (qs *Questions) retract(intent core.Event, hooks *RecoveryHooks) (bool, error) {
	hook := recoveryHookOf(hooks)
	changed := false
	if qs.store.HasRecord(intent.DecisionID) {
		d, err := qs.store.ReadRecord(intent.DecisionID)
		if err != nil {
			return false, err
		}
		if tx, _ := d.Frontmatter.Str("txid"); tx == intent.TxID {
			if err := qs.store.RemoveRecord(intent.DecisionID, "aborted transaction "+intent.TxID); err != nil {
				return false, err
			}
			if err := qs.afterStage(hook, "retract_decision"); err != nil {
				return false, err
			}
			changed = true
		}
	}
	q, err := qs.store.ReadQuestion(intent.QuestionID)
	if err != nil {
		var re *core.RuntimeError
		if errors.As(err, &re) && re.Code == "dead" {
			return changed, err
		}
		return changed, nil
	}
	for i := range q.Answers {
		a := &q.Answers[i]
		if a.AnswerID != intent.AnswerID {
			continue
		}
		if (a.AppliedAs != nil && a.AppliedAs.TxID == intent.TxID) || !a.Stale {
			if a.AppliedAs != nil && a.AppliedAs.TxID == intent.TxID {
				a.AppliedAs = nil
			}
			a.Stale = true
			if err := qs.write(q); err != nil {
				return changed, err
			}
			if err := qs.afterStage(hook, "retract_question"); err != nil {
				return changed, err
			}
			changed = true
		}
		break
	}
	return changed, nil
}

func (qs *Questions) ensureCommittedFiles(intent core.Event) error {
	if !qs.store.HasRecord(intent.DecisionID) {
		// committed but the decision file is gone (manual deletion): rewrite it from the journal
		_, err := qs.rollForward(intent, nil)
		return err
	}
	q, err := qs.store.ReadQuestion(intent.QuestionID)
	if err != nil {
		return qs.missingQuestion(intent, err)
	}
	for i := range q.Answers {
		a := &q.Answers[i]
		if a.AnswerID == intent.AnswerID && (a.AppliedAs == nil || a.AppliedAs.TxID != intent.TxID) {
			a.AppliedAs = &core.AppliedAs{DecisionID: intent.DecisionID, TxID: intent.TxID}
			return qs.write(q)
		}
	}
	return nil
}

// missingQuestion says which file to restore when a transaction's question file is
// gone: the answers in it are not in the journal, so it cannot be written again.
func (qs *Questions) missingQuestion(intent core.Event, err error) error {
	var re *core.RuntimeError
	if !errors.As(err, &re) || re.Code != "not_found" {
		return err
	}
	path := filepath.Join(qs.store.Config.RecordsDir, "questions", intent.QuestionID+".json")
	return core.Errf("not_found", fmt.Sprintf("transaction %s of %s needs %s, which is missing: the answers in it are not in the journal, so it cannot be written again; restore the file (from git, for example) and start again", intent.TxID, intent.AnswerID, path), "question", intent.QuestionID, "path", path)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
