// Package policy は、担当を起動してよいかを、利用者の設定と照合して決める。
//
// 設定は、人が読み書きする「設定の表」（Markdown）に書く。一行が一つの役割で、
// モデルを並べた数が、同時に動かせる人数になる。
//
// 最初の担当も追加の担当も、その役割の設定でここを通す。
// 表にない役割、禁止した値、上限を超える要求は起動しない。
// 元の手順にある自動の引き上げや、別モデルへの切り替えは行わない。
package policy

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wim-web/prepare-dev/internal/store"
)

// InheritParent は、モデルを指定せず、親と同じモデルで起動するという設定。
const InheritParent = "inherit-parent"

// Seat は、ある役割の一人分の設定。
type Seat struct {
	Model  string `json:"model"`
	Effort string `json:"effort,omitempty"` // 空なら「通常の思考量」を使う
}

// Raise は、通常より上の思考量を使ってよい条件。
type Raise struct {
	Effort string `json:"effort"`
	Event  string `json:"event"`
}

// Viewpoint は、レビューで確かめる観点。
type Viewpoint struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// Settings は、設定の表の内容。
type Settings struct {
	Roles            map[string][]Seat `json:"roles"`
	DefaultEffort    string            `json:"default_effort"` // 空なら、親と同じ（セッションの値）
	EffortLevels     []string          `json:"effort_levels"`  // 思考量の段階。低いほうから
	EffortAuto       bool              `json:"effort_auto"`    // 仕事に合わせて、AI の判断で上げ下げしてよいか
	Raises           []Raise           `json:"raises,omitempty"`
	ForbiddenModels  []string          `json:"forbidden_models,omitempty"`
	ForbiddenEfforts []string          `json:"forbidden_efforts,omitempty"`
	Alternatives     []string          `json:"alternatives,omitempty"`
	MaxSpawns        int               `json:"max_spawns,omitempty"` // 作業全体で起動してよい担当の数。0 は上限なし
	Enforce          bool              `json:"enforce"`              // 実行環境で止めるか
	Viewpoints       []Viewpoint       `json:"viewpoints"`
	MaxRounds        int               `json:"max_rounds,omitempty"`
	VerifierTimeout  int               `json:"verifier_timeout"`
	Polish           string            `json:"polish,omitempty"` // 日本語の説明を整える skill
	Source           string            `json:"source"`           // 読んだ設定の表。なければ空
	Warnings         []string          `json:"warnings,omitempty"`
}

// Default は、設定の表がないときの設定。役割は空で、利用者が決めるまで担当は起動しない。
func Default() *Settings {
	return &Settings{
		Roles: map[string][]Seat{},
		Viewpoints: []Viewpoint{
			{"要求", "要求を落としていないか。AI が未決の条件を勝手に確定していないか。"},
			{"成功する動作", "成功してほしい動作も含むか。常に拒否するだけで成立する弱い条件になっていないか。"},
			{"意味の対応", "文章、具体例、図、設計案、形式条件の意味が、互いに合っているか。"},
			{"前提", "前提、対象外、外部の部品への信頼、実装との関係が妥当か。"},
			{"今の内容", "結果が、対象の今の内容に使えるか。"},
		},
		VerifierTimeout: 120,
		EffortLevels:    []string{"low", "medium", "high", "xhigh", "max"},
		EffortAuto:      true,
	}
}

// Request は、担当を起動したいという要求。
type Request struct {
	Role     string
	Model    string // 空なら役割の設定を使う
	Effort   string // 空なら通常の値を使う
	Reason   string // 思考量を通常の値から変える理由
	Event    string // 通常より上の思考量を使う根拠の出来事
	EventRun string // その出来事が起きた実行
	Targets  []store.Target
}

// Decision は、照合の結果。
type Decision struct {
	Allowed      bool          `json:"allowed"`
	Role         string        `json:"role"`
	Reasons      []string      `json:"reasons,omitempty"`
	Resolved     store.Setting `json:"resolved"`
	Enforce      string        `json:"enforce,omitempty"`
	Exception    string        `json:"exception,omitempty"`
	Alternatives []string      `json:"alternatives,omitempty"`
	Context      string        `json:"context,omitempty"`
	Note         string        `json:"note,omitempty"` // 思考量を通常の値から変えたときの、変え方と理由
}

