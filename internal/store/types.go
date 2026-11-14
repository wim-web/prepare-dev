// Package store は、決めたこと・読んだ内容・確認結果の正式な記録を持つ。
//
// 記録は追記だけのログ（.ppdev/log.jsonl）で、一行が一つの確定単位になる。
// 状態はログを先頭から読み直して作る。更新はこのパッケージだけを通す。
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/wim-web/prepare-dev/internal/rule"
)

// 来歴の区分。
const (
	CatUser     = "user"     // 利用者の発言・操作
	CatAI       = "ai"       // AI の提案
	CatObserved = "observed" // 観測した事実
	CatDerived  = "derived"  // 既にある利用者の決定からの導出
)

// Kinds は記録の種類と、その日本語の名前。
var Kinds = map[string]string{
	"REQ": "要求",
	"EX":  "具体例",
	"ACC": "受入条件",
	"DEC": "決定",
	"Q":   "未決事項",
	"DES": "設計",
	"PRT": "試作",
	"GRT": "保証要求",
	"FRM": "形式条件",
	"OBS": "観測した事実",
	"FND": "指摘",
	"EVD": "証拠",
	"FIG": "図",
	"RPL": "返答",
	"EXC": "設定の例外",
}

// Error は、利用者や AI に理由を返すための誤り。
type Error struct {
	Code string `json:"code"` // invalid | not-found | conflict | stale | denied
	Msg  string `json:"message"`
}

func (e *Error) Error() string { return e.Msg }

func Errf(code, format string, a ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// Origin は、その内容がどこから来たか。
type Origin struct {
	Category string `json:"category"`
	By       string `json:"by,omitempty"`
	Source   string `json:"source,omitempty"` // 根拠となる発言・操作・実行
}

// Link は、ほかの記録や実行とのつながり。
type Link struct {
	Rel     string `json:"rel"`
	To      string `json:"to"`
	Version int    `json:"version,omitempty"`
}

// Target は、ある記録の、ある版。
type Target struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

func (t Target) String() string { return fmt.Sprintf("%s@%d", t.ID, t.Version) }

// Resolution は、指摘・未決事項・返答をどう閉じたか。
type Resolution struct {
	To         string  `json:"to"`
	Reason     string  `json:"reason,omitempty"`
	Decision   string  `json:"decision,omitempty"`    // 利用者の決定
	RecheckRun string  `json:"recheck_run,omitempty"` // 新しい文脈での再確認
	Refutation *Target `json:"refutation,omitempty"`  // 反証になる保存内容
	Fix        *Target `json:"fix,omitempty"`         // 直した版
}

// Version は、記録のある時点の内容。
type Version struct {
	N          int            `json:"n"`
	Hash       string         `json:"hash"`
	Title      string         `json:"title"`
	Body       string         `json:"body,omitempty"`
	Fields     map[string]any `json:"fields,omitempty"`
	Links      []Link         `json:"links,omitempty"`
	Origin     Origin         `json:"origin"`
	Status     string         `json:"status"`
	StatusBy   string         `json:"status_by,omitempty"` // 採否の根拠になった決定
	Resolution *Resolution    `json:"resolution,omitempty"`
	At         string         `json:"at"`
}

// Object は、同じものを指し続ける記録。版を重ねていく。
type Object struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Versions []Version `json:"versions"`
}

// Head は最新の版を返す。
func (o *Object) Head() *Version { return &o.Versions[len(o.Versions)-1] }

// At は指定した版を返す。なければ nil。
func (o *Object) At(n int) *Version {
	if n < 1 || n > len(o.Versions) {
		return nil
	}
	return &o.Versions[n-1]
}

// Setting は、担当に使うモデルと思考量。
type Setting struct {
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
}

// FileRef は、読んだファイルと、そのときの内容の印。
type FileRef struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

// RunAnswer は、実行から届いた回答ひとつ。
type RunAnswer struct {
	Hash    string `json:"hash"`
	Summary string `json:"summary,omitempty"`
	Fresh   bool   `json:"fresh"`   // 対象の現在の内容に対する回答か
	Adopted bool   `json:"adopted"` // 現在の結果に採用したか
	At      string `json:"at"`
}

// Failure は、実行が回答なしで終わった理由。
type Failure struct {
	Reason string `json:"reason"` // timeout | error | unproven | limit | stopped
	Detail string `json:"detail,omitempty"`
	At     string `json:"at,omitempty"`
}

