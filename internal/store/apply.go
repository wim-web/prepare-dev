package store

import (
	"strings"

	"github.com/wim-web/prepare-dev/internal/rule"
)

// apply は確定単位を状態に当てる。決まりに合わない変更は誤りを返す。
// 誤りを返したあとの状態は使わない。
func (st *State) apply(tx *Tx) error {
	for i := range tx.Ops {
		if err := st.applyOp(&tx.Ops[i], tx.At); err != nil {
			return err
		}
	}
	if tx.ID != "" {
		st.Seen[tx.ID] = tx.Result
	}
	return nil
}

func (st *State) applyOp(op *Op, at string) error {
	switch op.Type {
	case "put":
		return st.applyPut(op, at)
	case "adopt":
		return st.applyAdopt(op)
	case "resolve":
		return st.applyResolve(op)
	case "run.new":
		return st.applyRunNew(op, at)
	case "run.begin":
		r, err := st.run(op.ID)
		if err != nil {
			return err
		}
		if r.State != "approved" {
			return Errf("invalid", "%s は起動を許可された状態ではない（今は %s）", r.ID, r.State)
		}
		r.State = "running"
		r.Observed = op.Observed
		return nil
	case "run.observed":
		r, err := st.run(op.ID)
		if err != nil {
			return err
		}
		r.Observed = op.Observed
		return nil
	case "run.cancel":
		r, err := st.run(op.ID)
		if err != nil {
			return err
		}
		if r.State == "denied" {
			return Errf("invalid", "%s は起動していない", r.ID)
		}
		r.Cancelled = true
		st.Slots[r.Slot()] = rule.Cancel(st.Slots[r.Slot()], r.ID)
		return nil
	case "run.answer":
		return st.applyRunAnswer(op, at)
	case "run.fail":
		r, err := st.run(op.ID)
		if err != nil {
			return err
		}
		if op.Fail == nil || op.Fail.Reason == "" {
			return Errf("invalid", "終わった理由が要る")
		}
		if r.State == "denied" {
			return Errf("invalid", "%s は起動していない", r.ID)
		}
		f := *op.Fail
		f.At = at
		r.Fail = &f
		r.State = "failed"
		return nil
	}
	return Errf("invalid", "知らない操作: %s", op.Type)
}

func (st *State) run(id string) (*Run, error) {
	r, ok := st.Runs[id]
	if !ok {
		return nil, Errf("not-found", "実行 %s がない", id)
	}
	return r, nil
}

func (st *State) object(id string) (*Object, error) {
	o, ok := st.Objects[id]
	if !ok {
		return nil, Errf("not-found", "記録 %s がない", id)
	}
	return o, nil
}

// userDecision は、利用者の決定（またはそこからの導出）として使える記録かを確かめる。
func (st *State) userDecision(id string, allowDerived bool) error {
	o, err := st.object(id)
	if err != nil {
		return err
	}
	if o.Kind != "DEC" {
		return Errf("invalid", "%s は決定ではない", id)
	}
	switch o.Head().Status {
	case "decided":
		return nil
	case "derived":
		if allowDerived {
			return nil
		}
	}
	return Errf("invalid", "%s は利用者の決定として使えない（状態: %s）", id, o.Head().Status)
}

func (st *State) checkLinks(links []Link) error {
	for _, l := range links {
		if l.Rel == "" || l.To == "" {
			return Errf("invalid", "つながりには種類と相手が要る")
		}
		if strings.HasPrefix(l.To, "RUN-") {
			if _, err := st.run(l.To); err != nil {
				return err
			}
			continue
		}
		o, err := st.object(l.To)
		if err != nil {
			return err
		}
		if l.Version != 0 && o.At(l.Version) == nil {
			return Errf("not-found", "%s に版 %d はない", l.To, l.Version)
		}
	}
	return nil
}

func (st *State) applyPut(op *Op, at string) error {
	if op.Origin == nil {
		return Errf("invalid", "どこから来た内容か（来歴）が要る")
	}
	switch op.Origin.Category {
	case CatUser, CatObserved:
		if strings.TrimSpace(op.Origin.Source) == "" {
			return Errf("invalid", "利用者の発言や観測として記録するには、元の発言・操作・実行への参照が要る")
		}
	case CatAI, CatDerived:
	default:
		return Errf("invalid", "来歴の区分は user / ai / observed / derived のどれか")
	}
	if strings.TrimSpace(op.Title) == "" {
		return Errf("invalid", "題が要る")
	}
	if err := st.checkLinks(op.Links); err != nil {
		return err
	}

	var obj *Object
	if op.Base == 0 {
		if _, ok := Kinds[op.Kind]; !ok {
			return Errf("invalid", "知らない種類: %s", op.Kind)
		}
		if want := st.nextApplied(op.Kind); op.ID != want {
			return Errf("conflict", "新しい記録の番号が合わない（%s のはずが %s）", want, op.ID)
		}
		obj = &Object{ID: op.ID, Kind: op.Kind}
	} else {
		o, err := st.object(op.ID)
		if err != nil {
			return err
		}
		if op.Kind != "" && op.Kind != o.Kind {
			return Errf("invalid", "%s の種類は %s", o.ID, o.Kind)
		}
		if head := o.Head().N; head != op.Base {
			return Errf("conflict", "%s は版 %d を元にした変更だが、今の版は %d。ほかの変更と衝突している", o.ID, op.Base, head)
		}
		obj = o
	}

	v := Version{
		N:      len(obj.Versions) + 1,
		Title:  op.Title,
		Body:   op.Body,
		Fields: op.Fields,
		Links:  op.Links,
		Origin: *op.Origin,
		At:     at,
	}
	v.Hash = hashVersion(obj.Kind, &v)
	if err := st.initialStatus(obj.Kind, &v); err != nil {
		return err
	}
	obj.Versions = append(obj.Versions, v)
	if op.Base == 0 {
		st.Objects[obj.ID] = obj
		st.Order = append(st.Order, obj.ID)
		st.counters[obj.Kind]++
	}
	return nil
}

