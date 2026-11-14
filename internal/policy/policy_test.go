package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wim-web/prepare-dev/internal/store"
)

const sheet = `# 担当の設定

- 説明の行は読み飛ばす。例: こう書いても役割にはならない。

設計: model-a, model-b
実装, 証明: model-a @xhigh
spec-review: model-r, model-r, model-r
調査: 親と同じ

通常の思考量: high
思考量の引き上げ: xhigh（検証器の時間切れ）
禁止する思考量: max
代わりに使ってよいモデル: model-c
起動数の上限: なし
実行環境で止める: on
session hook: on
`

func settings() *Settings { return Parse(sheet) }

// 状態を作るために、本物の記録を一時的な場所に作る。
func state(t *testing.T, build ...store.Build) *store.State {
	t.Helper()
	s, err := store.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range build {
		if _, _, err := s.Update("", b); err != nil {
			t.Fatal(err)
		}
	}
	var out *store.State
	s.View(func(st *store.State) error { out = st; return nil })
	return out
}

func newRun(r store.Run) store.Build {
	return func(st *store.State) ([]store.Op, func(*store.State) any, error) {
		r.ID = st.Reserve("RUN")
		return []store.Op{{Type: "run.new", Run: &r}}, nil, nil
	}
}

func wantDenied(t *testing.T, d Decision, part string) {
	t.Helper()
	if d.Allowed {
		t.Fatalf("許可された: %+v", d)
	}
	if !strings.Contains(strings.Join(d.Reasons, "\n"), part) {
		t.Fatalf("理由に %q がない: %v", part, d.Reasons)
	}
}

var now = time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)

// 設定の表の読み方：一行が一つの役割、並べた数が人数、@ で思考量。
func TestParseSheet(t *testing.T) {
	cfg := settings()
	if len(cfg.Warnings) != 0 {
		t.Fatalf("読めない行がある: %v", cfg.Warnings)
	}
	if got := cfg.Roles["design"]; len(got) != 2 || got[0].Model != "model-a" || got[1].Model != "model-b" {
		t.Fatalf("設計: %+v", got)
	}
	if got := cfg.Roles["spec-review"]; len(got) != 3 {
		t.Fatalf("仕様レビューの人数: %d", len(got))
	}
	for _, role := range []string{"implement", "prove"} { // 一行に役割を二つ並べた
		if got := cfg.Roles[role]; len(got) != 1 || got[0].Effort != "xhigh" {
			t.Fatalf("%s: %+v", role, got)
		}
	}
	if got := cfg.Roles["research"]; got[0].Model != InheritParent {
		t.Fatalf("調査: %+v", got)
	}
	if cfg.DefaultEffort != "high" || !cfg.Enforce || cfg.MaxSpawns != 0 {
		t.Fatalf("全体の設定: %+v", cfg)
	}
	if len(cfg.Raises) != 1 || cfg.Raises[0] != (Raise{"xhigh", "verifier-timeout"}) {
		t.Fatalf("引き上げ: %+v", cfg.Raises)
	}
	if !cfg.EffortAuto || len(cfg.EffortLevels) != 5 {
		t.Fatalf("思考量は、書かなければ仕事に合わせて上げ下げする: %+v", cfg)
	}
	fixed := Parse("設計: model-a @session\n通常の思考量: 親と同じ\n思考量の調整: 固定\n思考量の段階: low, high\n")
	if fixed.EffortAuto || fixed.DefaultEffort != InheritParent || fixed.Roles["design"][0].Effort != InheritParent || len(fixed.EffortLevels) != 2 {
		t.Fatalf("固定と、親と同じ: %+v", fixed)
	}
	if len(cfg.Viewpoints) != 5 {
		t.Fatalf("観点を書かなければ、既定の五つを使う: %d", len(cfg.Viewpoints))
	}
	if _, ok := cfg.Roles["session hook"]; ok {
		t.Fatal("pstack の表から写した行が、役割として読まれた")
	}
}