// Check は要求を設定と照合する。状態は変えない。
func Check(st *store.State, cfg *Settings, req Request, now time.Time) Decision {
	role := RoleName(req.Role)
	roleLabel := RoleLabel(role)
	d := Decision{Role: role, Enforce: "instruction", Context: "continue", Alternatives: cfg.Alternatives}
	if cfg.Enforce {
		d.Enforce = "machine"
	}
	if strings.Contains(role, "review") {
		d.Context = "fresh" // レビューの確認役は、会話の経緯を持たない新しい文脈で動かす
	}
	seats := cfg.Roles[role]
	if len(seats) == 0 {
		d.Reasons = append(d.Reasons, fmt.Sprintf("役割「%s」の行が設定の表にない。利用者が設定するまで起動しない", roleLabel))
		return d
	}

	// 今動いている担当が使っている席を除き、空いている席を求める。
	free := slices.Clone(seats)
	active, total := 0, 0
	for _, id := range st.RunOrder {
		r := st.Runs[id]
		if r.State == "denied" {
			continue
		}
		total++
		if r.Role == role && !r.Cancelled && (r.State == "approved" || r.State == "running") {
			active++
			if i := slices.IndexFunc(free, func(s Seat) bool { return s.Model == r.Resolved.Model }); i >= 0 {
				free = slices.Delete(free, i, i+1)
			} else if len(free) > 0 {
				free = free[1:]
			}
		}
	}

	seat := seats[0]
	modelOK := true
	switch {
	case active >= len(seats):
		d.Reasons = append(d.Reasons, fmt.Sprintf("役割「%s」は同時に %d 人まで。今 %d 人が動いている", roleLabel, len(seats), active))
	case req.Model == "":
		seat = free[0]
	default:
		if i := slices.IndexFunc(free, func(s Seat) bool { return s.Model == req.Model }); i >= 0 {
			seat = free[i]
		} else {
			seat = free[0]
			modelOK = slices.Contains(cfg.Alternatives, req.Model)
			if slices.ContainsFunc(seats, func(s Seat) bool { return s.Model == req.Model }) {
				modelOK = false
				d.Reasons = append(d.Reasons, fmt.Sprintf("役割「%s」でモデル「%s」を使う席は、今すべて動いている", roleLabel, req.Model))
			}
		}
	}

	model := seat.Model
	if req.Model != "" {
		model = req.Model
	}
	base := seat.Effort
	if base == "" {
		base = cfg.DefaultEffort
	}
	if base == "" {
		base = InheritParent // 通常の思考量を書いていなければ、親と同じ値から始める
	}
	effort := req.Effort
	if effort == "" {
		effort = base
	}
	d.Resolved = store.Setting{Model: model, Effort: effort}

	if slices.Contains(cfg.ForbiddenModels, model) {
		modelOK = false
	}
	raise := slices.IndexFunc(cfg.Raises, func(r Raise) bool { return r.Effort == effort })
	forbidden := slices.Contains(cfg.ForbiddenEfforts, effort)

	// 仕事に合わせた上げ下げ。上げられるのは通常の値から一段まで。下げるのは何段でもよい。
	adjusted, tooHigh := false, false
	if effort != base && cfg.EffortAuto && !forbidden {
		bi, ei := slices.Index(cfg.EffortLevels, base), slices.Index(cfg.EffortLevels, effort)
		switch {
		case ei < 0:
		case base == InheritParent:
			adjusted = true
			d.Note = fmt.Sprintf("親と同じ値の代わりに「%s」を使う。何段の変更かは照合していない", effort)
		case bi < 0:
		case ei > bi+1:
			tooHigh = true
		case ei > bi:
			adjusted = true
			d.Note = fmt.Sprintf("通常の「%s」から一段上げて「%s」", base, effort)
		default:
			adjusted = true
			d.Note = fmt.Sprintf("通常の「%s」から下げて「%s」", base, effort)
		}
	}
	if adjusted {
		if strings.TrimSpace(req.Reason) == "" {
			d.Reasons = append(d.Reasons, fmt.Sprintf("思考量を通常の「%s」から「%s」に変える理由が要る", label(base), effort))
		}
		d.Note += "。AI の判断。理由：" + strings.TrimSpace(req.Reason)
	}
	effortInRange := effort == base || adjusted || (raise >= 0 && !forbidden)

	// 利用者が明示した例外があれば、その範囲に限って使う。
	if !modelOK || !effortInRange {
		if id := findException(st, role, req, model, effort, modelOK, effortInRange, now); id != "" {
			d.Exception = id
			modelOK, effortInRange = true, true
		}
	}
	if !modelOK && !slices.ContainsFunc(d.Reasons, func(s string) bool { return strings.Contains(s, "すべて動いている") }) {
		d.Reasons = append(d.Reasons, fmt.Sprintf("モデル「%s」は役割「%s」で許可されていない。許可されていない別のモデルへ自動では切り替えない", model, roleLabel))
	}
	switch {
	case effortInRange:
	case tooHigh:
		d.Reasons = append(d.Reasons, fmt.Sprintf("思考量を上げられるのは、通常の「%s」から一段まで。「%s」は使えない", base, effort))
	default:
		d.Reasons = append(d.Reasons, fmt.Sprintf("思考量「%s」は役割「%s」で許可されていない", effort, roleLabel))
	}
	if !adjusted {
		d.Note = ""
	}

	// 上げ下げを固定にしているとき、通常より上の思考量は、決めておいた出来事が記録にあるときだけ使える。
	if effortInRange && d.Exception == "" && effort != base && !adjusted {
		allowed := false
		for _, r := range cfg.Raises {
			if r.Effort == effort && r.Event == EventName(req.Event) {
				allowed = true
			}
		}
		switch {
		case req.Event == "" || !allowed:
			d.Reasons = append(d.Reasons, fmt.Sprintf("思考量を「%s」に変えてよい出来事が示されていない。AI の「難しい」という判断だけでは変えない", effort))
		case !eventRecorded(st, EventName(req.Event), req.EventRun):
			d.Reasons = append(d.Reasons, fmt.Sprintf("出来事「%s」が実行 %s の記録に見つからない", req.Event, req.EventRun))
		}
	}

	if cfg.MaxSpawns > 0 && total >= cfg.MaxSpawns {
		d.Reasons = append(d.Reasons, fmt.Sprintf("作業全体の起動数の上限 %d に達している", cfg.MaxSpawns))
	}

	d.Allowed = len(d.Reasons) == 0
	return d
}

