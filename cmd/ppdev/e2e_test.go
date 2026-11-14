package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wim-web/prepare-dev/internal/verify"
)

// 一連の作業を、実際のコマンドと実際の検証器で通す。
// 要求 → 具体例 → 保証要求 → 形式条件 → レビュー → 形式証明 → 適合テスト → 図 → 変更後の再確認。

type cli struct {
	t    *testing.T
	bin  string
	root string
}

func (c cli) exec(stdin string, args ...string) (map[string]any, int, string) {
	c.t.Helper()
	cmd := exec.Command(c.bin, append([]string{"--root", c.root}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(os.Environ(), "PPDEV_USER_MODELS=off") // 利用者自身の設定の表に左右されないようにする
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			c.t.Fatalf("%v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	out := map[string]any{}
	if b := bytes.TrimSpace(stdout.Bytes()); len(b) > 0 {
		if b[0] == '[' {
			var list []any
			json.Unmarshal(b, &list)
			out["list"] = list
		} else if err := json.Unmarshal(b, &out); err != nil {
			c.t.Fatalf("%v: 出力が読めない: %s", args, b)
		}
	}
	return out, code, stderr.String()
}

func (c cli) ok(args ...string) map[string]any {
	c.t.Helper()
	out, code, stderr := c.exec("", args...)
	if code != 0 {
		c.t.Fatalf("%v: 終了コード %d\n%s", args, code, stderr)
	}
	return out
}

func (c cli) fails(want int, args ...string) (map[string]any, string) {
	c.t.Helper()
	out, code, stderr := c.exec("", args...)
	if code != want {
		c.t.Fatalf("%v: 終了コード %d を期待したが %d\n%s", args, want, code, stderr)
	}
	return out, stderr
}

func text(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func wantContains(t *testing.T, v any, part string) {
	t.Helper()
	if !strings.Contains(text(v), part) {
		t.Fatalf("%q が含まれていない: %s", part, text(v))
	}
}

func setup(t *testing.T) cli {
	t.Helper()
	if verify.FindLean() == "" {
		if os.Getenv("PPDEV_REQUIRE_LEAN") != "" {
			t.Fatal("lean が見つからない")
		}
		t.Skip("lean が見つからないので、一連の確認は実行していない")
	}
	c := build(t)
	root := c.root
	os.MkdirAll(filepath.Join(root, "model"), 0o755)
	for _, name := range []string{"CancelRule.lean", "CancelRuleExport.lean", "mutations.json", "lean-toolchain"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "model", name))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(root, "model", name), b, 0o644)
	}
	return c
}

// build は、コマンドを作り、空のプロジェクトを用意する。検証器は要らない。
func build(t *testing.T) cli {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "ppdev")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("ビルドできない: %v\n%s", err, out)
	}
	return cli{t: t, bin: bin, root: t.TempDir()}
}

const guaranteeFields = `{
  "necessity": "required",
  "requires": [
    {"method": "theorem-proof", "target": "model"},
    {"method": "conformance-test", "target": "implementation", "min_depth": 4}
  ],
  "allowed_axioms": ["propext", "Quot.sound", "Classical.choice"],
  "require_mutation": true,
  "require_review": true,
  "premises": ["回答を受け取ってから現在の結果を更新するまで、別の出来事は割り込まない"],
  "out_of_scope": ["取り消していない実行が複数あるときの扱い"]
}`

var claims = []string{
	"--claim", "cancelled_run_never_updates_current", "--claim", "live_run_answer_is_adopted",
	"--claim", "answer_is_always_saved", "--claim", "stale_answer_never_updates_current",
	"--example", "example_retry_keeps_second_result", "--example", "example_answer_before_cancel_is_adopted",
	"--def", "step", "--def", "init", "--def", "run", "--def", "State", "--def", "Event",
}