// 読めない行は、黙って捨てずに知らせる。ひな形のままの行は設定にならない。
func TestUnreadableLinesAreReported(t *testing.T) {
	cfg := Parse(Template)
	if len(cfg.Roles) != 0 {
		t.Fatalf("書き換えていないひな形から役割ができた: %v", cfg.Roles)
	}
	if len(cfg.Warnings) == 0 {
		t.Fatal("書き換えていないひな形なのに、何も知らせない")
	}
	cfg = Parse("設計: model a\n起動数の上限: たくさん\n\n## 観点\n\n- 名前だけ\n- 意味: 意味が合うか\n")
	if len(cfg.Warnings) != 3 || len(cfg.Roles) != 0 {
		t.Fatalf("知らせた行: %v / 役割: %v", cfg.Warnings, cfg.Roles)
	}
	if len(cfg.Viewpoints) != 1 || cfg.Viewpoints[0].Key != "意味" {
		t.Fatalf("観点: %+v", cfg.Viewpoints)
	}
}

func TestRoleWithoutLineIsNotStarted(t *testing.T) {
	wantDenied(t, Check(state(t), settings(), Request{Role: "実装レビュー"}, now), "設定の表にない")
	// 通常の思考量を書いていなければ、親と同じ値で起動する。
	if d := Check(state(t), Parse("設計: model-a\n"), Request{Role: "設計"}, now); !d.Allowed || d.Resolved.Effort != InheritParent {
		t.Fatalf("通常の思考量がないとき: %+v", d)
	}
}

// 役割は、日本語の名前でも、記録で使う名前でも指定できる。
func TestDefaultsAreUsed(t *testing.T) {
	for _, role := range []string{"設計", "design"} {
		d := Check(state(t), settings(), Request{Role: role}, now)
		if !d.Allowed || d.Role != "design" || d.Resolved != (store.Setting{Model: "model-a", Effort: "high"}) || d.Enforce != "machine" {
			t.Fatalf("%s: %+v", role, d)
		}
	}
	// 席に書いた思考量は、通常の思考量より優先する。
	if d := Check(state(t), settings(), Request{Role: "証明"}, now); !d.Allowed || d.Resolved.Effort != "xhigh" {
		t.Fatalf("証明: %+v", d)
	}
	// レビューの確認役は、新しい文脈で動かす。
	if d := Check(state(t), settings(), Request{Role: "仕様レビュー"}, now); d.Context != "fresh" {
		t.Fatalf("仕様レビュー: %+v", d)
	}
	if d := Check(state(t), settings(), Request{Role: "調査"}, now); !d.Allowed || d.Resolved.Model != InheritParent {
		t.Fatalf("調査: %+v", d)
	}
}

// 並べたモデルの数が人数。空いている席を順に使い、埋まったら止まる。
func TestSeatsAreThePeople(t *testing.T) {
	cfg := settings()
	st := state(t, newRun(store.Run{Role: "design", State: "running", Resolved: store.Setting{Model: "model-a"}}))
	d := Check(st, cfg, Request{Role: "設計"}, now)
	if !d.Allowed || d.Resolved.Model != "model-b" {
		t.Fatalf("二人目は、空いている席のモデルを使うはず: %+v", d)
	}
	wantDenied(t, Check(st, cfg, Request{Role: "設計", Model: "model-a"}, now), "今すべて動いている")

	st = state(t,
		newRun(store.Run{Role: "design", State: "running", Resolved: store.Setting{Model: "model-a"}}),
		newRun(store.Run{Role: "design", State: "approved", Resolved: store.Setting{Model: "model-b"}}),
	)
	wantDenied(t, Check(st, cfg, Request{Role: "設計"}, now), "同時に 2 人まで")
}

