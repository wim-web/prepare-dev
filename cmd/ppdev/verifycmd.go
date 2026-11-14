package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/wim-web/prepare-dev/internal/policy"
	"github.com/wim-web/prepare-dev/internal/store"
	"github.com/wim-web/prepare-dev/internal/verify"
)

func verifierTimeout(cfg *policy.Settings, seconds int) time.Duration {
	if seconds <= 0 {
		seconds = cfg.VerifierTimeout
	}
	if seconds <= 0 {
		seconds = 120
	}
	return time.Duration(seconds) * time.Second
}

func abs(root, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, p)
}

func linkTo(v *store.Version, rel string) string {
	for _, l := range v.Links {
		if l.Rel == rel {
			return l.To
		}
	}
	return ""
}

func frmBody(file, toolchain string, claims, examples []string, r verify.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "検証器：%s\nファイル：%s\n", toolchain, file)
	statement := map[string]string{}
	for _, c := range r.Claims {
		statement[c.Name] = c.Statement
	}
	section := func(title string, names []string) {
		if len(names) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n**%s**\n", title)
		for _, n := range names {
			fmt.Fprintf(&b, "\n- `%s`\n\n```\n%s\n```\n", n, statement[n])
		}
	}
	section("証明する主張（検証器が表示した文面）", claims)
	section("具体例", examples)
	if len(r.Defs) > 0 {
		b.WriteString("\n**主張が使う定義**\n")
		names := make([]string, 0, len(r.Defs))
		for n := range r.Defs {
			names = append(names, n)
		}
		slices.Sort(names)
		for _, n := range names {
			fmt.Fprintf(&b, "\n- `%s`\n\n```\n%s\n```\n", n, r.Defs[n])
		}
	}
	return b.String()
}

// cmdFrmRegister は、機械向けに書いた条件を記録する。
// 主張の文面と定義は、検証器自身に表示させたものを保存する。
func cmdFrmRegister(root string, args []string) (any, error) {
	fs := flags("frm register")
	title := fs.String("title", "", "題")
	file := fs.String("file", "", "検証器に渡すファイル（必須）")
	exportFile := fs.String("export-file", "", "図と突き合わせのための書き出しの断片")
	mutationsFile := fs.String("mutations-file", "", "わざと壊す変種の一覧（JSON）")
	grt := fs.String("grt", "", "対応する保証要求")
	targetKind := fs.String("target-kind", "model", "何を検証するか: model（動作ルール） | implementation（コード）")
	id := fs.String("id", "", "版を重ねる記録の番号")
	base := fs.Int("base", 0, "元にした版（--id と一緒に渡す）")
	seconds := fs.Int("timeout", 0, "検証器の制限時間（秒）")
	var claims, examples, defs multi
	fs.Var(&claims, "claim", "証明する主張の名前（繰り返せる）")
	fs.Var(&examples, "example", "具体例の名前（繰り返せる）")
	fs.Var(&defs, "def", "主張が使う定義の名前（繰り返せる）")
	if _, err := parse(fs, args, 0); err != nil {
		return nil, err
	}
	if *file == "" || len(claims) == 0 {
		return nil, store.Errf("invalid", "ファイル（--file）と、証明する主張（--claim）が要る")
	}
	s, err := open(root)
	if err != nil {
		return nil, err
	}
	cfg, err := policy.Load(root)
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(abs(root, *file))
	if err != nil {
		return nil, store.Errf("not-found", "ファイルが読めない: %s", *file)
	}
	var mutations []verify.Mutation
	if *mutationsFile != "" {
		b, err := os.ReadFile(abs(root, *mutationsFile))
		if err != nil {
			return nil, store.Errf("not-found", "変種の一覧が読めない: %s", *mutationsFile)
		}
		if err := json.Unmarshal(b, &mutations); err != nil {
			return nil, store.Errf("invalid", "変種の一覧が読めない: %v", err)
		}
		for _, m := range mutations {
			if _, err := m.Apply(content); err != nil {
				return nil, store.Errf("invalid", "%v", err)
			}
		}
	}
	dir := filepath.Dir(abs(root, *file))
	probes := append(append([]string{}, claims...), examples...)
	r := verify.CheckLean(dir, content, probes, defs, verifierTimeout(cfg, *seconds))
	if r.Verdict == "timeout" || r.Verdict == "error" {
		return nil, store.Errf("invalid", "検証器から主張の文面を取れなかった（%s）: %s", r.Verdict, r.Detail)
	}
	for _, c := range r.Claims {
		if c.Missing {
			return nil, store.Errf("invalid", "検証器が %s を見つけられない", c.Name)
		}
	}
	toolchain := verify.LeanVersion(dir)
	fields := map[string]any{
		"checker": "lean", "toolchain": toolchain, "target_kind": *targetKind,
		"file": *file, "export_file": *exportFile,
		"claims": []string(claims), "examples": []string(examples), "defs": []string(defs),
		"mutations": mutations, "statement_hash": verify.StatementHash(r),
	}
	var links []store.Link
	if *grt != "" {
		links = append(links, store.Link{Rel: "formalizes", To: *grt})
	}
	res, _, err := s.Update("", func(st *store.State) ([]store.Op, func(*store.State) any, error) {
		op := store.Op{Type: "put", Kind: "FRM", Title: *title, Body: frmBody(*file, toolchain, claims, examples, r),
			Fields: fields, Links: links, Origin: &store.Origin{Category: store.CatAI}}
		if *id != "" {
			op.ID, op.Base = *id, *base
			if o, ok := st.Objects[*id]; ok && op.Title == "" {
				op.Title = o.Head().Title
			}
		} else {
			op.ID = st.Reserve("FRM")
		}
		frm := op.ID
		return []store.Op{op}, func(st *store.State) any {
			h := st.Objects[frm].Head()
			return map[string]any{"id": frm, "version": h.N, "status": h.Status, "statement_hash": fields["statement_hash"],
				"next": "この版の主張・前提・対象範囲をレビューしてから検証する。レビュー後に主張や定義を変えると、statement_hash が変わり、再レビューが要る"}
		}, nil
	})
	return res, err
}

