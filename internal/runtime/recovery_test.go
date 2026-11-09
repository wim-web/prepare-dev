package runtime

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wim-web/prepare-dev/internal/core"
	"github.com/wim-web/prepare-dev/internal/store"
)

type recoveryObs struct {
	Answer   int  `json:"answer"`
	Aborted  bool `json:"aborted"`
	Decision bool `json:"decision"`
	Applied  bool `json:"applied"`
	Stale    bool `json:"stale"`
	Ready    bool `json:"ready"`
}

type recoveryTrace struct {
	Stage   string      `json:"stage"`
	Initial recoveryObs `json:"initial"`
	Steps   []struct {
		Action  string      `json:"action"`
		Observe recoveryObs `json:"observe"`
	} `json:"steps"`
}

// [C3][C5][C6] replays the Lean recovery traces, including repeated crashes at every
// abort-cleanup write.
func TestRecoveryTraces(t *testing.T) {
	run(t, "[C3][C5][C6] replays Lean recovery traces including repeated crashes at every abort-cleanup write", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join(repoRoot(), "model", "traces", "recovery.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		var traces []recoveryTrace
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var tr recoveryTrace
			if err := json.Unmarshal([]byte(line), &tr); err != nil {
				t.Fatal(err)
			}
			if tr.Stage != "intent" && tr.Stage != "decision" && tr.Stage != "question" {
				t.Fatalf("bad stage %q", tr.Stage)
			}
			traces = append(traces, tr)
		}
		if len(traces) != 48 {
			t.Fatalf("expected 48 traces, got %d", len(traces))
		}
		for ti, tr := range traces {
			func() {
				root := t.TempDir()
				rt, err := Open(root, Options{})
				if err != nil {
					t.Fatal(err)
				}
				req, err := rt.Store.CreateRecord("requirement", core.OM("title", "R"), "before")
				if err != nil {
					t.Fatal(err)
				}
				q, err := rt.Questions.Create(CreateQuestionInput{Kind: "free_text", Prompt: "Q", Subjects: []string{req.ID}})
				if err != nil {
					t.Fatal(err)
				}
				text := "saved answer"
				_, ans, err := rt.Questions.Answer(q.ID, AnswerInput{Text: &text})
				if err != nil {
					t.Fatal(err)
				}
				answerID := ans.AnswerID
				_, err = rt.Questions.Apply(q.ID, answerID, core.DecisionDraft{Title: "D", Body: "D", Alternatives: []string{}}, nil, &ApplyHooks{AfterStage: func(s string) error {
					if s == tr.Stage {
						return &core.SimulatedCrash{Stage: s}
					}
					return nil
				}})
				var crash *core.SimulatedCrash
				if !errors.As(err, &crash) {
					t.Fatalf("trace %d: expected a simulated crash, got %v", ti, err)
				}
				f, err := os.OpenFile(filepath.Join(root, req.Path), os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = f.WriteString("\nchanged while stopped")
				f.Close()
				observe := func() recoveryObs {
					st, err := store.Open(root)
					if err != nil {
						t.Fatal(err)
					}
					qs := &Questions{store: st}
					raw, err := st.ReadQuestion(q.ID)
					if err != nil {
						t.Fatal(err)
					}
					a := raw.Answers[0]
					if a.AnswerID != answerID || a.Text == nil || *a.Text != "saved answer" {
						t.Fatalf("trace %d: answer lost or changed: %+v", ti, a)
					}
					pending, err := qs.RecoveryPending()
					if err != nil {
						t.Fatal(err)
					}
					if pending {
						if _, err := qs.Ack(q.ID, answerID); err == nil || !strings.Contains(err.Error(), "recovery must run first") {
							t.Fatalf("trace %d: ack during clean-up should be refused, got %v", ti, err)
						}
					}
					pub, err := qs.Read(q.ID)
					if err != nil {
						t.Fatal(err)
					}
					if pub.Answers[0].AppliedAs != nil {
						t.Fatalf("trace %d: provisional applied_as is public", ti)
					}
					return recoveryObs{Answer: 1, Aborted: len(st.AbortedTxids()) > 0, Decision: st.HasRecord("DEC-0001"), Applied: a.AppliedAs != nil, Stale: a.Stale, Ready: !pending}
				}
				if got := observe(); got != tr.Initial {
					t.Fatalf("trace %d initial: got %+v want %+v", ti, got, tr.Initial)
				}
				for si, step := range tr.Steps {
					if step.Action == "resume" {
						_, err := Open(root, Options{RecoveryHooks: &RecoveryHooks{AfterStage: func(s string) error { return &core.SimulatedCrash{Stage: s} }}})
						if err != nil && !errors.As(err, &crash) {
							t.Fatalf("trace %d step %d: %v", ti, si, err)
						}
					}
					if got := observe(); got != step.Observe {
						t.Fatalf("trace %d step %d (%s): got %+v want %+v", ti, si, step.Action, got, step.Observe)
					}
				}
				recovered, err := Open(root, Options{})
				if err != nil {
					t.Fatal(err)
				}
				view, err := recovered.Status.View()
				if err != nil {
					t.Fatal(err)
				}
				if view.RecoveryPending {
					t.Fatalf("trace %d: recovery still pending", ti)
				}
				pending, err := recovered.Questions.Pending()
				if err != nil {
					t.Fatal(err)
				}
				if len(pending) == 0 || pending[0].AnswerID != answerID {
					t.Fatalf("trace %d: answer not pending after recovery", ti)
				}
				pub, err := recovered.Trace.PublicDecisions()
				if err != nil {
					t.Fatal(err)
				}
				if len(pub) != 0 {
					t.Fatalf("trace %d: a decision became public", ti)
				}
			}()
		}
	})
}