func hasLink(v *Version, rel string) (Link, bool) {
	for _, l := range v.Links {
		if l.Rel == rel {
			return l, true
		}
	}
	return Link{}, false
}

// initialStatus は、作ったばかりの版の状態を決める。
// AI が作った内容は、利用者の決定が結び付くまで採用済みにならない。
func (st *State) initialStatus(kind string, v *Version) error {
	cat := v.Origin.Category
	switch kind {
	case "DEC":
		switch cat {
		case CatUser:
			v.Status = "decided"
		case CatDerived:
			l, ok := hasLink(v, "derived-from")
			if !ok {
				return Errf("invalid", "導出した決定には、元になった利用者の決定（derived-from）が要る")
			}
			if err := st.userDecision(l.To, false); err != nil {
				return err
			}
			if strings.TrimSpace(v.Body) == "" {
				return Errf("invalid", "導出した決定には、どう導いたかの説明が要る")
			}
			v.Status = "derived"
		case CatAI:
			v.Status = "proposed"
		default:
			return Errf("invalid", "決定の来歴は user / derived / ai のどれか")
		}
	case "OBS":
		if cat != CatObserved {
			return Errf("invalid", "観測した事実の来歴は observed")
		}
		v.Status = "observed"
	case "Q":
		v.Status = "open"
	case "FND":
		v.Status = "open"
		if m, ok := v.Fields["mapped"].(bool); ok && !m {
			v.Status = "unmapped"
		}
	case "RPL":
		v.Status = "recorded"
		if m, ok := v.Fields["mapped"].(bool); ok && !m {
			v.Status = "unmapped"
		}
	case "EVD":
		v.Status = "recorded"
	case "FIG":
		v.Status = "generated"
	case "PRT":
		v.Status = "prototype"
	case "EXC":
		l, ok := hasLink(v, "decided-by")
		if !ok {
			return Errf("invalid", "設定の例外には、利用者の決定（decided-by）が要る")
		}
		if err := st.userDecision(l.To, false); err != nil {
			return err
		}
		v.Status = "active"
	default:
		if cat == CatUser {
			v.Status = "adopted"
			v.StatusBy = "origin"
		} else {
			v.Status = "proposed"
		}
	}
	return nil
}

func (st *State) applyAdopt(op *Op) error {
	o, err := st.object(op.ID)
	if err != nil {
		return err
	}
	head := o.Head()
	if op.Version != head.N {
		return Errf("stale", "%s の版 %d についての決定だが、今の版は %d。古い版への決定は今の版に当てない", o.ID, op.Version, head.N)
	}
	if op.As != "adopted" && op.As != "rejected" {
		return Errf("invalid", "採否は adopted か rejected")
	}
	if head.Status != "proposed" {
		return Errf("invalid", "%s の版 %d は提案中ではない（状態: %s）", o.ID, head.N, head.Status)
	}
	if err := st.userDecision(op.Decision, true); err != nil {
		return err
	}
	head.Status = op.As
	head.StatusBy = op.Decision
	return nil
}

func (st *State) applyResolve(op *Op) error {
	o, err := st.object(op.ID)
	if err != nil {
		return err
	}
	res := op.Resolution
	if res == nil || res.To == "" {
		return Errf("invalid", "どう閉じるかが要る")
	}
	head := o.Head()
	if res.Decision != "" {
		if err := st.userDecision(res.Decision, false); err != nil {
			return err
		}
	}
	if res.Refutation != nil {
		if err := st.targetExists(*res.Refutation); err != nil {
			return err
		}
	}
	if res.Fix != nil {
		if err := st.targetExists(*res.Fix); err != nil {
			return err
		}
	}
	switch o.Kind {
	case "Q":
		if head.Status != "open" {
			return Errf("invalid", "%s は開いていない", o.ID)
		}
		if res.To != "resolved" || res.Decision == "" {
			return Errf("invalid", "未決事項を閉じるには、利用者の決定が要る")
		}
	case "RPL":
		if head.Status != "recorded" && head.Status != "unmapped" {
			return Errf("invalid", "%s は既に処理済み", o.ID)
		}
		switch res.To {
		case "applied":
			if head.Status == "unmapped" {
				return Errf("invalid", "どこへの返答か分からないものは、推測で当てない")
			}
		case "dismissed":
			if strings.TrimSpace(res.Reason) == "" {
				return Errf("invalid", "返答を使わない理由が要る")
			}
		default:
			return Errf("invalid", "返答は applied か dismissed で閉じる")
		}
	case "FND":
		if head.Status != "open" && head.Status != "unmapped" {
			return Errf("invalid", "%s は開いていない", o.ID)
		}
		if strings.TrimSpace(res.Reason) == "" {
			return Errf("invalid", "指摘を閉じる理由が要る")
		}
		if err := st.checkFindingResolution(head, res); err != nil {
			return err
		}
	default:
		return Errf("invalid", "%s は閉じる種類の記録ではない", o.ID)
	}
	head.Status = res.To
	head.Resolution = res
	return nil
}