func eventRecorded(st *store.State, event, runID string) bool {
	r, ok := st.Runs[runID]
	if !ok {
		return false
	}
	switch event {
	case "verifier-timeout":
		return r.Fail != nil && r.Fail.Reason == "timeout"
	case "attempt-failed":
		return r.State == "failed"
	case "attempt-finished":
		return r.State == "answered" || r.State == "failed"
	}
	return r.Fail != nil && r.Fail.Reason == event
}

// findException は、要求を覆う有効な例外を探す。
// 例外は利用者の決定に結び付いた記録（EXC）で、役割・値・有効な範囲を持つ。
func findException(st *store.State, role string, req Request, model, effort string, modelOK, effortOK bool, now time.Time) string {
	for _, o := range st.OfKind("EXC") {
		v := o.Head()
		if v.Status != "active" {
			continue
		}
		f := v.Fields
		if r, _ := f["role"].(string); r != "*" && RoleName(r) != role {
			continue
		}
		if !modelOK {
			if m, _ := f["model"].(string); m != model {
				continue
			}
		}
		if !effortOK {
			if e, _ := f["effort"].(string); e != effort {
				continue
			}
		}
		if until, _ := f["until"].(string); until != "" {
			t, err := time.Parse(time.RFC3339, until)
			if err != nil || now.After(t) {
				continue
			}
		}
		if target, _ := f["target"].(string); target != "" {
			if !slices.ContainsFunc(req.Targets, func(t store.Target) bool { return t.ID == target }) {
				continue
			}
		}
		if uses, ok := f["uses"].(float64); ok && uses > 0 {
			used := 0
			for _, r := range st.Runs {
				if r.Exception == o.ID && r.State != "denied" {
					used++
				}
			}
			if used >= int(uses) {
				continue
			}
		}
		return o.ID
	}
	return ""
}

// label は、思考量を人に見せるときの言葉にする。
func label(effort string) string {
	if effort == InheritParent {
		return "親と同じ"
	}
	return effort
}