type audit struct {
	Holes             []string `json:"holes"`
	UnapprovedAxioms  []string `json:"unapproved_axioms"`
	Flagged           []string `json:"flagged"`
	IndependentCheck  string   `json:"independent_recheck"`
	AllowedAxioms     []string `json:"allowed_axioms"`
	AllowedAxiomsFrom string   `json:"allowed_axioms_from"`
}

// cmdVerifyLean は、記録した形式条件を検証器にかけ、判定と受理確認を証拠として残す。
func cmdVerifyLean(root string, args []string) (any, error) {
	fs := flags("verify lean")
	frmID := fs.String("frm", "", "検証する形式条件の番号（必須）")
	seconds := fs.Int("timeout", 0, "検証器の制限時間（秒）")
	if _, err := parse(fs, args, 0); err != nil {
		return nil, err
	}
	s, err := open(root)
	if err != nil {
		return nil, err
	}
	cfg, err := policy.Load(root)
	if err != nil {
		return nil, err
	}
	timeout := verifierTimeout(cfg, *seconds)

	var frm store.Version
	var grtID string
	a := audit{Holes: []string{}, UnapprovedAxioms: []string{}, Flagged: []string{},
		IndependentCheck: "未実施。検証器と同じ実行の中で公理を調べている。別の検査器での再検査はしていない"}
	err = s.View(func(st *store.State) error {
		o, ok := st.Objects[*frmID]
		if !ok || o.Kind != "FRM" {
			return store.Errf("not-found", "形式条件 %s がない", *frmID)
		}
		frm = *o.Head()
		grtID = linkTo(&frm, "formalizes")
		a.AllowedAxiomsFrom = "保証要求がないので、どの公理も認めていない"
		if g, ok := st.Objects[grtID]; ok {
			a.AllowedAxioms = strs(g.Head().Fields, "allowed_axioms")
			a.AllowedAxiomsFrom = fmt.Sprintf("%s@%d（状態: %s）", g.ID, g.Head().N, g.Head().Status)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	f := frm.Fields
	file := str(f, "file")
	content, err := os.ReadFile(abs(root, file))
	if err != nil {
		return nil, store.Errf("not-found", "ファイルが読めない: %s", file)
	}
	snapshot, err := s.Snapshot([]string{file})
	if err != nil {
		return nil, err
	}
	target := store.Target{ID: *frmID, Version: frm.N}

	var runID string
	if _, _, err = s.Update("", func(st *store.State) ([]store.Op, func(*store.State) any, error) {
		runID = st.Reserve("RUN")
		return []store.Op{{Type: "run.new", Run: &store.Run{ID: runID, Role: "verify", State: "running",
			Targets: []store.Target{target}, Snapshot: snapshot}}}, nil, nil
	}); err != nil {
		return nil, err
	}

	dir := filepath.Dir(abs(root, file))
	claims, examples, defs := strs(f, "claims"), strs(f, "examples"), strs(f, "defs")
	probes := append(append([]string{}, claims...), examples...)
	r := verify.CheckLean(dir, content, probes, defs, timeout)

	a.Holes = append(a.Holes, r.Holes...)
	a.Flagged = append(a.Flagged, verify.Flagged(content)...)
	for _, c := range r.Claims {
		if !c.Missing && !c.Proven() {
			a.Holes = append(a.Holes, fmt.Sprintf("%s は未証明の穴に頼っている", c.Name))
		}
		for _, ax := range c.Axioms {
			if ax != "sorryAx" && !slices.Contains(a.AllowedAxioms, ax) && !slices.Contains(a.UnapprovedAxioms, ax) {
				a.UnapprovedAxioms = append(a.UnapprovedAxioms, ax)
			}
		}
	}
	statementHash := ""
	if r.Verdict == "accepted" || r.Verdict == "rejected" {
		statementHash = verify.StatementHash(r)
	}

	var mutations []verify.Mutation
	decode(f["mutations"], &mutations)
	results := []verify.MutationResult{}
	if r.Verdict == "accepted" {
		for _, m := range mutations {
			results = append(results, verify.CheckMutation(dir, content, m, probes, defs, timeout))
		}
	}

	fields := map[string]any{
		"method": "theorem-proof", "target_kind": str(f, "target_kind"), "checker": "lean",
		"toolchain": verify.LeanVersion(dir), "timeout_seconds": int(timeout.Seconds()),
		"file": file, "file_hash": snapshot[0].Hash, "frm": *frmID, "frm_version": frm.N,
		"statement_hash": statementHash, "statement_matches_registered": statementHash != "" && statementHash == str(f, "statement_hash"),
		"verdict": r.Verdict, "detail": r.Detail, "errors": r.Errors, "claims": r.Claims,
		"audit": a, "mutations": results, "duration_ms": r.DurationMS, "run": runID,
	}
	says := map[string]string{
		"accepted": "検証器は受理した。これは検証器の判定で、求める保証を満たしたかは ppdev guarantee check で照合する",
		"rejected": "証明が通っていない（未証明）。反例が見つかったという意味ではない",
		"timeout":  "時間切れ。成立するともしないとも言えない（未確認）",
		"error":    "検証器を実行できなかった（実行エラー）",
	}[r.Verdict]

	evidenceJSON, _ := json.Marshal(fields)
	answerHash, err := s.PutBlob(evidenceJSON)
	if err != nil {
		return nil, err
	}
	links := []store.Link{{Rel: "verifies", To: *frmID, Version: frm.N}, {Rel: "produced-by", To: runID}}
	if grtID != "" {
		links = append(links, store.Link{Rel: "evidence-for", To: grtID})
	}
	res, _, err := s.Update("", func(st *store.State) ([]store.Op, func(*store.State) any, error) {
		evd := st.Reserve("EVD")
		ops := []store.Op{{Type: "put", ID: evd, Kind: "EVD", Title: "形式証明の実行：" + frm.Title, Body: says,
			Fields: fields, Links: links, Origin: &store.Origin{Category: store.CatObserved, Source: "検証器の実行 " + runID}}}
		switch r.Verdict {
		case "accepted":
			ops = append(ops, store.Op{Type: "run.answer", ID: runID, Answer: &store.RunAnswer{Hash: answerHash, Summary: "受理"}})
		case "rejected":
			ops = append(ops, store.Op{Type: "run.fail", ID: runID, Fail: &store.Failure{Reason: "unproven", Detail: strings.Join(r.Errors, " / ")}})
		default:
			ops = append(ops, store.Op{Type: "run.fail", ID: runID, Fail: &store.Failure{Reason: r.Verdict, Detail: r.Detail}})
		}
		return ops, func(*store.State) any {
			return map[string]any{"evidence": evd, "run": runID, "verdict": r.Verdict, "says": says,
				"statement_matches_registered": fields["statement_matches_registered"],
				"audit":                        a, "mutations": results, "errors": r.Errors}
		}, nil
	})
	return res, err
}

// cmdVerifyTest は、テストなどの命令を実行し、結果を証拠として残す。
func cmdVerifyTest(root string, args []string) (any, error) {
	fs := flags("verify test")
	grt := fs.String("grt", "", "対応する保証要求")
	method := fs.String("method", "test", "確認方法: conformance-test（モデルとコードの突き合わせ） | test")
	command := fs.String("cmd", "", "実行する命令（必須）")
	title := fs.String("title", "", "題")
	depth := fs.Int("depth", 0, "突き合わせた並びの長さ（適合テストのとき）")
	seconds := fs.Int("timeout", 600, "制限時間（秒）")
	var inputs multi
	fs.Var(&inputs, "input", "結果が依存するファイル（内容を保存する。繰り返せる）")
	if _, err := parse(fs, args, 0); err != nil {
		return nil, err
	}
	if *command == "" {
		return nil, store.Errf("invalid", "実行する命令（--cmd）が要る")
	}
	s, err := open(root)
	if err != nil {
		return nil, err
	}
	snapshot, err := s.Snapshot(inputs)
	if err != nil {
		return nil, err
	}
	var runID string
	if _, _, err = s.Update("", func(st *store.State) ([]store.Op, func(*store.State) any, error) {
		var targets []store.Target
		if *grt != "" {
			t, err := parseTarget(st, *grt)
			if err != nil {
				return nil, nil, err
			}
			targets = append(targets, t)
		}
		runID = st.Reserve("RUN")
		return []store.Op{{Type: "run.new", Run: &store.Run{ID: runID, Role: "verify-test", State: "running", Targets: targets, Snapshot: snapshot}}}, nil, nil
	}); err != nil {
		return nil, err
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*seconds)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", *command)
	cmd.Dir = root
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	runErr := cmd.Run()
	verdict, exit := "accepted", 0
	var exitErr *exec.ExitError
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		verdict = "timeout"
	case errors.As(runErr, &exitErr):
		verdict, exit = "rejected", exitErr.ExitCode()
	case runErr != nil:
		verdict = "error"
	}
	outHash, err := s.PutBlob(out.Bytes())
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) > 15 {
		lines = lines[len(lines)-15:]
	}
	fields := map[string]any{"method": *method, "command": *command, "inputs": snapshot, "depth": *depth,
		"verdict": verdict, "exit_code": exit, "output": outHash, "output_tail": lines,
		"duration_ms": time.Since(start).Milliseconds(), "run": runID}
	says := map[string]string{
		"accepted": "実行した範囲では期待どおりだった。試していない場合まで正しいとは言えない",
		"rejected": "期待と違う結果が出た",
		"timeout":  "時間切れ（未確認）",
		"error":    "命令を実行できなかった（実行エラー）",
	}[verdict]
	links := []store.Link{{Rel: "produced-by", To: runID}}
	if *grt != "" {
		links = append(links, store.Link{Rel: "evidence-for", To: strings.SplitN(*grt, "@", 2)[0]})
	}
	name := *title
	if name == "" {
		name = "テストの実行：" + *command
	}
	res, _, err := s.Update("", func(st *store.State) ([]store.Op, func(*store.State) any, error) {
		evd := st.Reserve("EVD")
		ops := []store.Op{{Type: "put", ID: evd, Kind: "EVD", Title: name, Body: says, Fields: fields, Links: links,
			Origin: &store.Origin{Category: store.CatObserved, Source: "命令の実行 " + runID}}}
		if verdict == "accepted" {
			ops = append(ops, store.Op{Type: "run.answer", ID: runID, Answer: &store.RunAnswer{Hash: outHash, Summary: "成功"}})
		} else {
			reason := map[string]string{"rejected": "error", "timeout": "timeout", "error": "error"}[verdict]
			ops = append(ops, store.Op{Type: "run.fail", ID: runID, Fail: &store.Failure{Reason: reason, Detail: says}})
		}
		return ops, func(*store.State) any {
			return map[string]any{"evidence": evd, "run": runID, "verdict": verdict, "says": says, "output_tail": lines}
		}, nil
	})
	return res, err
}

// evidenceCurrent は、証拠が今の内容についてのものかを確かめる。
// 違うところがあれば理由を返す。
func evidenceCurrent(s *store.Store, st *store.State, v *store.Version) []string {
	var reasons []string
	f := v.Fields
	check := func(path, hash string) {
		switch now := s.FileHash(path); {
		case now == "":
			reasons = append(reasons, fmt.Sprintf("%s が読めない。今の内容に使えるか分からない", path))
		case now != hash:
			reasons = append(reasons, fmt.Sprintf("%s が、確かめたあとで変わっている", path))
		}
	}
	if frm := str(f, "frm"); frm != "" {
		if o, ok := st.Objects[frm]; !ok {
			reasons = append(reasons, "元の形式条件がない")
		} else if head := o.Head().N; head != int(num(f, "frm_version")) {
			reasons = append(reasons, fmt.Sprintf("形式条件 %s が版 %d から版 %d に変わっている", frm, int(num(f, "frm_version")), head))
		}
		check(str(f, "file"), str(f, "file_hash"))
	}
	var inputs []store.FileRef
	decode(f["inputs"], &inputs)
	for _, in := range inputs {
		check(in.Path, in.Hash)
	}
	return reasons
}

type requirement struct {
	Method   string `json:"method"`
	Target   string `json:"target"`
	MinDepth int    `json:"min_depth"`
}

var methodNames = map[string]string{
	"theorem-proof":    "形式証明",
	"model-check":      "モデル検査",
	"conformance-test": "適合テスト（モデルとコードの突き合わせ）",
	"test":             "実行テスト",
}

// guaranteeReport は、求める保証と、手元の証拠を照合した結果。
type guaranteeReport struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	State    string   `json:"state"` // fulfilled | insufficient | undecided
	Summary  string   `json:"summary"`
	Missing  []string `json:"missing,omitempty"`
	Evidence []string `json:"evidence,omitempty"`
	Says     []string `json:"says,omitempty"`
}

