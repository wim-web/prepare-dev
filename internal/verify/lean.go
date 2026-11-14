// Package verify は、検証器を実行し、返ってきた判定を読む。
//
// 検証器が成功を返したことと、求める保証を満たしたことは別に扱う。
// ここでは検証器の出力から、主張の文面・頼っている公理・未証明の穴を取り出す。
package verify

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/wim-web/prepare-dev/internal/store"
)

// Claim は、検証器に確かめさせた主張ひとつ。
type Claim struct {
	Name      string   `json:"name"`
	Statement string   `json:"statement,omitempty"` // 検証器が表示した主張の文面
	Axioms    []string `json:"axioms"`              // 頼っている公理（間接のものを含む）
	Missing   bool     `json:"missing,omitempty"`   // 検証器がこの主張を見つけられなかった
}

// Proven は、主張が穴なしで証明されているかを返す。
func (c Claim) Proven() bool {
	if c.Missing {
		return false
	}
	for _, a := range c.Axioms {
		if a == "sorryAx" {
			return false
		}
	}
	return true
}

// Result は、検証器を一回実行した結果。
type Result struct {
	Verdict    string            `json:"verdict"` // accepted | rejected | timeout | error
	Claims     []Claim           `json:"claims,omitempty"`
	Defs       map[string]string `json:"defs,omitempty"` // 主張が使う定義の、検証器による表示
	Errors     []string          `json:"errors,omitempty"`
	Holes      []string          `json:"holes,omitempty"` // 未証明の穴
	Detail     string            `json:"detail,omitempty"`
	DurationMS int64             `json:"duration_ms"`
}

// FindLean は lean の場所を返す。見つからなければ空。
func FindLean() string {
	if p, err := exec.LookPath("lean"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".elan", "bin", "lean")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// LeanVersion は、dir で使われる検証器の版を返す。
func LeanVersion(dir string) string {
	lean := FindLean()
	if lean == "" {
		return ""
	}
	cmd := exec.Command(lean, "--version")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

type leanMessage struct {
	Severity string `json:"severity"`
	Kind     string `json:"kind"`
	Data     string `json:"data"`
	Pos      struct {
		Line int `json:"line"`
	} `json:"pos"`
}

var axiomsRe = regexp.MustCompile(`depends on axioms:\s*\[([^\]]*)\]`)

// CheckLean は、内容を検証器にかけ、主張と定義について検証器自身に答えさせる。
//
// 主張の文面と公理は、内容の末尾に足した問い合わせ（#check / #print axioms / #print）への
// 検証器の返事から取る。文字列を探して判定することはしない。
func CheckLean(dir string, content []byte, claims, defs []string, timeout time.Duration) Result {
	start := time.Now()
	res := Result{Defs: map[string]string{}}
	finish := func() Result {
		res.DurationMS = time.Since(start).Milliseconds()
		return res
	}
	lean := FindLean()
	if lean == "" {
		res.Verdict, res.Detail = "error", "検証器 lean が見つからない"
		return finish()
	}

	origLines := bytes.Count(content, []byte("\n")) + 1
	var buf bytes.Buffer
	buf.Write(content)
	buf.WriteString("\n")
	type probe struct{ kind, name string }
	probes := map[int]probe{}
	line := origLines + 1
	add := func(kind, name, text string) {
		buf.WriteString(text + "\n")
		probes[line] = probe{kind, name}
		line++
	}
	for _, c := range claims {
		add("check", c, "#check @"+c)
		add("axioms", c, "#print axioms "+c)
	}
	for _, d := range defs {
		add("def", d, "#print "+d)
	}

	tmp, err := os.MkdirTemp("", "ppdev-lean-")
	if err != nil {
		res.Verdict, res.Detail = "error", err.Error()
		return finish()
	}
	defer os.RemoveAll(tmp)
	file := filepath.Join(tmp, "Check.lean")
	if err := os.WriteFile(file, buf.Bytes(), 0o644); err != nil {
		res.Verdict, res.Detail = "error", err.Error()
		return finish()
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, lean, "--json", file)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, runErr := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		res.Verdict, res.Detail = "timeout", fmt.Sprintf("%s で打ち切った", timeout)
		return finish()
	}

	byName := map[string]*Claim{}
	for _, c := range claims {
		byName[c] = &Claim{Name: c, Axioms: []string{}}
	}
	parsed := 0
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		var m leanMessage
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		parsed++
		p, isProbe := probes[m.Pos.Line]
		if !isProbe {
			text := fmt.Sprintf("%d 行目: %s", m.Pos.Line, m.Data)
			switch {
			case m.Severity == "error":
				res.Errors = append(res.Errors, text)
			case m.Kind == "hasSorry" || strings.Contains(m.Data, "sorry"):
				res.Holes = append(res.Holes, text)
			}
			continue
		}
		switch p.kind {
		case "check":
			if m.Severity == "error" {
				byName[p.name].Missing = true
			} else {
				byName[p.name].Statement = strings.Join(strings.Fields(m.Data), " ")
			}
		case "axioms":
			if m.Severity == "error" {
				byName[p.name].Missing = true
			} else if g := axiomsRe.FindStringSubmatch(m.Data); g != nil {
				for _, a := range strings.Split(g[1], ",") {
					if a = strings.TrimSpace(a); a != "" {
						byName[p.name].Axioms = append(byName[p.name].Axioms, a)
					}
				}
				sort.Strings(byName[p.name].Axioms)
			}
		case "def":
			if m.Severity != "error" {
				res.Defs[p.name] = m.Data
			} else {
				res.Errors = append(res.Errors, fmt.Sprintf("定義 %s を検証器が見つけられない", p.name))
			}
		}
	}
	for _, c := range claims {
		res.Claims = append(res.Claims, *byName[c])
	}

	var exitErr *exec.ExitError
	switch {
	case runErr != nil && !errors.As(runErr, &exitErr):
		res.Verdict, res.Detail = "error", runErr.Error()
	case runErr != nil && parsed == 0:
		res.Verdict, res.Detail = "error", strings.TrimSpace(stderr.String())
	case len(res.Errors) > 0:
		res.Verdict = "rejected"
	default:
		res.Verdict = "accepted"
		for _, c := range res.Claims {
			if c.Missing {
				res.Verdict = "rejected"
				res.Errors = append(res.Errors, fmt.Sprintf("主張 %s を検証器が見つけられない", c.Name))
			}
		}
	}
	return finish()
}

