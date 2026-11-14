// Package rule は、取り消した実行から遅れて届いた回答の扱いを決める。
//
// model/CancelRule.lean の step と同じ規則を書いたもので、
// 両者が同じ結果を出すことを適合テスト（rule_test.go）で突き合わせる。
package rule

import "slices"

// Entry は、ある実行から届いた回答ひとつ。
type Entry struct {
	Run    string `json:"run"`
	Answer string `json:"answer"`
}

// State は、同じ対象に対する実行の集まりについての状態。
type State struct {
	Cancelled []string `json:"cancelled,omitempty"` // 取り消した実行
	History   []Entry  `json:"history,omitempty"`   // 届いた回答。届いた順
	Current   *Entry   `json:"current,omitempty"`   // 現在の結果
}

// IsCancelled は、実行が取り消されているかを返す。
func (s State) IsCancelled(run string) bool {
	return slices.Contains(s.Cancelled, run)
}

func (s State) clone() State {
	out := State{
		Cancelled: append([]string(nil), s.Cancelled...),
		History:   append([]Entry(nil), s.History...),
	}
	if s.Current != nil {
		c := *s.Current
		out.Current = &c
	}
	return out
}

// Cancel は取消を記録する。一度記録した取消は消えない。
func Cancel(s State, run string) State {
	out := s.clone()
	if !out.IsCancelled(run) {
		out.Cancelled = append(out.Cancelled, run)
	}
	return out
}

// Answer は届いた回答をひとつ処理する。
//
// 回答は必ず履歴へ残す。現在の結果に採用するのは、取り消していない実行の、
// 対象の現在の内容に対する回答（fresh）だけ。採用したかどうかを二つ目の値で返す。
func Answer(s State, run, answer string, fresh bool) (State, bool) {
	out := s.clone()
	e := Entry{Run: run, Answer: answer}
	out.History = append(out.History, e)
	if out.IsCancelled(run) || !fresh {
		return out, false
	}
	out.Current = &e
	return out, true
}
