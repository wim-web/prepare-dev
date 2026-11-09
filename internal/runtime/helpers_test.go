package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wim-web/prepare-dev/internal/core"
	"github.com/wim-web/prepare-dev/internal/store"
)

func openRT(t *testing.T, root string, opts ...Options) *Runtime {
	t.Helper()
	o := Options{}
	if len(opts) > 0 {
		o = opts[0]
	}
	rt, err := Open(root, o)
	if err != nil {
		t.Fatal(err)
	}
	return rt
}

type testFailure struct{ err error }

// ok returns v or fails the running test (through run's guard).
func ok[T any](v T, err error) T {
	if err != nil {
		panic(testFailure{err})
	}
	return v
}

func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// run is t.Run with a guard that turns ok's failures into t.Fatal.
func run(t *testing.T, name string, fn func(t *testing.T)) {
	t.Run(name, func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				if f, isFail := r.(testFailure); isFail {
					t.Fatalf("%v", f.err)
				}
				panic(r)
			}
		}()
		fn(t)
	})
}

func mustErr(t *testing.T, err error, contains string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q", contains)
	}
	if contains != "" && !strings.Contains(err.Error(), contains) {
		t.Fatalf("error %q does not contain %q", err.Error(), contains)
	}
}

func isCrash(err error) bool {
	var c *core.SimulatedCrash
	return errors.As(err, &c)
}

func seed(t *testing.T, rt *Runtime) string {
	t.Helper()
	r := ok(rt.Store.CreateRecord("requirement", core.OM("title", "回答は依頼した版にのみ一度だけ反映する", "status", "proposed"), "本文 v1\n"))
	return r.ID
}

func ptr[T any](v T) *T { return &v }

func ask(t *testing.T, rt *Runtime, subject string) *core.Question {
	t.Helper()
	return ok(rt.Questions.Create(CreateQuestionInput{
		Kind: "single_choice", Prompt: "対象版が変わっていた場合の扱いは？", Subjects: []string{subject},
		Options: []core.QuestionOption{{Key: "hold", Label: "保存して再確認", Recommended: ptr(true)}, {Key: "apply", Label: "そのまま反映"}},
	}))
}

var testDecision = core.DecisionDraft{Title: "保存して再確認する", Body: "理由: 古い版への誤適用を避ける。\n", Chosen: ptr("hold"), Alternatives: []string{"apply"}}

func crashAfter(stage string) *ApplyHooks {
	return &ApplyHooks{AfterStage: func(s string) error {
		if s == stage {
			return &core.SimulatedCrash{Stage: s}
		}
		return nil
	}}
}

func answerChoice(t *testing.T, rt *Runtime, qid, key string) *core.Answer {
	t.Helper()
	_, a, err := rt.Questions.Answer(qid, AnswerInput{Choice: []string{key}})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// rewriteBody edits a record on disk the way an external editor would (rev untouched).
func rewriteBody(t *testing.T, root, rel, body string) {
	t.Helper()
	abs := filepath.Join(root, rel)
	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	fm, _, err := core.ParseMarkdown(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(core.SerializeMarkdown(fm, body)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readRec(t *testing.T, st *store.Store, id string) *store.RecordView {
	t.Helper()
	return ok(st.ReadRecord(id))
}

func ids(list []*store.RecordView) []string {
	out := []string{}
	for _, r := range list {
		out = append(out, r.ID)
	}
	return out
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
