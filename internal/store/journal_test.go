package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wim-web/prepare-dev/internal/core"
)

func readJournal(t *testing.T, path string) []core.Event {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []core.Event
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e core.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

// A journal merged from two working copies (both sides' lines kept) is detected, and
// the store then refuses to write; a journal written by one writer passes.
func TestJournalConflicts(t *testing.T) {
	t.Run("[C3] a journal written by one writer has no conflicts", func(t *testing.T) {
		// testdata/ts-342bc64-events.jsonl is the journal the TypeScript version wrote (commit 342bc64)
		if issues := ValidateJournal(readJournal(t, filepath.Join("testdata", "ts-342bc64-events.jsonl"))); len(issues) > 0 {
			t.Fatal(issues)
		}
	})
	t.Run("[C3] entries two working copies numbered from the same base are reported", func(t *testing.T) {
		evs := []core.Event{
			{Type: "record.write", ID: "REQ-0001", Rev: 1},
			{Type: "question.create", QuestionID: "Q-0001"},
			{Type: "answer.save", QuestionID: "Q-0001", AnswerID: "ANS-0001-1"},
			{Type: "apply.intent", TxID: "a", AnswerID: "ANS-0001-1", DecisionID: "DEC-0001"},
			{Type: "apply.commit", TxID: "a"},
			// the other branch
			{Type: "record.write", ID: "REQ-0001", Rev: 1},
			{Type: "question.create", QuestionID: "Q-0001"},
			{Type: "answer.save", QuestionID: "Q-0001", AnswerID: "ANS-0001-1"},
			{Type: "apply.intent", TxID: "b", AnswerID: "ANS-0001-1", DecisionID: "DEC-0001"},
			{Type: "apply.commit", TxID: "b"},
		}
		issues := strings.Join(ValidateJournal(evs), "\n")
		for _, want := range []string{"record REQ-0001 is created twice", "question Q-0001 is created twice", "answer ANS-0001-1 is saved twice", "decision DEC-0001 is allocated by two transactions", "answer ANS-0001-1 is committed by two transactions"} {
			if !strings.Contains(issues, want) {
				t.Fatalf("missing %q in:\n%s", want, issues)
			}
		}
	})
	t.Run("[C3] with a conflicting journal the store opens, reports the problem and writes nothing", func(t *testing.T) {
		root := t.TempDir()
		s, err := Open(root)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateRecord("requirement", core.OM("title", "A"), "a\n"); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(s.EventsFile)
		if err := os.WriteFile(s.EventsFile, append(data, data...), 0o644); err != nil { // both sides kept
			t.Fatal(err)
		}
		before, _ := os.ReadFile(s.EventsFile)
		s2, err := Open(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(s2.Blocked) == 0 || len(s2.Problems) == 0 || !strings.Contains(s2.Problems[0].Message, "created twice") {
			t.Fatalf("blocked %v problems %v", s2.Blocked, s2.Problems)
		}
		_, err = s2.CreateRecord("requirement", core.OM("title", "B"), "b\n")
		var re *core.RuntimeError
		if !errors.As(err, &re) || re.Code != "conflict" {
			t.Fatalf("write with a conflicting journal: %v", err)
		}
		if after, _ := os.ReadFile(s2.EventsFile); string(after) != string(before) {
			t.Fatal("the journal changed")
		}
	})
	t.Run("[C3] merge conflict markers in the journal stop the daemon with a hint", func(t *testing.T) {
		root := t.TempDir()
		if _, err := Open(root); err != nil {
			t.Fatal(err)
		}
		ev := filepath.Join(root, "prepare", "events.jsonl")
		if err := os.WriteFile(ev, []byte("<<<<<<< HEAD\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(root); err == nil || !strings.Contains(err.Error(), "merge conflict marker") {
			t.Fatalf("open: %v", err)
		}
	})
}
