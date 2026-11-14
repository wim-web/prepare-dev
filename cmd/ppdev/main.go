// ppdev は、skill から呼ぶ補助コマンド。
// 記録の更新、担当の起動の照合、検証の実行と受理確認、図と指摘の対応づけを行う。
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/wim-web/prepare-dev/internal/store"
)

type command func(root string, args []string) (any, error)

// exitError は、結果を出力したうえで、指定した終了コードで終わるための誤り。
type exitError struct {
	code   int
	result any
}

func (e *exitError) Error() string { return "exit" }

var commands = map[string]command{
	"init":            cmdInit,
	"new":             cmdNew,
	"revise":          cmdRevise,
	"decide":          cmdDecide,
	"derive":          cmdDerive,
	"resolve":         cmdResolve,
	"show":            cmdShow,
	"list":            cmdList,
	"tx":              cmdTx,
	"run start":       cmdRunStart,
	"run cancel":      cmdRunCancel,
	"run answer":      cmdRunAnswer,
	"run fail":        cmdRunFail,
	"run show":        cmdRunShow,
	"spawn check":     cmdSpawnCheck,
	"spawn gate":      cmdSpawnGate,
	"spawn observed":  cmdSpawnObserved,
	"policy show":     cmdPolicyShow,
	"policy except":   cmdPolicyExcept,
	"policy template": cmdPolicyTemplate,
	"review bundle":   cmdReviewBundle,
	"review finding":  cmdReviewFinding,
	"review status":   cmdReviewStatus,
	"frm register":    cmdFrmRegister,
	"verify lean":     cmdVerifyLean,
	"verify test":     cmdVerifyTest,
	"guarantee check": cmdGuaranteeCheck,
	"figure generate": cmdFigureGenerate,
	"figure check":    cmdFigureCheck,
	"reply record":    cmdReplyRecord,
	"impact":          cmdImpact,
	"status":          cmdStatus,
}

func usage() string {
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	return "使い方: ppdev [--root DIR] <命令> [引数]\n命令:\n  " + strings.Join(names, "\n  ") +
		"\n各命令の引数は ppdev <命令> -h で見られる。"
}

func main() {
	args := os.Args[1:]
	root := os.Getenv("PPDEV_ROOT")
	if len(args) >= 2 && args[0] == "--root" {
		root, args = args[1], args[2:]
	}
	if root == "" {
		root, _ = os.Getwd()
	}
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Println(usage())
		return
	}
	name, rest := args[0], args[1:]
	if _, ok := commands[name]; !ok && len(rest) > 0 {
		name, rest = name+" "+rest[0], rest[1:]
	}
	cmd, ok := commands[name]
	if !ok {
		fail(store.Errf("invalid", "知らない命令: %s\n%s", strings.Join(args, " "), usage()))
	}
	result, err := cmd(root, rest)
	if err != nil {
		var ee *exitError
		if errors.As(err, &ee) {
			emit(ee.result)
			os.Exit(ee.code)
		}
		fail(err)
	}
	emit(result)
}

func emit(v any) {
	if v == nil {
		return
	}
	if s, ok := v.(string); ok {
		fmt.Println(s)
		return
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}

func fail(err error) {
	code, exit := "error", 1
	var se *store.Error
	if errors.As(err, &se) {
		code = se.Code
		switch se.Code {
		case "denied":
			exit = 2
		case "conflict", "stale":
			exit = 3
		}
	}
	if errors.Is(err, flag.ErrHelp) {
		os.Exit(0)
	}
	b, _ := json.MarshalIndent(map[string]any{"error": map[string]string{"code": code, "message": err.Error()}}, "", "  ")
	fmt.Fprintln(os.Stderr, string(b))
	os.Exit(exit)
}

// multi は、繰り返し指定できる引数。
type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

func flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("ppdev "+name, flag.ContinueOnError)
	return fs
}

