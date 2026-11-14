package store

import (
	"errors"
	"os"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func ops(o ...Op) Build {
	return func(*State) ([]Op, func(*State) any, error) { return o, nil, nil }
}

func mustUpdate(t *testing.T, s *Store, o ...Op) {
	t.Helper()
	if _, _, err := s.Update("", ops(o...)); err != nil {
		t.Fatal(err)
	}
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("誤り %q を期待したが、%v だった", code, err)
	}
}

func head(t *testing.T, s *Store, id string) Version {
	t.Helper()
	var v Version
	if err := s.View(func(st *State) error { v = *st.Objects[id].Head(); return nil }); err != nil {
		t.Fatal(err)
	}
	return v
}

var (
	user = &Origin{Category: CatUser, Source: "発言「取り消したあとの回答で上書きしないでほしい」"}
	ai   = &Origin{Category: CatAI}
)

// AI が補った期待結果は、利用者の決定が結び付くまで採用済みにならない。
func TestAIProposalNeedsUserDecision(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, Op{Type: "put", ID: "EX-0001", Kind: "EX", Title: "取消後の回答", Origin: ai})
	if got := head(t, s, "EX-0001").Status; got != "proposed" {
		t.Fatalf("AI の提案が %q になった", got)
	}

	// 決定なしでは採用できない。
	_, _, err := s.Update("", ops(Op{Type: "adopt", ID: "EX-0001", Version: 1, As: "adopted", Decision: "DEC-0001"}))
	wantCode(t, err, "not-found")

	// AI が作った決定は、利用者の決定として使えない。
	mustUpdate(t, s, Op{Type: "put", ID: "DEC-0001", Kind: "DEC", Title: "採用する", Origin: ai})
	_, _, err = s.Update("", ops(Op{Type: "adopt", ID: "EX-0001", Version: 1, As: "adopted", Decision: "DEC-0001"}))
	wantCode(t, err, "invalid")

	// 利用者の決定があれば採用できる。決定の保存と採用は一つの確定単位。
	mustUpdate(t, s,
		Op{Type: "put", ID: "DEC-0002", Kind: "DEC", Title: "採用する", Origin: user},
		Op{Type: "adopt", ID: "EX-0001", Version: 1, As: "adopted", Decision: "DEC-0002"},
	)
	v := head(t, s, "EX-0001")
	if v.Status != "adopted" || v.StatusBy != "DEC-0002" {
		t.Fatalf("採用されていない: %+v", v)
	}
}

// 利用者の発言として記録するには、元の発言への参照が要る。
func TestUserOriginNeedsSource(t *testing.T) {
	s := newStore(t)
	_, _, err := s.Update("", ops(Op{Type: "put", ID: "DEC-0001", Kind: "DEC", Title: "決定", Origin: &Origin{Category: CatUser}}))
	wantCode(t, err, "invalid")
}

// 途中で失敗した確定単位は、何も残さない。
func TestFailedUpdateLeavesNothing(t *testing.T) {
	s := newStore(t)
	_, _, err := s.Update("", ops(
		Op{Type: "put", ID: "DEC-0001", Kind: "DEC", Title: "決定", Origin: user},
		Op{Type: "adopt", ID: "EX-0009", Version: 1, As: "adopted", Decision: "DEC-0001"},
	))
	wantCode(t, err, "not-found")
	s.View(func(st *State) error {
		if len(st.Objects) != 0 {
			t.Fatalf("失敗したのに記録が残った: %d 件", len(st.Objects))
		}
		return nil
	})
}

// 同じ旧版から二つの変更が届いたら、二つ目は衝突として拒否する。
func TestParallelRevisionConflicts(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, Op{Type: "put", ID: "REQ-0001", Kind: "REQ", Title: "要求", Origin: user})
	mustUpdate(t, s, Op{Type: "put", ID: "REQ-0001", Base: 1, Title: "要求（A の変更）", Origin: ai})
	_, _, err := s.Update("", ops(Op{Type: "put", ID: "REQ-0001", Base: 1, Title: "要求（B の変更）", Origin: ai}))
	wantCode(t, err, "conflict")
	if got := head(t, s, "REQ-0001").Title; got != "要求（A の変更）" {
		t.Fatalf("先の変更が失われた: %q", got)
	}
}

