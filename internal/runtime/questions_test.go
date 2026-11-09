package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wim-web/prepare-dev/internal/core"
)

// SPEC-0001: questions, answers, the staged apply and recovery.
func TestQuestions(t *testing.T) {
	run(t, "[U2] accepts question data, preserves legacy display fields, and rejects invalid answers", func(t *testing.T) {
		rt := openRT(t, t.TempDir())
		req := seed(t, rt)
		q := ask(t, rt, req)
		if q.UI != nil {
			t.Fatal("a new question has ui data")
		}
		legacy := q.Clone()
		legacy.UI = json.RawMessage(`{"spec":{"old":true}}`)
		noErr(t, rt.Store.WriteQuestion(legacy))
		got := ok(rt.Questions.Read(q.ID))
		if string(got.UI) != `{"spec":{"old":true}}` {
			t.Fatalf("legacy ui not preserved: %s", got.UI)
		}
		_, _, err := rt.Questions.Answer(q.ID, AnswerInput{Choice: []string{"hold", "apply"}})
		mustErr(t, err, "only one")
		free := ok(rt.Questions.Create(CreateQuestionInput{Kind: "free_text", Prompt: "条件を教えてください", Subjects: []string{req}}))
		_, _, err = rt.Questions.Answer(free.ID, AnswerInput{Text: ptr("  ")})
		mustErr(t, err, "choice or text")
		_, a, err := rt.Questions.Answer(free.ID, AnswerInput{Text: ptr("待ち時間を優先")})
		if err != nil || *a.Text != "待ち時間を優先" {
			t.Fatalf("free text answer: %v", err)
		}
	})

	run(t, "[C1][C3][C6] public reads and notifications never expose a provisional application", func(t *testing.T) {
		root := t.TempDir()
		rt := openRT(t, root)
		q := ask(t, rt, seed(t, rt))
		first := answerChoice(t, rt, q.ID, "hold")
		ok(rt.Questions.Apply(q.ID, first.AnswerID, testDecision, nil, nil))
		second := answerChoice(t, rt, q.ID, "hold")
		type seen struct {
			answer             string
			applied, committed bool
		}
		var events []seen
		rt.Store.Emitter.On(func(name string, payload any) {
			if name != "question" {
				return
			}
			for _, a := range payload.(*core.Question).Answers {
				events = append(events, seen{a.AnswerID, a.AppliedAs != nil, rt.Questions.IsCommitted(a.AnswerID)})
			}
		})
		_, err := rt.Questions.Apply(q.ID, second.AnswerID, testDecision, nil, &ApplyHooks{AfterStage: func(stage string) error {
			if stage != "question" {
				return nil
			}
			raw := ok(rt.Store.ReadQuestion(q.ID))
			if raw.Answers[1].AppliedAs == nil {
				t.Fatal("stage c did not write applied_as")
			}
			if ok(rt.Questions.Read(q.ID)).Answers[1].AppliedAs != nil {
				t.Fatal("read exposes a provisional applied_as")
			}
			if ok(rt.Questions.List())[0].Answers[1].AppliedAs != nil {
				t.Fatal("list exposes a provisional applied_as")
			}
			if ok(rt.Questions.PendingFor(q.ID))[0].AppliedAs != nil {
				t.Fatal("pending exposes a provisional applied_as")
			}
			return &core.SimulatedCrash{Stage: stage}
		}})
		if !isCrash(err) {
			t.Fatalf("expected a crash, got %v", err)
		}
		if len(events) == 0 {
			t.Fatal("no notifications")
		}
		for _, e := range events {
			if e.applied != e.committed {
				t.Fatalf("notification shows applied=%v for %s, committed=%v", e.applied, e.answer, e.committed)
			}
		}
		restarted := openRT(t, root)
		for _, a := range ok(restarted.Questions.Read(q.ID)).Answers {
			if a.AppliedAs == nil {
				t.Fatal("restart did not roll forward")
			}
		}
	})

	run(t, "[C1] M1-1,2,7: create, answer, wait, apply once, trace", func(t *testing.T) {
		rt := openRT(t, t.TempDir())
		req := seed(t, rt)
		q := ask(t, rt, req)
		done := make(chan WaitResult, 1)
		go func() {
			res, err := rt.WaitQuestion(context.Background(), q.ID, 5*time.Second)
			if err != nil {
				t.Error(err)
			}
			done <- res
		}()
		time.Sleep(50 * time.Millisecond)
		var answer *core.Answer
		ok(0, rt.Do(func() error {
			_, a, err := rt.Questions.Answer(q.ID, AnswerInput{Choice: []string{"hold"}})
			answer = a
			return err
		}))
		w := <-done
		if w.Status != "answered" || w.Answers[0].AnswerID != answer.AnswerID {
			t.Fatalf("wait: %+v", w)
		}
		if p := ok(rt.Questions.Pending()); len(p) != 1 || p[0].AnswerID != answer.AnswerID {
			t.Fatalf("pending: %+v", p)
		}
		ok(rt.Questions.Ack(q.ID, answer.AnswerID))
		if p := ok(rt.Questions.Pending()); len(p) != 1 {
			t.Fatal("ack must not process the answer")
		}
		r1 := ok(rt.Questions.Apply(q.ID, answer.AnswerID, testDecision, nil, nil))
		r2 := ok(rt.Questions.Apply(q.ID, answer.AnswerID, testDecision, nil, nil))
		if r1.Status != "applied" || r2.Status != "already_applied" || r2.DecisionID != r1.DecisionID {
			t.Fatalf("apply: %+v %+v", r1, r2)
		}
		if p := ok(rt.Questions.Pending()); len(p) != 0 {
			t.Fatal("still pending after apply")
		}
		if d := ids(ok(rt.Store.ListRecords("decision"))); !eqStrings(d, []string{r1.DecisionID}) {
			t.Fatalf("decisions: %v", d)
		}
		tr := ok(rt.Trace.For(req))
		if len(tr.Decisions) != 1 || tr.Decisions[0].ID != r1.DecisionID || !tr.Decisions[0].Committed || !tr.Decisions[0].ReviewedCurrent {
			t.Fatalf("trace decisions: %+v", tr.Decisions)
		}
		if a := tr.Questions[0].Answers[0]; a.AppliedAs == nil || *a.AppliedAs != r1.DecisionID {
			t.Fatalf("trace answer: %+v", a)
		}
	})

	run(t, "[C3] M1-3: wait interrupted, then answered: pending still returns it", func(t *testing.T) {
		rt := openRT(t, t.TempDir())
		q := ask(t, rt, seed(t, rt))
		w := ok(rt.WaitQuestion(context.Background(), q.ID, 50*time.Millisecond))
		if w.Status != "pending" {
			t.Fatalf("wait: %s", w.Status)
		}
		a := answerChoice(t, rt, q.ID, "hold")
		if p := ok(rt.Questions.Pending()); len(p) != 1 || p[0].AnswerID != a.AnswerID {
			t.Fatalf("pending: %+v", p)
		}
		if w2 := ok(rt.WaitQuestion(context.Background(), q.ID, 50*time.Millisecond)); w2.Status != "answered" {
			t.Fatalf("wait after answer: %s", w2.Status)
		}
	})

	run(t, "[C3] M1-4: ack then restart before apply: the answer stays pending", func(t *testing.T) {
		root := t.TempDir()
		rt := openRT(t, root)
		q := ask(t, rt, seed(t, rt))
		a := answerChoice(t, rt, q.ID, "hold")
		ok(rt.Questions.Ack(q.ID, a.AnswerID))
		rt2 := openRT(t, root)
		p := ok(rt2.Questions.Pending())
		if len(p) != 1 || p[0].AnswerID != a.AnswerID || !p[0].Received {
			t.Fatalf("pending after restart: %+v", p)
		}
		if v := ok(rt2.Status.View()); len(v.Pending) != 1 {
			t.Fatal("status pending")
		}
	})

	run(t, "[C2] M1-5: subject edited through the runtime after the answer: apply is rejected as stale", func(t *testing.T) {
		rt := openRT(t, t.TempDir())
		req := seed(t, rt)
		q := ask(t, rt, req)
		a := answerChoice(t, rt, q.ID, "hold")
		cur := readRec(t, rt.Store, req)
		ok(rt.Store.WriteRecord(req, core.NewOMap(), "本文 v2\n", expected(cur.Rev, cur.SHA256)))
		_, err := rt.Questions.Apply(q.ID, a.AnswerID, testDecision, nil, nil)
		mustErr(t, err, "older version")
		if !ok(rt.Questions.Read(q.ID)).Answers[0].Stale {
			t.Fatal("answer not marked stale")
		}
		if p := ok(rt.Questions.Pending()); len(p) != 1 || !p[0].Stale {
			t.Fatal("stale answer must stay pending for re-confirmation")
		}
		if d := ok(rt.Store.ListRecords("decision")); len(d) != 0 {
			t.Fatal("a decision was created")
		}
	})

	run(t, "[C2] M1-6: external edit without rev change and index regeneration: apply is rejected", func(t *testing.T) {
		root := t.TempDir()
		rt := openRT(t, root)
		req := seed(t, rt)
		q := ask(t, rt, req)
		a := answerChoice(t, rt, q.ID, "hold")
		rewriteBody(t, root, readRec(t, rt.Store, req).Path, "外部エディタで書き換えた本文\n")
		if err := os.Remove(filepath.Join(root, ".prepare-dev", "index.json")); err != nil {
			t.Fatal(err)
		}
		rt2 := openRT(t, root)
		changes := 0
		for _, e := range rt2.Store.Events() {
			if e.Type == "external_change" {
				changes++
			}
		}
		if changes != 1 || readRec(t, rt2.Store, req).Rev != 2 {
			t.Fatalf("external change not detected: %d", changes)
		}
		_, err := rt2.Questions.Apply(q.ID, a.AnswerID, testDecision, nil, nil)
		mustErr(t, err, "older version")
		if !ok(rt2.Questions.Read(q.ID)).Answers[0].Stale {
			t.Fatal("answer not marked stale")
		}
	})

	run(t, "[C1][C3] M1-8: crash after stage a, b or c, restart: exactly one decision, rolled forward", func(t *testing.T) {
		for _, stage := range []string{"intent", "decision", "question"} {
			root := t.TempDir()
			rt := openRT(t, root)
			q := ask(t, rt, seed(t, rt))
			a := answerChoice(t, rt, q.ID, "hold")
			if _, err := rt.Questions.Apply(q.ID, a.AnswerID, testDecision, nil, crashAfter(stage)); !isCrash(err) {
				t.Fatalf("%s: expected a crash, got %v", stage, err)
			}
			_, err := rt.Questions.Pending()
			mustErr(t, err, "dead")
			rt2 := openRT(t, root)
			if len(rt2.Recovery.Recovered) != 1 || rt2.Recovery.Recovered[0].Outcome != "forward" {
				t.Fatalf("%s: recovery %+v", stage, rt2.Recovery)
			}
			if len(rt2.Store.InflightIntents()) != 0 {
				t.Fatalf("%s: still in flight", stage)
			}
			decisions := ok(rt2.Store.ListRecords("decision"))
			if len(decisions) != 1 || len(ok(rt2.Questions.Pending())) != 0 {
				t.Fatalf("%s: decisions %v", stage, ids(decisions))
			}
			r := ok(rt2.Questions.Apply(q.ID, a.AnswerID, testDecision, nil, nil))
			if r.Status != "already_applied" || r.DecisionID != decisions[0].ID {
				t.Fatalf("%s: retry %+v", stage, r)
			}
		}
	})

	run(t, "[C2][C3] M1-9: crash after stage c, subject edited while down, restart: abort, nothing public, answer pending and stale", func(t *testing.T) {
		root := t.TempDir()
		rt := openRT(t, root)
		req := seed(t, rt)
		q := ask(t, rt, req)
		a := answerChoice(t, rt, q.ID, "hold")
		if _, err := rt.Questions.Apply(q.ID, a.AnswerID, testDecision, nil, crashAfter("question")); !isCrash(err) {
			t.Fatalf("expected a crash, got %v", err)
		}
		dec := filepath.Join(root, "prepare", "decisions", "DEC-0001.md")
		if _, err := os.Stat(dec); err != nil {
			t.Fatal("provisional decision file missing")
		}
		rewriteBody(t, root, filepath.Join("prepare", "requirements", req+".md"), "停止中に編集\n")
		rt2 := openRT(t, root)
		if rt2.Recovery.Recovered[0].Outcome != "abort" || len(rt2.Store.InflightIntents()) != 0 || len(rt2.Store.AbortedTxids()) != 1 {
			t.Fatalf("recovery: %+v", rt2.Recovery)
		}
		if _, err := os.Stat(dec); err == nil {
			t.Fatal("aborted decision file still exists")
		}
		if pub := ok(rt2.Trace.PublicDecisions()); len(pub) != 0 {
			t.Fatal("a decision is public")
		}
		if v := ok(rt2.Status.View()); len(v.StaleDecisions) != 0 {
			t.Fatal("stale decisions")
		}
		p := ok(rt2.Questions.Pending())
		if len(p) != 1 || p[0].AnswerID != a.AnswerID || !p[0].Stale {
			t.Fatalf("pending: %+v", p)
		}
		if ok(rt2.Questions.Read(q.ID)).Answers[0].AppliedAs != nil {
			t.Fatal("applied_as not retracted")
		}
		if id := rt2.Store.AllocateID("decision"); id != "DEC-0002" {
			t.Fatalf("the aborted decision id must not be reused: %s", id)
		}
	})

	run(t, "[C1] M1-10: crash after commit before the response, subject edited, retry: original result, no retraction", func(t *testing.T) {
		root := t.TempDir()
		rt := openRT(t, root)
		req := seed(t, rt)
		q := ask(t, rt, req)
		a := answerChoice(t, rt, q.ID, "hold")
		if _, err := rt.Questions.Apply(q.ID, a.AnswerID, testDecision, nil, crashAfter("commit")); !isCrash(err) {
			t.Fatalf("expected a crash, got %v", err)
		}
		rewriteBody(t, root, filepath.Join("prepare", "requirements", req+".md"), "確定後に編集\n")
		rt2 := openRT(t, root)
		if len(rt2.Recovery.Recovered) != 0 {
			t.Fatalf("a committed transaction was recovered: %+v", rt2.Recovery)
		}
		r := ok(rt2.Questions.Apply(q.ID, a.AnswerID, testDecision, nil, nil))
		if r.Status != "already_applied" || r.DecisionID != "DEC-0001" {
			t.Fatalf("retry: %+v", r)
		}
		if pub := ids(ok(rt2.Trace.PublicDecisions())); !eqStrings(pub, []string{"DEC-0001"}) {
			t.Fatalf("public decisions: %v", pub)
		}
		view := rt2.Trace.DecisionView(readRec(t, rt2.Store, "DEC-0001"))
		if !view.Committed || view.ReviewedCurrent {
			t.Fatalf("decision view: %+v", view)
		}
		if len(ok(rt2.Questions.Pending())) != 0 {
			t.Fatal("pending after commit")
		}
	})

	run(t, "close: closes an unprocessed answer and refuses to close an applied one", func(t *testing.T) {
		rt := openRT(t, t.TempDir())
		q := ask(t, rt, seed(t, rt))
		a := answerChoice(t, rt, q.ID, "apply")
		ok(rt.Questions.Close(q.ID, a.AnswerID, "再質問で置き換え"))
		if len(ok(rt.Questions.Pending())) != 0 {
			t.Fatal("closed answer still pending")
		}
		_, err := rt.Questions.Apply(q.ID, a.AnswerID, testDecision, nil, nil)
		mustErr(t, err, "closed")
		a2 := answerChoice(t, rt, q.ID, "hold")
		ok(rt.Questions.Apply(q.ID, a2.AnswerID, testDecision, nil, nil))
		_, err = rt.Questions.Close(q.ID, a2.AnswerID, "x")
		mustErr(t, err, "already applied")
	})

	run(t, "record_put: conflict on a wrong expected version", func(t *testing.T) {
		rt := openRT(t, t.TempDir())
		req := seed(t, rt)
		cur := readRec(t, rt.Store, req)
		_, err := rt.Store.WriteRecord(req, core.NewOMap(), "x", expected(cur.Rev, "deadbeef"))
		mustErr(t, err, "changed")
		next := ok(rt.Store.WriteRecord(req, core.NewOMap(), "x", expected(cur.Rev, cur.SHA256)))
		if next.Rev != 2 {
			t.Fatalf("rev %d", next.Rev)
		}
	})
}
