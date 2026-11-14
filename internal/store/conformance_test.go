package store

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/wim-web/prepare-dev/internal/rule"
)

// 適合テスト：証明したモデル（model/CancelRule.lean）を実行して得た結果と、
// このコードの結果を、出来事の並びすべてについて突き合わせる。
//
// 確かめる範囲：実行二つ、回答二種類、長さ PPDEV_CONFORMANCE_DEPTH（既定 4）までの並びすべて。
// 回答が現在の内容に対するものか（fresh）は、モデルと同じ値を与える。
// fresh の判定そのもの（版の比較）は TestFreshIsVersionMatch で別に確かめる。

func leanPath() string {
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

func modelVectors(t *testing.T, depth int) []string {
	t.Helper()
	lean := leanPath()
	if lean == "" {
		if os.Getenv("PPDEV_REQUIRE_LEAN") != "" {
			t.Fatal("lean が見つからない。適合テストを実行できない")
		}
		t.Skip("lean が見つからないので、適合テストは実行していない")
	}
	modelDir, err := filepath.Abs(filepath.Join("..", "..", "model"))
	if err != nil {
		t.Fatal(err)
	}
	var src []byte
	for _, name := range []string{"CancelRule.lean", "CancelRuleExport.lean"} {
		b, err := os.ReadFile(filepath.Join(modelDir, name))
		if err != nil {
			t.Fatal(err)
		}
		src = append(append(src, b...), '\n')
	}
	file := filepath.Join(t.TempDir(), "Export.lean")
	if err := os.WriteFile(file, src, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(lean, "--run", file, "vectors", strconv.Itoa(depth))
	cmd.Dir = modelDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("モデルを実行できなかった: %v\n%s", err, stderr.String())
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}

var runIDs = map[string]string{"1": "RUN-0001", "2": "RUN-0002"}

// newTwoRuns は、同じ対象の同じ版を読む実行を二つ持つ状態を作る。
func newTwoRuns(t *testing.T) *State {
	t.Helper()
	st := NewState()
	tx := &Tx{ID: "setup", At: "t0", Ops: []Op{
		{Type: "put", ID: "DES-0001", Kind: "DES", Title: "設計書", Origin: &Origin{Category: CatAI}},
		{Type: "run.new", Run: &Run{ID: "RUN-0001", Role: "spec-review", State: "running", Targets: []Target{{"DES-0001", 1}}}},
		{Type: "run.new", Run: &Run{ID: "RUN-0002", Role: "spec-review", State: "running", Targets: []Target{{"DES-0001", 1}}}},
	}}
	if err := st.apply(tx); err != nil {
		t.Fatal(err)
	}
	return st
}

func showEntry(e rule.Entry) string {
	for n, id := range runIDs {
		if id == e.Run {
			return n + e.Answer
		}
	}
	return "?" + e.Answer
}

// showSlot は、状態をモデルと同じ書き方の一行にする。
func showSlot(s rule.State) string {
	var cancelled []string
	for _, n := range []string{"1", "2"} {
		if s.IsCancelled(runIDs[n]) {
			cancelled = append(cancelled, n)
		}
	}
	var history []string
	for _, e := range slices.Backward(s.History) { // モデルは新しいものが先頭
		history = append(history, showEntry(e))
	}
	current := "-"
	if s.Current != nil {
		current = showEntry(*s.Current)
	}
	return strings.Join(cancelled, ",") + "\t" + strings.Join(history, ",") + "\t" + current
}

func TestConformsToProvenModel(t *testing.T) {
	depth := 4
	if v := os.Getenv("PPDEV_CONFORMANCE_DEPTH"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			t.Fatalf("PPDEV_CONFORMANCE_DEPTH が数でない: %q", v)
		}
		depth = n
	}
	lines := modelVectors(t, depth)
	if len(lines) < 2 {
		t.Fatalf("モデルから結果が出ていない（%d 行）", len(lines))
	}
	for _, line := range lines {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			t.Fatalf("モデルの出力が読めない: %q", line)
		}
		events, want := parts[0], parts[1]

		pure := rule.State{} // ルールの関数だけを通した結果
		st := newTwoRuns(t)  // 記録の更新処理を通した結果
		slot := st.Runs["RUN-0001"].Slot()

		for _, ev := range strings.Fields(events) {
			run := runIDs[ev[1:2]]
			if run == "" {
				t.Fatalf("知らない実行: %q", ev)
			}
			switch ev[0] {
			case 's': // 始めるだけでは何も変わらない
			case 'c':
				pure = rule.Cancel(pure, run)
				if err := st.applyOp(&Op{Type: "run.cancel", ID: run}, "t"); err != nil {
					t.Fatal(err)
				}
			case 'a':
				answer, fresh := ev[2:3], ev[3:4] == "t"
				pure, _ = rule.Answer(pure, run, answer, fresh)
				st.answerRun(st.Runs[run], RunAnswer{Hash: answer}, fresh, "t")
			default:
				t.Fatalf("知らない出来事: %q", ev)
			}
		}
		if got := showSlot(pure); got != want {
			t.Fatalf("ルールの関数がモデルと違う\n並び: %s\nモデル: %q\nコード: %q", events, want, got)
		}
		if got := showSlot(st.Slots[slot]); got != want {
			t.Fatalf("記録の更新処理がモデルと違う\n並び: %s\nモデル: %q\nコード: %q", events, want, got)
		}
	}
	t.Logf("長さ %d までの並び %d 通りすべてで、モデルと一致した", depth, len(lines))
}

// 適合テストが違いを見つけられることの確認：わざと違う規則にすると落ちる。
func TestConformanceDetectsDifference(t *testing.T) {
	lines := modelVectors(t, 3)
	alwaysAdopt := func(s rule.State, run, answer string) rule.State {
		s.History = append(s.History, rule.Entry{Run: run, Answer: answer})
		s.Current = &rule.Entry{Run: run, Answer: answer}
		return s
	}
	differs := false
	for _, line := range lines {
		parts := strings.SplitN(line, "\t", 2)
		s := rule.State{}
		for _, ev := range strings.Fields(parts[0]) {
			run := runIDs[ev[1:2]]
			switch ev[0] {
			case 'c':
				s = rule.Cancel(s, run)
			case 'a':
				s = alwaysAdopt(s, run, ev[2:3])
			}
		}
		if showSlot(s) != parts[1] {
			differs = true
			break
		}
	}
	if !differs {
		t.Fatal("取消を確かめない規則でも、モデルとの違いが見つからなかった")
	}
}