// 古い版への決定は、今の版に当てない。
func TestDecisionOnOldVersionIsNotApplied(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, Op{Type: "put", ID: "DES-0001", Kind: "DES", Title: "設計", Origin: ai})
	mustUpdate(t, s, Op{Type: "put", ID: "DES-0001", Base: 1, Title: "設計（改）", Origin: ai})
	_, _, err := s.Update("", ops(
		Op{Type: "put", ID: "DEC-0001", Kind: "DEC", Title: "採用", Origin: user},
		Op{Type: "adopt", ID: "DES-0001", Version: 1, As: "adopted", Decision: "DEC-0001"},
	))
	wantCode(t, err, "stale")
	if got := head(t, s, "DES-0001").Status; got != "proposed" {
		t.Fatalf("今の版が %q になった", got)
	}
}

// 同じ要求を二度送っても、二重には当たらない。
func TestSameRequestIsAppliedOnce(t *testing.T) {
	s := newStore(t)
	build := func(st *State) ([]Op, func(*State) any, error) {
		id := st.Reserve("Q")
		return []Op{{Type: "put", ID: id, Kind: "Q", Title: "未決", Origin: ai}},
			func(*State) any { return map[string]string{"id": id} }, nil
	}
	first, dup, err := s.Update("reply-1", build)
	if err != nil || dup {
		t.Fatalf("一度目: dup=%v err=%v", dup, err)
	}
	second, dup, err := s.Update("reply-1", build)
	if err != nil || !dup {
		t.Fatalf("二度目: dup=%v err=%v", dup, err)
	}
	if string(first) != string(second) {
		t.Fatalf("結果が違う: %s / %s", first, second)
	}
	s.View(func(st *State) error {
		if n := len(st.OfKind("Q")); n != 1 {
			t.Fatalf("記録が %d 件できた", n)
		}
		return nil
	})
	if _, ok, _ := s.Applied("reply-1"); !ok {
		t.Fatal("確定した要求が照合できない")
	}
	if _, ok, _ := s.Applied("reply-2"); ok {
		t.Fatal("送っていない要求が確定済みに見える")
	}
}