// StatementHash は、主張の文面と、主張が使う定義の印を作る。
// 証明の書き方を変えても変わらず、主張や定義を変えると変わる。
func StatementHash(r Result) string {
	var b strings.Builder
	claims := append([]Claim(nil), r.Claims...)
	sort.Slice(claims, func(i, j int) bool { return claims[i].Name < claims[j].Name })
	for _, c := range claims {
		fmt.Fprintf(&b, "claim %s\n%s\n", c.Name, c.Statement)
	}
	names := make([]string, 0, len(r.Defs))
	for n := range r.Defs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&b, "def %s\n%s\n", n, r.Defs[n])
	}
	return store.HashBytes([]byte(b.String()))
}

// RunLean は、内容をプログラムとして検証器に実行させ、出力を返す。
func RunLean(dir string, content []byte, args []string, timeout time.Duration) (string, error) {
	lean := FindLean()
	if lean == "" {
		return "", errors.New("検証器 lean が見つからない")
	}
	tmp, err := os.MkdirTemp("", "ppdev-lean-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	file := filepath.Join(tmp, "Run.lean")
	if err := os.WriteFile(file, content, 0o644); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, lean, append([]string{"--run", file}, args...)...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("%s で打ち切った", timeout)
	}
	if err != nil {
		return "", fmt.Errorf("モデルを実行できなかった: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// 証明の検査を弱めたり、検証していないコードを信頼させたりする書き方。
// 見つけたら人が確かめる対象として報告する。ないことの証明にはしない。
var flagged = []struct{ token, meaning string }{
	{"skipKernelTC", "検査を省く設定"},
	{"native_decide", "コンパイルしたコードの実行結果を信頼する証明"},
	{"implemented_by", "証明した定義とは別の実装を実行に使う宣言"},
	{"@[extern", "外部の実装を信頼する宣言"},
	{"unsafe ", "安全でないコード"},
}

var axiomDecl = regexp.MustCompile(`(?m)^\s*(?:private\s+|protected\s+)?axiom\s+(\S+)`)

// Flagged は、内容にある要注意の書き方を返す。
func Flagged(content []byte) []string {
	var out []string
	text := string(content)
	for _, f := range flagged {
		if strings.Contains(text, f.token) {
			out = append(out, fmt.Sprintf("%s（%s）", f.meaning, strings.TrimSpace(f.token)))
		}
	}
	for _, m := range axiomDecl.FindAllStringSubmatch(text, -1) {
		out = append(out, fmt.Sprintf("公理の追加（%s）", m[1]))
	}
	return out
}

// Mutation は、守る性質に違反するように内容を一か所だけ変えた変種。
type Mutation struct {
	Name  string `json:"name"`
	Old   string `json:"old"`
	New   string `json:"new"`
	Claim string `json:"claim"` // この変種で証明できなくなるはずの主張
}

// MutationResult は、変種を検証器にかけた結果。
type MutationResult struct {
	Name     string `json:"name"`
	Claim    string `json:"claim"`
	Detected bool   `json:"detected"`
	Outcome  string `json:"outcome"`
}

// Apply は変種を作る。置き換える箇所がちょうど一つでなければ誤り。
func (m Mutation) Apply(content []byte) ([]byte, error) {
	if n := bytes.Count(content, []byte(m.Old)); n != 1 {
		return nil, fmt.Errorf("変種「%s」の置き換え箇所が %d か所ある（ちょうど一か所である必要がある）", m.Name, n)
	}
	return bytes.Replace(content, []byte(m.Old), []byte(m.New), 1), nil
}

// CheckMutation は、変種で主張が証明できなくなるかを確かめる。
// 時間切れや実行の失敗は、違反を検出したことにしない。
func CheckMutation(dir string, content []byte, m Mutation, claims, defs []string, timeout time.Duration) MutationResult {
	out := MutationResult{Name: m.Name, Claim: m.Claim}
	variant, err := m.Apply(content)
	if err != nil {
		out.Outcome = err.Error()
		return out
	}
	r := CheckLean(dir, variant, claims, defs, timeout)
	switch r.Verdict {
	case "timeout":
		out.Outcome = "時間切れ。違反を検出したとは言えない"
	case "error":
		out.Outcome = "検証器を実行できなかった: " + r.Detail
	case "accepted":
		out.Outcome = "壊したのに受理された"
		for _, c := range r.Claims {
			if c.Name == m.Claim && !c.Proven() {
				out.Detected, out.Outcome = true, "主張が証明できなくなった"
			}
		}
	case "rejected":
		out.Outcome = "検証器は拒否したが、対象の主張は証明できたまま"
		for _, c := range r.Claims {
			if c.Name == m.Claim && !c.Proven() {
				out.Detected, out.Outcome = true, "主張が証明できなくなった"
			}
		}
	}
	return out
}