func TestEndToEnd(t *testing.T) {
	c := setup(t)
	c.ok("init")
	wantContains(t, c.ok("status"), "未設定")

	// 要求は利用者の発言。具体例は AI の提案で、決定が結び付くまで採用されない。
	c.ok("new", "REQ", "--title", "取り消したレビューの回答で、やり直した結果を上書きしない",
		"--origin", "user", "--source", "発言「取り消した 1 回目の回答で 2 回目を上書きしないでほしい」")
	ex := c.ok("new", "EX", "--title", "1 回目を取り消し、2 回目の指摘のあとに 1 回目の指摘なしが届く", "--link", "example-of:REQ-0001")
	if ex["status"] != "proposed" {
		t.Fatalf("AI の具体例が %v になった", ex["status"])
	}
	c.fails(1, "decide", "--title", "採用", "--adopt", "EX-0001@1") // 発言への参照がない決定は記録できない
	c.ok("decide", "--title", "具体例を採用する", "--source", "発言「その例で合っている」", "--adopt", "EX-0001@1")

	// 保証要求：未採用の間は未決。採用しても、証拠がなければ不足。
	c.ok("new", "GRT", "--title", "取消後の回答で現在の結果を更新しない", "--fields-json", guaranteeFields, "--link", "guarantees:REQ-0001")
	wantContains(t, c.ok("guarantee", "check", "GRT-0001"), `"state":"undecided"`)
	c.ok("decide", "--title", "ルールを証明し、コードはテストで確かめる", "--source", "発言「案 1 でよい」", "--adopt", "GRT-0001@1")
	wantContains(t, c.ok("guarantee", "check", "GRT-0001"), `"state":"insufficient"`)

	// 形式条件を記録する。
	frm := c.ok(append([]string{"frm", "register", "--title", "取消ルール", "--file", "model/CancelRule.lean",
		"--export-file", "model/CancelRuleExport.lean", "--mutations-file", "model/mutations.json", "--grt", "GRT-0001"}, claims...)...)
	if frm["id"] != "FRM-0001" {
		t.Fatalf("形式条件: %v", frm)
	}

	// 設定のない役割は起動しない。
	denied, _ := c.fails(2, "spawn", "check", "--role", "spec-review", "--target", "FRM-0001")
	wantContains(t, denied, "設定の表にない")
	// 設定の表：一行が一つの役割。モデルを並べた数が人数になる。
	sheet := `# 担当の設定

- 説明の行は読み飛ばす。

仕様レビュー: model-r, model-r

通常の思考量: high
思考量の引き上げ: xhigh（検証器の時間切れ）
禁止する思考量: max
実行環境で止める: on
検証器の制限時間: 120

## レビューで必ず確かめる観点

- meaning: 意味が対応するか
- premises: 前提が妥当か
`
	os.WriteFile(filepath.Join(c.root, ".ppdev", "models.md"), []byte(sheet), 0o644)
	shown := c.ok("policy", "show")
	wantContains(t, shown, `"people":2`)
	if strings.Contains(text(shown), "warnings") {
		t.Fatalf("設定の表に読めない行がある: %s", text(shown))
	}

	// レビュー：文書を作り、新しい文脈の担当を、設定を通して起動する。
	bundle := c.ok("review", "bundle", "--target", "FRM-0001", "--target", "GRT-0001")
	hash, path := bundle["bundle"].(string), bundle["path"].(string)
	doc, _ := os.ReadFile(filepath.Join(c.root, path))
	lineOf := func(prefix string) string {
		for i, l := range strings.Split(string(doc), "\n") {
			if strings.HasPrefix(l, prefix) {
				return text(i + 3)
			}
		}
		t.Fatalf("レビュー文書に %q がない", prefix)
		return ""
	}

	spawn := c.ok("spawn", "check", "--role", "spec-review", "--target", "FRM-0001", "--viewpoint", "meaning", "--viewpoint", "premises")
	review := spawn["run"].(string)
	ticket := spawn["ticket_line"].(string)
	hook := func(prompt, model string) string {
		b, _ := json.Marshal(map[string]any{"cwd": c.root, "tool_name": "Agent", "tool_input": map[string]any{"prompt": prompt, "model": model}})
		return string(b)
	}
	// 実行環境の入口：札のない起動と、許可と違うモデルでの起動は止まる。
	if _, code, stderr := c.exec(hook("レビューして", "model-r"), "spawn", "gate"); code != 2 || !strings.Contains(stderr, "札") {
		t.Fatalf("札のない起動が止まらない: %d %s", code, stderr)
	}
	if _, code, _ := c.exec(hook("レビューして "+ticket, "other-model"), "spawn", "gate"); code != 2 {
		t.Fatalf("違うモデルでの起動が止まらない: %d", code)
	}
	if _, code, stderr := c.exec(hook("レビューして "+ticket, "model-r"), "spawn", "gate"); code != 0 {
		t.Fatalf("許可された起動が止まった: %d %s", code, stderr)
	}
	if _, code, _ := c.exec(hook("レビューして "+ticket, "model-r"), "spawn", "gate"); code != 2 {
		t.Fatal("同じ札で二度起動できた")
	}
	// 禁止した思考量は、条件を示しても使えない。
	c.fails(2, "spawn", "check", "--role", "spec-review", "--effort", "max")

	// 指摘：行番号から、どの記録のどの版への指摘かが決まる。決まらないものは推測で付けない。
	f1 := c.ok("review", "finding", "--run", review, "--bundle", hash, "--line", lineOf("## FRM-0001@1"),
		"--title", "採用後に取り消した場合が書かれていない", "--viewpoint", "premises", "--problem", "採用したあとで取り消したときの扱いが読めない")
	if f1["target"] != "FRM-0001@1" {
		t.Fatalf("指摘の対応先: %v", f1)
	}
	f2 := c.ok("review", "finding", "--run", review, "--bundle", hash, "--line", "1", "--title", "場所の分からない指摘", "--problem", "x")
	f3 := c.ok("review", "finding", "--run", review, "--bundle", "deadbeef", "--line", "10", "--title", "知らない文書への指摘", "--problem", "x")
	if f2["status"] != "unmapped" || f3["status"] != "unmapped" {
		t.Fatalf("対応先のない指摘が付いた: %v %v", f2, f3)
	}
	c.ok("run", "answer", review, "--text", "指摘 3 件", "--summary", "指摘あり")
	wantContains(t, c.ok("review", "status", "--target", "FRM-0001"), `"complete":false`)

	// 指摘は、書いた側の判断だけでは不採用にできない。
	c.fails(1, "resolve", "FND-0001", "--to", "rejected", "--reason", "対象外に書いてある")
	c.ok("resolve", "FND-0001", "--to", "rejected", "--reason", "対象外として保証要求に書いてある", "--refutation", "GRT-0001@1")
	c.ok("resolve", "FND-0002", "--to", "rejected", "--reason", "確認役に確かめた。指摘ではなかった", "--refutation", "FRM-0001@1")
	c.ok("resolve", "FND-0003", "--to", "rejected", "--reason", "確認役に確かめた。指摘ではなかった", "--refutation", "FRM-0001@1")
	wantContains(t, c.ok("review", "status", "--target", "FRM-0001"), `"complete":true`)

	// 形式証明：検証器が受理し、穴はなく、わざと壊した変種は四つとも落ちる。
	proof := c.ok("verify", "lean", "--frm", "FRM-0001")
	if proof["verdict"] != "accepted" || proof["statement_matches_registered"] != true {
		t.Fatalf("証明: %s", text(proof))
	}
	if n := strings.Count(text(proof["mutations"]), `"detected":true`); n != 4 {
		t.Fatalf("検出した変種が %d 個: %s", n, text(proof["mutations"]))
	}
	wantContains(t, proof["audit"], `"holes":[]`)
	wantContains(t, proof["audit"], `"unapproved_axioms":[]`)

	// ルールの証明だけでは足りない。コードとの突き合わせも要る。長さが足りなければ不足のまま。
	wantContains(t, c.ok("guarantee", "check", "GRT-0001"), "証拠がない")
	c.ok("verify", "test", "--grt", "GRT-0001", "--method", "conformance-test", "--cmd", "true", "--depth", "2", "--input", "model/CancelRule.lean")
	wantContains(t, c.ok("guarantee", "check", "GRT-0001"), "足りない")
	c.ok("verify", "test", "--grt", "GRT-0001", "--method", "conformance-test", "--cmd", "true", "--depth", "4", "--input", "model/CancelRule.lean")
	full := c.ok("guarantee", "check", "GRT-0001")
	wantContains(t, full, `"state":"fulfilled"`)
	wantContains(t, full, "実装との対応は別に確かめる")

	// 図：受理された検証結果の保存内容から作る。指摘は番号で返す。
	fig := c.ok("figure", "generate", "--frm", "FRM-0001", "--evidence", proof["evidence"].(string))
	page, _ := os.ReadFile(filepath.Join(c.root, fig["page"].(string)))
	for _, part := range []string{"検証対象から作った図", "E1", "取消済み", "確かめていないこと", "別の出来事は割り込まない"} {
		if !strings.Contains(string(page), part) {
			t.Fatalf("図の文書に %q がない:\n%s", part, page)
		}
	}
	wantContains(t, c.ok("figure", "check", "FIG-0001"), `"state":"current"`)
	reply := c.ok("reply", "record", "--figure", "FIG-0001@1", "--element", "E2", "--text", "取り消したあともここを通るの？", "--reply-id", "r1")
	if reply["status"] != "recorded" || reply["applicability"] != "current" {
		t.Fatalf("返答: %v", reply)
	}
	wantContains(t, c.ok("reply", "record", "--figure", "FIG-0001@1", "--element", "E2", "--text", "取り消したあともここを通るの？", "--reply-id", "r1"), "duplicate")
	wantContains(t, c.ok("reply", "record", "--figure", "FIG-0001@1", "--element", "E99", "--text", "ここ"), "unmapped")

	// 変更：主張が使う定義を書き換える。証明は通るままだが、レビューした内容とは違う。
	model := filepath.Join(c.root, "model", "CancelRule.lean")
	src, _ := os.ReadFile(model)
	changed := strings.Replace(string(src), "def init : State := { cancelled := [], history := [], current := none }",
		"def init : State := { cancelled := [], history := [(0, .noFindings)], current := none }", 1)
	if changed == string(src) {
		t.Fatal("定義を書き換えられなかった")
	}
	os.WriteFile(model, []byte(changed), 0o644)

	wantContains(t, c.ok("impact"), "確かめたあとで変わっている")
	wantContains(t, c.ok("guarantee", "check", "GRT-0001"), "今の内容の証拠ではない")
	wantContains(t, c.ok("figure", "check", "FIG-0001"), "現在への適用未確認")
	wantContains(t, c.ok("reply", "record", "--figure", "FIG-0001@1", "--element", "E1", "--text", "古い図を見て"), "古い図への指摘")

	again := c.ok("verify", "lean", "--frm", "FRM-0001")
	if again["verdict"] != "accepted" || again["statement_matches_registered"] != false {
		t.Fatalf("定義を変えたあとの証明: %s", text(again))
	}
	wantContains(t, c.ok("guarantee", "check", "GRT-0001"), "記録した形式条件の主張と違う")
	c.fails(1, "figure", "generate", "--frm", "FRM-0001", "--evidence", again["evidence"].(string))

	// 形式条件の版を重ねると、前の版のレビューは今の版のレビューにならない。
	c.ok(append([]string{"frm", "register", "--id", "FRM-0001", "--base", "1", "--file", "model/CancelRule.lean",
		"--export-file", "model/CancelRuleExport.lean", "--mutations-file", "model/mutations.json", "--grt", "GRT-0001"}, claims...)...)
	wantContains(t, c.ok("review", "status", "--target", "FRM-0001"), "古い版を読んだ")
	c.ok("verify", "lean", "--frm", "FRM-0001")
	wantContains(t, c.ok("guarantee", "check", "GRT-0001"), "レビューが完了していない")

	// 未証明の穴：検証器は受理するが、保証を満たす証拠にはしない。
	holed := strings.Replace(changed, "  simp only [run, List.foldl_append, List.foldl_cons, List.foldl_nil]\n  exact answer_saved _ r a fresh", "  sorry", 1)
	os.WriteFile(model, []byte(holed), 0o644)
	hole := c.ok("verify", "lean", "--frm", "FRM-0001")
	if hole["verdict"] != "accepted" || strings.Contains(text(hole["audit"]), `"holes":[]`) {
		t.Fatalf("穴を見つけていない: %s", text(hole))
	}
	wantContains(t, c.ok("guarantee", "check", "GRT-0001"), "未証明の穴")
	os.WriteFile(model, []byte(changed), 0o644)
}