// guaranteeState は、保証要求を満たす証拠が揃っているかを判定する。
// 要求は、得られた証拠に合わせて弱めない。足りないものは足りないと返す。
func guaranteeState(s *store.Store, st *store.State, cfg *policy.Settings, o *store.Object) guaranteeReport {
	head := o.Head()
	f := head.Fields
	rep := guaranteeReport{ID: o.ID, Title: head.Title}
	undecided := func(why string) guaranteeReport {
		rep.State, rep.Summary = "undecided", "未決。満たしたとは記録しない"
		rep.Missing = append(rep.Missing, why)
		return rep
	}
	if head.Status != "adopted" {
		return undecided(fmt.Sprintf("この保証要求（版 %d）は、利用者がまだ採用していない", head.N))
	}
	switch str(f, "necessity") {
	case "required", "auxiliary":
	default:
		return undecided("必須かどうかが決まっていない")
	}
	var reqs []requirement
	decode(f["requires"], &reqs)
	if len(reqs) == 0 {
		return undecided("どの方法で確かめれば足りるかが決まっていない")
	}

	latest := func(method string) *store.Object {
		evds := st.OfKind("EVD")
		for i := len(evds) - 1; i >= 0; i-- {
			h := evds[i].Head()
			if str(h.Fields, "method") == method && linkTo(h, "evidence-for") == o.ID {
				return evds[i]
			}
		}
		return nil
	}
	for _, req := range reqs {
		name := methodNames[req.Method]
		if name == "" {
			name = req.Method
		}
		miss := func(format string, a ...any) { rep.Missing = append(rep.Missing, name+"："+fmt.Sprintf(format, a...)) }
		evd := latest(req.Method)
		if evd == nil {
			miss("証拠がない")
			continue
		}
		e := evd.Head().Fields
		rep.Evidence = append(rep.Evidence, evd.ID)
		before := len(rep.Missing)
		switch str(e, "verdict") {
		case "accepted":
		case "timeout":
			miss("%s は時間切れ（未確認）", evd.ID)
		case "rejected":
			if req.Method == "theorem-proof" {
				miss("%s は証明が通っていない（未証明）", evd.ID)
			} else {
				miss("%s は期待と違う結果だった", evd.ID)
			}
		default:
			miss("%s は実行エラー", evd.ID)
		}
		for _, why := range evidenceCurrent(s, st, evd.Head()) {
			miss("%s は今の内容の証拠ではない。%s", evd.ID, why)
		}
		if req.Method == "theorem-proof" {
			var a audit
			decode(e["audit"], &a)
			if len(a.Holes) > 0 {
				miss("未証明の穴がある（%s）", strings.Join(a.Holes, "、"))
			}
			if len(a.UnapprovedAxioms) > 0 {
				miss("認めていない公理に頼っている（%s）", strings.Join(a.UnapprovedAxioms, "、"))
			}
			if !boolean(e, "statement_matches_registered") {
				miss("検証した主張が、記録した形式条件の主張と違う")
			}
			if req.Target == "implementation" && str(e, "target_kind") != "implementation" {
				miss("動作ルールの証明はあるが、求めているのはコードの証明")
			}
			if boolean(f, "require_mutation") {
				var ms []verify.MutationResult
				decode(e["mutations"], &ms)
				if len(ms) == 0 {
					miss("わざと壊した変種での確認が未実施")
				}
				for _, m := range ms {
					if !m.Detected {
						miss("変種「%s」を検出できていない（%s）", m.Name, m.Outcome)
					}
				}
			}
			if boolean(f, "require_review") {
				frm := str(e, "frm")
				if _, ok := st.Objects[frm]; !ok {
					miss("レビューする形式条件がない")
				} else if r := reviewState(st, cfg, frm); !r.Complete {
					miss("形式条件 %s のレビューが完了していない（%s）", r.Target, r.Explanation)
				}
			}
		} else if req.MinDepth > 0 && int(num(e, "depth")) < req.MinDepth {
			miss("突き合わせた長さ %d が、求める長さ %d に足りない", int(num(e, "depth")), req.MinDepth)
		}
		if len(rep.Missing) == before {
			switch req.Method {
			case "theorem-proof":
				if str(e, "target_kind") == "implementation" {
					rep.Says = append(rep.Says, "実装に対する形式証明："+evd.ID+"。指定した実装・主張・前提について、検証器が受理した範囲")
				} else {
					rep.Says = append(rep.Says, "抽象モデルの形式証明："+evd.ID+"。モデル上の主張について成立。実装との対応は別に確かめる")
				}
			case "model-check":
				rep.Says = append(rep.Says, "モデル検査："+evd.ID+"。設定した範囲の検査結果")
			default:
				rep.Says = append(rep.Says, name+"："+evd.ID+"。実行したケースで確認した動作")
			}
		}
	}
	if len(rep.Missing) > 0 {
		rep.State, rep.Summary = "insufficient", "不足。求める保証をまだ満たしていない"
		rep.Says = nil
	} else {
		rep.State, rep.Summary = "fulfilled", "充足。求める保証を満たす証拠が揃っている"
	}
	return rep
}

func cmdGuaranteeCheck(root string, args []string) (any, error) {
	pos, err := parse(flags("guarantee check <保証要求の番号>"), args, 1)
	if err != nil {
		return nil, err
	}
	s, err := open(root)
	if err != nil {
		return nil, err
	}
	cfg, err := policy.Load(root)
	if err != nil {
		return nil, err
	}
	var rep guaranteeReport
	err = s.View(func(st *store.State) error {
		o, ok := st.Objects[pos[0]]
		if !ok || o.Kind != "GRT" {
			return store.Errf("not-found", "保証要求 %s がない", pos[0])
		}
		rep = guaranteeState(s, st, cfg, o)
		return nil
	})
	return rep, err
}