func (st *State) targetExists(t Target) error {
	o, err := st.object(t.ID)
	if err != nil {
		return err
	}
	if o.At(t.Version) == nil {
		return Errf("not-found", "%s に版 %d はない", t.ID, t.Version)
	}
	return nil
}

// checkFindingResolution は、指摘の閉じ方の決まり。
// 受入条件に影響しうる指摘を、設計を書いた側の判断だけで不採用や軽微にはできない。
func (st *State) checkFindingResolution(head *Version, res *Resolution) error {
	switch res.To {
	case "resolved":
		if res.Fix == nil && res.Decision == "" {
			return Errf("invalid", "解決済みにするには、直した版か、利用者の決定が要る")
		}
		return nil
	case "rejected", "minor":
		if res.To == "minor" {
			if a, ok := head.Fields["affects_acceptance"].(bool); ok && !a {
				return nil // 確認役が最初から軽微とした指摘
			}
		}
		if res.Decision != "" || res.Refutation != nil {
			return nil
		}
		if res.RecheckRun != "" {
			r, err := st.run(res.RecheckRun)
			if err != nil {
				return err
			}
			raised, _ := head.Fields["run"].(string)
			if r.ID == raised {
				return Errf("invalid", "再確認は、指摘を出した実行とは別の実行で行う")
			}
			if !r.FreshContext || r.Cancelled || r.State != "answered" {
				return Errf("invalid", "再確認に使えるのは、新しい文脈で最後まで終わった実行だけ")
			}
			return nil
		}
		return Errf("invalid", "不採用や軽微にするには、反証になる保存内容・新しい文脈での再確認・利用者の決定のどれかが要る")
	}
	return Errf("invalid", "指摘は resolved / rejected / minor で閉じる")
}

func (st *State) applyRunNew(op *Op, at string) error {
	r := op.Run
	if r == nil {
		return Errf("invalid", "実行の内容が要る")
	}
	if want := st.nextApplied("RUN"); r.ID != want {
		return Errf("conflict", "新しい実行の番号が合わない（%s のはずが %s）", want, r.ID)
	}
	if strings.TrimSpace(r.Role) == "" {
		return Errf("invalid", "役割が要る")
	}
	switch r.State {
	case "approved", "denied", "running":
	default:
		return Errf("invalid", "実行の最初の状態は approved / denied / running のどれか")
	}
	for _, t := range r.Targets {
		if err := st.targetExists(t); err != nil {
			return err
		}
	}
	for _, id := range []string{r.RetryOf, r.Parent} {
		if id != "" {
			if _, err := st.run(id); err != nil {
				return err
			}
		}
	}
	c := *r
	c.At = at
	c.Answers = nil
	c.Fail = nil
	c.Cancelled = false
	st.Runs[c.ID] = &c
	st.RunOrder = append(st.RunOrder, c.ID)
	st.counters["RUN"]++
	return nil
}

// isFresh は、実行が読んだ版が、対象の今の版と同じかを返す。
func (st *State) isFresh(r *Run) bool {
	for _, t := range r.Targets {
		o, ok := st.Objects[t.ID]
		if !ok || o.Head().N != t.Version {
			return false
		}
	}
	return true
}

func (st *State) applyRunAnswer(op *Op, at string) error {
	r, err := st.run(op.ID)
	if err != nil {
		return err
	}
	if op.Answer == nil || op.Answer.Hash == "" {
		return Errf("invalid", "回答の内容の印が要る")
	}
	if r.State == "denied" {
		return Errf("invalid", "%s は起動していない", r.ID)
	}
	st.answerRun(r, *op.Answer, st.isFresh(r), at)
	return nil
}

// answerRun は、届いた回答を実行の履歴へ残し、採用するかどうかをルールで決める。
// fresh は、回答が対象の現在の内容に対するものかどうか。
func (st *State) answerRun(r *Run, a RunAnswer, fresh bool, at string) {
	next, adopted := rule.Answer(st.Slots[r.Slot()], r.ID, a.Hash, fresh)
	st.Slots[r.Slot()] = next
	r.Answers = append(r.Answers, RunAnswer{
		Hash:    a.Hash,
		Summary: a.Summary,
		Fresh:   fresh,
		Adopted: adopted,
		At:      at,
	})
	if !r.Cancelled {
		r.State = "answered"
	}
}