func TestCancelAndLateAnswer(t *testing.T) {
	c := setup(t)
	c.ok("init")
	c.ok("new", "DES", "--title", "設計書")
	first := c.ok("run", "start", "--role", "spec-review", "--target", "DES-0001", "--fresh-context")["id"].(string)
	c.ok("run", "cancel", first)
	second := c.ok("run", "start", "--role", "spec-review", "--target", "DES-0001", "--fresh-context", "--retry-of", first)["id"].(string)
	adopted := c.ok("run", "answer", second, "--text", "未証明のときの表示が設計にない", "--summary", "指摘あり")
	if adopted["adopted"] != true {
		t.Fatalf("2 回目の回答: %v", adopted)
	}
	late := c.ok("run", "answer", first, "--text", "指摘なし", "--summary", "指摘なし", "--request-id", "answer-1")
	if late["adopted"] != false || late["saved_to_history"] != true {
		t.Fatalf("取り消した 1 回目の回答: %v", late)
	}
	wantContains(t, late, "取消済み")
	if cur := late["current"].(map[string]any); cur["run"] != second {
		t.Fatalf("現在の結果が 2 回目でなくなった: %v", cur)
	}
	// 同じ回答をもう一度送っても、履歴は増えない。
	c.ok("run", "answer", first, "--text", "指摘なし", "--request-id", "answer-1")
	shown := c.ok("run", "show", first)
	if n := len(shown["run"].(map[string]any)["answers"].([]any)); n != 1 {
		t.Fatalf("1 回目の履歴が %d 件", n)
	}
	wantContains(t, c.ok("tx", "answer-1"), `"applied":true`)
	wantContains(t, c.ok("tx", "answer-2"), `"applied":false`)
	wantContains(t, c.ok("status"), "取消済みの実行から回答が届いている")

	// 同じ旧版からの二つ目の変更は、衝突として終了コード 3 で拒否する。
	c.ok("revise", "DES-0001", "--base", "1", "--title", "設計書（A の変更）")
	c.fails(3, "revise", "DES-0001", "--base", "1", "--title", "設計書（B の変更）")

	// 利用者が明示した例外は、範囲を付けたときだけ記録できる。
	os.WriteFile(filepath.Join(c.root, ".ppdev", "models.md"), []byte("設計: m\n通常の思考量: high\n禁止する思考量: max\n"), 0o644)
	c.fails(2, "spawn", "check", "--role", "設計", "--effort", "max") // 役割は日本語の名前でも指定できる
	c.ok("decide", "--title", "今回は max で", "--source", "発言「今回は max で」")
	c.fails(1, "policy", "except", "--decision", "DEC-0001", "--title", "max を許す", "--role", "design", "--effort", "max")
	c.ok("policy", "except", "--decision", "DEC-0001", "--title", "設計担当を一度だけ max で", "--role", "design", "--effort", "max", "--uses", "1")
	once := c.ok("spawn", "check", "--role", "design", "--effort", "max")
	if once["exception"] != "EXC-0001" {
		t.Fatalf("例外が使われていない: %v", once)
	}
	wantContains(t, once, "手順としての指示")
	c.ok("run", "fail", once["run"].(string), "--reason", "stopped")
	c.fails(2, "spawn", "check", "--role", "design", "--effort", "max") // 二度目は使えない

	// 思考量は、仕事に合わせて一段上げられる。理由が記録に残る。
	c.fails(2, "spawn", "check", "--role", "設計", "--effort", "xhigh")
	raised := c.ok("spawn", "check", "--role", "設計", "--effort", "xhigh", "--reason", "順番の入れ替わりを含む、取り違えやすい設計")
	wantContains(t, raised, "一段上げて")
	wantContains(t, c.ok("policy", "show"), "取り違えやすい設計")
}