// parse は引数を読む。位置引数（番号など）は、名前付きの引数より前に置く。
func parse(fs *flag.FlagSet, args []string, positional int) ([]string, error) {
	var pos []string
	for len(args) > 0 && len(pos) < positional && !strings.HasPrefix(args[0], "-") {
		pos, args = append(pos, args[0]), args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if len(pos) < positional {
		return nil, store.Errf("invalid", "引数が足りない。%s -h で確認する", fs.Name())
	}
	if fs.NArg() > 0 {
		return nil, store.Errf("invalid", "余分な引数: %s", strings.Join(fs.Args(), " "))
	}
	return pos, nil
}

// parseTarget は「REQ-0001@2」を記録と版に分ける。版を省くと最新の版になる。
func parseTarget(st *store.State, s string) (store.Target, error) {
	id, ver, has := strings.Cut(s, "@")
	o, ok := st.Objects[id]
	if !ok {
		return store.Target{}, store.Errf("not-found", "記録 %s がない", id)
	}
	if !has {
		return store.Target{ID: id, Version: o.Head().N}, nil
	}
	n, err := strconv.Atoi(strings.TrimPrefix(ver, "v"))
	if err != nil || o.At(n) == nil {
		return store.Target{}, store.Errf("not-found", "%s に版 %s はない", id, ver)
	}
	return store.Target{ID: id, Version: n}, nil
}

func parseTargets(st *store.State, in []string) ([]store.Target, error) {
	var out []store.Target
	for _, s := range in {
		t, err := parseTarget(st, s)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// content は、内容を持つ記録に共通の引数。
type content struct {
	title, body, bodyFile  string
	origin, source, by     string
	fields, links          multi
	fieldsJSON, fieldsFile string
}

func (c *content) register(fs *flag.FlagSet) {
	fs.StringVar(&c.title, "title", "", "題")
	fs.StringVar(&c.body, "body", "", "本文")
	fs.StringVar(&c.bodyFile, "body-file", "", "本文を読むファイル")
	fs.StringVar(&c.origin, "origin", "ai", "来歴: user | ai | observed | derived")
	fs.StringVar(&c.source, "source", "", "根拠となる発言・操作・実行（user と observed では必須）")
	fs.StringVar(&c.by, "by", "", "誰の発言・提案か")
	fs.Var(&c.fields, "field", "項目 key=value（繰り返せる）")
	fs.StringVar(&c.fieldsJSON, "fields-json", "", "項目を JSON で渡す")
	fs.StringVar(&c.fieldsFile, "fields-file", "", "項目の JSON を読むファイル")
	fs.Var(&c.links, "link", "つながり 種類:番号[@版]（繰り返せる）")
}

func (c *content) readBody() (string, error) {
	if c.bodyFile == "" {
		return c.body, nil
	}
	b, err := os.ReadFile(c.bodyFile)
	if err != nil {
		return "", store.Errf("not-found", "本文のファイルが読めない: %s", c.bodyFile)
	}
	return string(b), nil
}

func (c *content) readFields() (map[string]any, error) {
	out := map[string]any{}
	raw := c.fieldsJSON
	if c.fieldsFile != "" {
		b, err := os.ReadFile(c.fieldsFile)
		if err != nil {
			return nil, store.Errf("not-found", "項目のファイルが読めない: %s", c.fieldsFile)
		}
		raw = string(b)
	}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			return nil, store.Errf("invalid", "項目の JSON が読めない: %v", err)
		}
	}
	for _, f := range c.fields {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			return nil, store.Errf("invalid", "項目は key=value で渡す: %s", f)
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func (c *content) readLinks(st *store.State) ([]store.Link, error) {
	var out []store.Link
	for _, l := range c.links {
		rel, to, ok := strings.Cut(l, ":")
		if !ok {
			return nil, store.Errf("invalid", "つながりは 種類:番号[@版] で渡す: %s", l)
		}
		if strings.HasPrefix(to, "RUN-") {
			out = append(out, store.Link{Rel: rel, To: to})
			continue
		}
		id, _, hasVer := strings.Cut(to, "@")
		t, err := parseTarget(st, to)
		if err != nil {
			return nil, err
		}
		link := store.Link{Rel: rel, To: id}
		if hasVer {
			link.Version = t.Version
		}
		out = append(out, link)
	}
	return out, nil
}

func (c *content) originValue() *store.Origin {
	return &store.Origin{Category: c.origin, Source: c.source, By: c.by}
}

// 項目を読むための小さな道具。保存した項目は、読み直すと一般的な形になっている。

func str(f map[string]any, k string) string {
	s, _ := f[k].(string)
	return s
}

func boolean(f map[string]any, k string) bool {
	b, _ := f[k].(bool)
	return b
}

func num(f map[string]any, k string) float64 {
	n, _ := f[k].(float64)
	return n
}

func strs(f map[string]any, k string) []string {
	var out []string
	switch v := f[k].(type) {
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	case []string:
		out = v
	}
	return out
}

// decode は、項目の値を決まった形に読み直す。
func decode(v any, into any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, into)
}

func open(root string) (*store.Store, error) { return store.Open(root) }

func summary(o *store.Object) map[string]any {
	h := o.Head()
	return map[string]any{"id": o.ID, "kind": store.Kinds[o.Kind], "version": h.N, "title": h.Title, "status": h.Status}
}