// Run は、担当や検証器の一回の実行。やり直しは別の実行になる。
type Run struct {
	ID           string      `json:"id"`
	Role         string      `json:"role"`
	Targets      []Target    `json:"targets,omitempty"`
	Snapshot     []FileRef   `json:"snapshot,omitempty"`
	Viewpoints   []string    `json:"viewpoints,omitempty"`
	FreshContext bool        `json:"fresh_context,omitempty"`
	RetryOf      string      `json:"retry_of,omitempty"`
	Parent       string      `json:"parent,omitempty"`
	State        string      `json:"state"` // approved | denied | running | answered | failed
	Cancelled    bool        `json:"cancelled,omitempty"`
	Requested    Setting     `json:"requested"`
	Resolved     Setting     `json:"resolved"`
	Observed     *Setting    `json:"observed,omitempty"`
	Enforce      string      `json:"enforce,omitempty"`
	Exception    string      `json:"exception,omitempty"`
	Event        string      `json:"event,omitempty"`
	Note         string      `json:"note,omitempty"` // 思考量を通常の値から変えたときの、変え方と理由
	Reasons      []string    `json:"reasons,omitempty"`
	Answers      []RunAnswer `json:"answers,omitempty"`
	Fail         *Failure    `json:"fail,omitempty"`
	At           string      `json:"at,omitempty"`
}

// Slot は、この実行の結果が入る場所。同じ役割・同じ対象の実行は同じ場所を使う。
func (r *Run) Slot() string { return SlotKey(r.Role, r.Targets) }

// SlotKey は、役割と対象から、結果の入る場所の名前を作る。
func SlotKey(role string, targets []Target) string {
	ids := make([]string, 0, len(targets))
	for _, t := range targets {
		ids = append(ids, t.ID)
	}
	sort.Strings(ids)
	return role + ":" + strings.Join(ids, ",")
}

// Op は、記録への変更ひとつ。
type Op struct {
	Type string `json:"type"`

	// put：記録を新しく作る、または版を重ねる
	ID     string         `json:"id,omitempty"`
	Kind   string         `json:"kind,omitempty"`
	Base   int            `json:"base,omitempty"` // 元にした版。新しく作るときは 0
	Title  string         `json:"title,omitempty"`
	Body   string         `json:"body,omitempty"`
	Fields map[string]any `json:"fields,omitempty"`
	Links  []Link         `json:"links,omitempty"`
	Origin *Origin        `json:"origin,omitempty"`

	// adopt：提案を採用または不採用にする
	Version  int    `json:"version,omitempty"`
	Decision string `json:"decision,omitempty"`
	As       string `json:"as,omitempty"`

	// resolve：指摘・未決事項・返答を閉じる
	Resolution *Resolution `json:"resolution,omitempty"`

	// run.*：実行
	Run      *Run       `json:"run,omitempty"`
	Answer   *RunAnswer `json:"answer,omitempty"`
	Fail     *Failure   `json:"fail,omitempty"`
	Observed *Setting   `json:"observed,omitempty"`
}

// Tx は一つの確定単位。ログの一行になる。
type Tx struct {
	ID     string          `json:"id"`
	At     string          `json:"at"`
	Ops    []Op            `json:"ops"`
	Result json.RawMessage `json:"result,omitempty"`
}

// State は、ログを読み直して作った現在の状態。
type State struct {
	Objects map[string]*Object
	Runs    map[string]*Run
	Slots   map[string]rule.State
	Seen    map[string]json.RawMessage

	Order    []string // 記録を作った順
	RunOrder []string // 実行を作った順

	counters map[string]int
	reserved map[string]int
}

func NewState() *State {
	return &State{
		Objects:  map[string]*Object{},
		Runs:     map[string]*Run{},
		Slots:    map[string]rule.State{},
		Seen:     map[string]json.RawMessage{},
		counters: map[string]int{},
		reserved: map[string]int{},
	}
}

// Reserve は、これから作る記録や実行の番号を取る。
// 取った番号は、同じ確定単位の中で順に使う。
func (st *State) Reserve(kind string) string {
	st.reserved[kind]++
	return fmt.Sprintf("%s-%04d", kind, st.counters[kind]+st.reserved[kind])
}

func (st *State) nextApplied(kind string) string {
	return fmt.Sprintf("%s-%04d", kind, st.counters[kind]+1)
}

// OfKind は、ある種類の記録を作った順に返す。
func (st *State) OfKind(kind string) []*Object {
	var out []*Object
	for _, id := range st.Order {
		if o := st.Objects[id]; o.Kind == kind {
			out = append(out, o)
		}
	}
	return out
}

// HashBytes は内容の印を作る。
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hashVersion(kind string, v *Version) string {
	b, _ := json.Marshal(struct {
		Kind   string         `json:"kind"`
		Title  string         `json:"title"`
		Body   string         `json:"body"`
		Fields map[string]any `json:"fields"`
		Links  []Link         `json:"links"`
	}{kind, v.Title, v.Body, v.Fields, v.Links})
	return HashBytes(b)
}