// 思考量は、表の値が出発点。仕事に合わせて、いちばん難しい作業では一段上げ、
// 判断の作業では一段、定型の作業では二段以上下げる。
func TestEffortFollowsTheJob(t *testing.T) {
	cfg := Parse("設計: model-a\n証明: model-a @xhigh\n調査: model-a @親と同じ\n通常の思考量: medium\n禁止する思考量: max\n")
	check := func(role, effort, reason string) Decision {
		return Check(state(t), cfg, Request{Role: role, Effort: effort, Reason: reason}, now)
	}

	up := check("設計", "high", "順番の入れ替わりを含む、取り違えやすい設計")
	if !up.Allowed || up.Resolved.Effort != "high" || !strings.Contains(up.Note, "一段上げて") || !strings.Contains(up.Note, "取り違えやすい") {
		t.Fatalf("一段上げる: %+v", up)
	}
	down := check("設計", "low", "決まった形の書き換え")
	if !down.Allowed || !strings.Contains(down.Note, "下げて") {
		t.Fatalf("下げる: %+v", down)
	}
	if d := check("証明", "medium", "定型の補題"); !d.Allowed { // 席の値（xhigh）から二段下げる
		t.Fatalf("二段下げる: %+v", d)
	}

	wantDenied(t, check("設計", "high", ""), "理由が要る")
	wantDenied(t, check("設計", "xhigh", "とても難しい"), "一段まで")     // 二段は上げない
	wantDenied(t, check("証明", "max", "とても難しい"), "許可されていない")   // 禁止した値は、一段上でも使わない
	wantDenied(t, check("設計", "ultra", "とても難しい"), "許可されていない") // 段階にない値

	// 出発点が親と同じ値のときは、何段の変更かを照合できない。そう記録して通す。
	if d := check("調査", "high", "読む量が多い"); !d.Allowed || !strings.Contains(d.Note, "照合していない") {
		t.Fatalf("親と同じ値から変える: %+v", d)
	}
	// 変えなければ、理由は要らない。
	if d := check("設計", "", ""); !d.Allowed || d.Note != "" || d.Resolved.Effort != "medium" {
		t.Fatalf("通常の値: %+v", d)
	}
}

// 上げ下げを固定にした表：「通常 high、条件を満たしたときだけ xhigh、max は禁止」。
func TestFixedEffortRaiseNeedsRecordedEvent(t *testing.T) {
	cfg := Parse(sheet + "思考量の調整: 固定\n")

	// AI の判断だけでは引き上げない。
	wantDenied(t, Check(state(t), cfg, Request{Role: "設計", Effort: "xhigh"}, now), "出来事が示されていない")

	// 出来事が記録にないなら引き上げない。
	st := state(t, newRun(store.Run{Role: "verify", State: "running"}))
	wantDenied(t, Check(st, cfg, Request{Role: "設計", Effort: "xhigh", Event: "検証器の時間切れ", EventRun: "RUN-0001"}, now), "記録に見つからない")

	// 検証器の時間切れが記録にあれば、決めておいた値まで引き上げられる。
	st = state(t,
		newRun(store.Run{Role: "verify", State: "running"}),
		func(*store.State) ([]store.Op, func(*store.State) any, error) {
			return []store.Op{{Type: "run.fail", ID: "RUN-0001", Fail: &store.Failure{Reason: "timeout"}}}, nil, nil
		},
	)
	for _, event := range []string{"検証器の時間切れ", "verifier-timeout"} {
		d := Check(st, cfg, Request{Role: "設計", Effort: "xhigh", Event: event, EventRun: "RUN-0001"}, now)
		if !d.Allowed || d.Resolved.Effort != "xhigh" {
			t.Fatalf("条件を満たしたのに許可されない（%s）: %+v", event, d)
		}
	}

	// 条件を満たしても、禁止した値は使えない。
	wantDenied(t, Check(st, cfg, Request{Role: "設計", Effort: "max", Event: "検証器の時間切れ", EventRun: "RUN-0001"}, now), "許可されていない")
}

// 表にない別のモデルへは切り替えない。代わりに使ってよいと書いたモデルだけ通る。
func TestNoSilentModelFallback(t *testing.T) {
	wantDenied(t, Check(state(t), settings(), Request{Role: "設計", Model: "parent-model"}, now), "自動では切り替えない")
	if d := Check(state(t), settings(), Request{Role: "設計", Model: "model-c"}, now); !d.Allowed || d.Resolved.Model != "model-c" {
		t.Fatalf("代わりに使ってよいモデル: %+v", d)
	}
}