// init は、記録の置き場所を作るだけでなく、skill もプロジェクトに入れる。
func TestInitInstallsSkills(t *testing.T) {
	c := build(t)
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(c.root, rel))
		if err != nil {
			t.Fatalf("%s が読めない: %v", rel, err)
		}
		return string(b)
	}
	count := func(out map[string]any, key string) int {
		return len(out["skills"].(map[string]any)[key].([]any))
	}

	// 何も指定しなければ、.agents/skills と .claude/skills の二か所に入る。
	first := c.ok("init")
	if n := count(first, "copied"); n != 16 {
		t.Fatalf("入った skill が %d 個（八つを二か所で 16 のはず）: %s", n, text(first))
	}
	for _, rel := range []string{".agents/skills/prepare-dev/SKILL.md", ".claude/skills/prepare-setup/SKILL.md", ".ppdev/skills.json",
		// 説明のページの雛形は、手順書と同じフォルダに入る。
		".agents/skills/prepare-show/page.html", ".claude/skills/prepare-show/page.html"} {
		read(rel)
	}
	if _, err := os.Stat(filepath.Join(c.root, ".agents/skills/SOURCES.md")); err == nil {
		t.Fatal("skill でないファイルまで入った")
	}
	if !strings.Contains(read(".ppdev/.gitignore"), "skills-backup/") {
		t.Fatal("退避先が ignore されていない")
	}

	// もう一度実行しても、何も変わらない。
	again := c.ok("init")
	if count(again, "copied") != 0 || count(again, "unchanged") != 16 {
		t.Fatalf("二度目: %s", text(again))
	}

	// 編集した skill は残し、知らせる。
	edited := ".agents/skills/prepare-show/SKILL.md"
	os.WriteFile(filepath.Join(c.root, edited), []byte(read(edited)+"\n自分で足した行\n"), 0o644)
	kept := c.ok("init")
	wantContains(t, kept["skills"].(map[string]any)["left_as_is"], ".agents/skills/prepare-show")
	if !strings.Contains(read(edited), "自分で足した行") {
		t.Fatal("編集した skill が上書きされた")
	}
	wantContains(t, c.ok("status"), "中身がこの版と違う skill がある")

	// 置き換えるよう頼まれたら、元の中身を退避してから置き換える。
	replaced := c.ok("init", "--replace-skills")
	list := replaced["skills"].(map[string]any)["replaced"].([]any)
	if len(list) != 1 {
		t.Fatalf("置き換えた skill: %s", text(replaced))
	}
	backup := list[0].(map[string]any)["backup"].(string)
	if strings.Contains(read(edited), "自分で足した行") || !strings.Contains(read(filepath.Join(backup, "SKILL.md")), "自分で足した行") {
		t.Fatalf("退避と置き換えができていない（退避先 %s）", backup)
	}

	// 入れない、または場所を指定する。
	none := cli{t: t, bin: c.bin, root: t.TempDir()}
	none.ok("init", "--no-skills")
	if _, err := os.Stat(filepath.Join(none.root, ".agents")); err == nil {
		t.Fatal("--no-skills なのに skill が入った")
	}
	custom := cli{t: t, bin: c.bin, root: t.TempDir()}
	custom.ok("init", "--skills-dir", "tools/skills")
	if _, err := os.Stat(filepath.Join(custom.root, "tools/skills/prepare-dev/SKILL.md")); err != nil {
		t.Fatal("指定した場所に skill が入っていない")
	}
}
