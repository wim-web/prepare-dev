package store

import (
	"fmt"

	"github.com/wim-web/prepare-dev/internal/core"
)

// ValidateJournal finds journal entries that cannot both be true. A single writer never
// produces them; they appear when the journals of two working copies are merged with
// both sides' lines kept, since both copies numbered their records, questions and
// decisions from the same base. It returns one message per conflict. Runs are not
// checked: they are evidence, not part of the decision state, and the journal keeps a
// historical duplicate (RUN-0006, two jobs that shared one id).
func ValidateJournal(events []core.Event) []string {
	var out []string
	created := map[string]bool{}
	questions := map[string]bool{}
	answers := map[string]bool{}
	decisions := map[string]string{}    // decision id -> txid
	intentAnswer := map[string]string{} // txid -> answer id
	committed := map[string]string{}    // answer id -> txid
	for i, e := range events {
		line := i + 1
		switch e.Type {
		case "record.write":
			if e.Rev == 1 {
				if created[e.ID] {
					out = append(out, fmt.Sprintf("line %d: record %s is created twice", line, e.ID))
				}
				created[e.ID] = true
			}
		case "question.create":
			if questions[e.QuestionID] {
				out = append(out, fmt.Sprintf("line %d: question %s is created twice", line, e.QuestionID))
			}
			questions[e.QuestionID] = true
		case "answer.save":
			if answers[e.AnswerID] {
				out = append(out, fmt.Sprintf("line %d: answer %s is saved twice", line, e.AnswerID))
			}
			answers[e.AnswerID] = true
		case "apply.intent":
			if tx, ok := decisions[e.DecisionID]; ok && tx != e.TxID {
				out = append(out, fmt.Sprintf("line %d: decision %s is allocated by two transactions", line, e.DecisionID))
			}
			decisions[e.DecisionID] = e.TxID
			if _, ok := intentAnswer[e.TxID]; ok {
				out = append(out, fmt.Sprintf("line %d: transaction %s starts twice", line, e.TxID))
			}
			intentAnswer[e.TxID] = e.AnswerID
		case "apply.commit":
			a, ok := intentAnswer[e.TxID]
			if !ok {
				out = append(out, fmt.Sprintf("line %d: commit of transaction %s has no intent before it", line, e.TxID))
				break
			}
			if tx, ok := committed[a]; ok && tx != e.TxID {
				out = append(out, fmt.Sprintf("line %d: answer %s is committed by two transactions", line, a))
			}
			committed[a] = e.TxID
		}
	}
	return out
}