// 書き込みが途中で切れた行は確定していない。次の変更はその手前から続ける。
func TestTornTailIsNotApplied(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, Op{Type: "put", ID: "REQ-0001", Kind: "REQ", Title: "要求", Origin: user})
	f, err := os.OpenFile(s.logPath(), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"id":"torn","at":"t","ops":[{"type":"put","id":"REQ-0002","kind":"REQ","ti`)
	f.Close()

	if _, ok, err := s.Applied("torn"); err != nil || ok {
		t.Fatalf("切れた行が確定済みに見える: ok=%v err=%v", ok, err)
	}
	mustUpdate(t, s, Op{Type: "put", ID: "REQ-0002", Kind: "REQ", Title: "次の要求", Origin: user})
	s.View(func(st *State) error {
		if n := len(st.OfKind("REQ")); n != 2 {
			t.Fatalf("要求が %d 件", n)
		}
		return nil
	})
}

func startReview(t *testing.T, s *Store, target Target) string {
	t.Helper()
	var id string
	_, _, err := s.Update("", func(st *State) ([]Op, func(*State) any, error) {
		id = st.Reserve("RUN")
		return []Op{{Type: "run.new", Run: &Run{ID: id, Role: "spec-review", State: "running", FreshContext: true, Targets: []Target{target}}}}, nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// 仕様の例：1 回目を取り消し、2 回目が指摘を返し、そのあと 1 回目の「指摘なし」が届く。
func TestLateAnswerOfCancelledRunDoesNotOverwrite(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, Op{Type: "put", ID: "DES-0001", Kind: "DES", Title: "設計書", Origin: ai})
	target := Target{"DES-0001", 1}

	first := startReview(t, s, target)
	mustUpdate(t, s, Op{Type: "run.cancel", ID: first})
	second := startReview(t, s, target)
	mustUpdate(t, s, Op{Type: "run.answer", ID: second, Answer: &RunAnswer{Hash: "指摘あり"}})
	mustUpdate(t, s, Op{Type: "run.answer", ID: first, Answer: &RunAnswer{Hash: "指摘なし"}})

	s.View(func(st *State) error {
		slot := st.Slots[st.Runs[second].Slot()]
		if slot.Current == nil || slot.Current.Run != second || slot.Current.Answer != "指摘あり" {
			t.Fatalf("現在の結果が 2 回目の指摘でなくなった: %+v", slot.Current)
		}
		late := st.Runs[first].Answers
		if len(late) != 1 || late[0].Adopted || late[0].Hash != "指摘なし" {
			t.Fatalf("1 回目の回答が履歴に正しく残っていない: %+v", late)
		}
		if !st.Runs[first].Cancelled {
			t.Fatal("1 回目の取消が消えた")
		}
		return nil
	})
}

// 回答が現在の内容に対するものかは、読んだ版と今の版が同じかで決める。
func TestFreshIsVersionMatch(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, Op{Type: "put", ID: "DES-0001", Kind: "DES", Title: "設計書", Origin: ai})
	run := startReview(t, s, Target{"DES-0001", 1})
	mustUpdate(t, s, Op{Type: "put", ID: "DES-0001", Base: 1, Title: "設計書（改）", Origin: ai})
	mustUpdate(t, s, Op{Type: "run.answer", ID: run, Answer: &RunAnswer{Hash: "指摘なし"}})

	s.View(func(st *State) error {
		a := st.Runs[run].Answers[0]
		if a.Fresh || a.Adopted {
			t.Fatalf("古い版への回答が採用された: %+v", a)
		}
		if st.Slots[st.Runs[run].Slot()].Current != nil {
			t.Fatal("古い版への回答が現在の結果になった")
		}
		return nil
	})
}

// 受入条件に影響しうる指摘は、書いた側の判断だけでは不採用にできない。
func TestFindingCannotBeDismissedByAuthorAlone(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, Op{Type: "put", ID: "DES-0001", Kind: "DES", Title: "設計書", Origin: ai})
	raised := startReview(t, s, Target{"DES-0001", 1})
	mustUpdate(t, s, Op{Type: "put", ID: "FND-0001", Kind: "FND", Title: "保存失敗時の処理がない", Origin: ai,
		Fields: map[string]any{"run": raised, "affects_acceptance": true}})

	reject := func(r Resolution) error {
		r.To, r.Reason = "rejected", "別の節で扱っている"
		_, _, err := s.Update("", ops(Op{Type: "resolve", ID: "FND-0001", Resolution: &r}))
		return err
	}
	wantCode(t, reject(Resolution{}), "invalid")
	wantCode(t, reject(Resolution{RecheckRun: raised}), "invalid") // 指摘を出した実行では再確認にならない

	// 新しい文脈の別の実行が最後まで終わっていれば、再確認として使える。
	recheck := startReview(t, s, Target{"DES-0001", 1})
	wantCode(t, reject(Resolution{RecheckRun: recheck}), "invalid") // まだ終わっていない
	mustUpdate(t, s, Op{Type: "run.answer", ID: recheck, Answer: &RunAnswer{Hash: "別の節にある"}})
	if err := reject(Resolution{RecheckRun: recheck}); err != nil {
		t.Fatal(err)
	}
	if got := head(t, s, "FND-0001").Status; got != "rejected" {
		t.Fatalf("状態が %q", got)
	}
}

// 未決事項は、利用者の決定でしか閉じられない。
func TestQuestionNeedsUserDecision(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, Op{Type: "put", ID: "Q-0001", Kind: "Q", Title: "証明の範囲", Origin: ai})
	_, _, err := s.Update("", ops(Op{Type: "resolve", ID: "Q-0001", Resolution: &Resolution{To: "resolved"}}))
	wantCode(t, err, "invalid")
	mustUpdate(t, s,
		Op{Type: "put", ID: "DEC-0001", Kind: "DEC", Title: "ルールまで証明する", Origin: user},
		Op{Type: "resolve", ID: "Q-0001", Resolution: &Resolution{To: "resolved", Decision: "DEC-0001"}},
	)
}