func TestTotalBudget(t *testing.T) {
	cfg := Parse(sheet + "起動数の上限: 1\n")
	st := state(t,
		newRun(store.Run{Role: "verify", State: "running"}),
		func(*store.State) ([]store.Op, func(*store.State) any, error) {
			return []store.Op{{Type: "run.answer", ID: "RUN-0001", Answer: &store.RunAnswer{Hash: "x"}}}, nil, nil
		},
	)
	wantDenied(t, Check(st, cfg, Request{Role: "設計"}, now), "上限 1 に達している")
}

// 利用者が明示した例外は、その範囲に限って使える。全体の設定は変わらない。
func TestExplicitExceptionIsScoped(t *testing.T) {
	exception := func(st *store.State) ([]store.Op, func(*store.State) any, error) {
		return []store.Op{
			{Type: "put", ID: st.Reserve("DEC"), Kind: "DEC", Title: "今回は max で",
				Origin: &store.Origin{Category: store.CatUser, Source: "発言「今回は max で」"}},
			{Type: "put", ID: st.Reserve("EXC"), Kind: "EXC", Title: "設計担当を一度だけ max で",
				Origin: &store.Origin{Category: store.CatAI},
				Links:  []store.Link{{Rel: "decided-by", To: "DEC-0001"}},
				Fields: map[string]any{"role": "design", "effort": "max", "uses": 1}},
		}, nil, nil
	}
	cfg := settings()

	st := state(t, exception)
	d := Check(st, cfg, Request{Role: "設計", Effort: "max"}, now)
	if !d.Allowed || d.Exception != "EXC-0001" {
		t.Fatalf("例外が使えない: %+v", d)
	}
	// 別の役割には及ばない。
	wantDenied(t, Check(st, cfg, Request{Role: "証明", Effort: "max"}, now), "許可されていない")

	// 一度使ったら、次は使えない。
	st = state(t, exception, newRun(store.Run{Role: "design", State: "approved", Exception: "EXC-0001"}))
	wantDenied(t, Check(st, cfg, Request{Role: "設計", Effort: "max"}, now), "許可されていない")
}

// 利用者の決定に結び付かない例外は作れない。
func TestExceptionNeedsUserDecision(t *testing.T) {
	s, _ := store.Init(t.TempDir())
	_, _, err := s.Update("", func(st *store.State) ([]store.Op, func(*store.State) any, error) {
		return []store.Op{{Type: "put", ID: st.Reserve("EXC"), Kind: "EXC", Title: "max を許す",
			Origin: &store.Origin{Category: store.CatAI}, Fields: map[string]any{"role": "*", "effort": "max"}}}, nil, nil
	})
	if err == nil {
		t.Fatal("決定なしで例外が作れた")
	}
}

// 設定の表は、このプロジェクトのものがあればそれを、なければ、どのプロジェクトでも使うものを読む。
func TestLoadPrefersProjectSheet(t *testing.T) {
	root, user := t.TempDir(), filepath.Join(t.TempDir(), "prepare-dev-models.md")
	t.Setenv("PPDEV_USER_MODELS", user)

	cfg, err := Load(root)
	if err != nil || cfg.Source != "" || len(cfg.Roles) != 0 {
		t.Fatalf("表がないとき: %+v %v", cfg, err)
	}
	os.WriteFile(user, []byte("設計: user-model\n通常の思考量: high\n"), 0o644)
	if cfg, _ = Load(root); cfg.Source != user || cfg.Roles["design"][0].Model != "user-model" {
		t.Fatalf("どのプロジェクトでも使う表: %+v", cfg)
	}
	os.MkdirAll(filepath.Join(root, store.DirName), 0o755)
	os.WriteFile(Path(root), []byte("設計: project-model\n通常の思考量: high\n"), 0o644)
	if cfg, _ = Load(root); cfg.Source != Path(root) || cfg.Roles["design"][0].Model != "project-model" {
		t.Fatalf("このプロジェクトの表: %+v", cfg)
	}
}
