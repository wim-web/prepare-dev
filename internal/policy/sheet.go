package policy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/wim-web/prepare-dev/internal/store"
)

// 設定の表に書く役割の名前と、記録やコマンドで使う名前の対応。
// 表にはどちらで書いてもよい。ここにない名前は、そのまま役割の名前として使う。
var roleNames = [][2]string{
	{"調査", "research"},
	{"設計", "design"},
	{"設計の評価", "design-judge"},
	{"実装", "implement"},
	{"仕様レビュー", "spec-review"},
	{"実装レビュー", "code-review"},
	{"証明", "prove"},
	{"説明", "explain"},
}

// RoleName は、役割の名前を、記録やコマンドで使う名前にそろえる。
func RoleName(s string) string {
	s = strings.TrimSpace(s)
	for _, p := range roleNames {
		if s == p[0] {
			return p[1]
		}
	}
	return strings.ToLower(s)
}

// RoleLabel は、役割を人に見せるときの名前を返す。
func RoleLabel(name string) string {
	for _, p := range roleNames {
		if name == p[1] {
			return p[0]
		}
	}
	return name
}

var eventNames = map[string]string{
	"検証器の時間切れ": "verifier-timeout",
	"試行の失敗":    "attempt-failed",
	"試行の終了":    "attempt-finished",
}

// EventName は、出来事の名前を、記録で使う名前にそろえる。
func EventName(s string) string {
	s = strings.TrimSpace(s)
	if n, ok := eventNames[s]; ok {
		return n
	}
	return s
}

var globalKeys = map[string]string{
	"通常の思考量": "default-effort", "default effort": "default-effort",
	"思考量の引き上げ": "raise", "effort raise": "raise",
	"思考量の調整": "effort-auto", "effort adjust": "effort-auto",
	"思考量の段階": "effort-levels", "effort levels": "effort-levels",
	"禁止するモデル": "forbidden-models", "forbidden models": "forbidden-models",
	"禁止する思考量": "forbidden-efforts", "forbidden efforts": "forbidden-efforts",
	"代わりに使ってよいモデル": "alternatives", "alternatives": "alternatives",
	"起動数の上限": "max-spawns", "max spawns": "max-spawns",
	"実行環境で止める": "enforce", "enforce": "enforce",
	"検証器の制限時間": "verifier-timeout", "verifier timeout": "verifier-timeout",
	"日本語を整える skill": "polish", "japanese polish": "polish",
	"レビューの回数の上限": "max-rounds", "review max rounds": "max-rounds",
	"session hook": "ignore", // pstack の表から写した行。prepare-dev では使わない
}

var (
	seatRe  = regexp.MustCompile(`^([^\s,@<>]+)(?:\s*@\s*([^\s,@<>]+))?$`)
	raiseRe = regexp.MustCompile(`^([^\s（(]+)\s*(?:[（(]\s*(.+?)\s*[）)]|when\s+(.+))$`)
)

func cutColon(s string) (string, string, bool) {
	i := strings.IndexAny(s, ":：")
	if i < 0 {
		return "", "", false
	}
	_, size := firstRune(s[i:])
	return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+size:]), true
}

func firstRune(s string) (rune, int) {
	for _, r := range s {
		return r, len(string(r))
	}
	return 0, 0
}

func list(value string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '、' }) {
		if p = strings.TrimSpace(p); p != "" && !none(p) {
			out = append(out, p)
		}
	}
	return out
}

func none(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "なし", "none", "-":
		return true
	}
	return false
}

// Parse は設定の表を読む。
//
// 読めない行は捨てずに Warnings に残す。黙って無視すると、書いたつもりの設定が
// 効いていないことに気付けないため。
func Parse(text string) *Settings {
	s := Default()
	warn := func(n int, format string, a ...any) {
		s.Warnings = append(s.Warnings, fmt.Sprintf("%d 行目：", n)+fmt.Sprintf(format, a...))
	}
	inViewpoints, ownViewpoints := false, false
	for i, raw := range strings.Split(text, "\n") {
		n := i + 1
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "#"):
			inViewpoints = strings.Contains(line, "観点") || strings.Contains(strings.ToLower(line), "viewpoint")
			continue
		case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "* "):
			if !inViewpoints {
				continue // 説明の箇条書き
			}
			key, text, ok := cutColon(line[2:])
			if !ok || key == "" || text == "" {
				warn(n, "観点は「名前: 確かめること」の形で書く")
				continue
			}
			if !ownViewpoints {
				s.Viewpoints, ownViewpoints = nil, true
			}
			s.Viewpoints = append(s.Viewpoints, Viewpoint{Key: key, Text: text})
			continue
		case strings.HasPrefix(line, ">"), strings.HasPrefix(line, "<!--"), strings.HasPrefix(line, "|"), strings.HasPrefix(line, "```"):
			continue
		}
		key, value, ok := cutColon(line)
		if !ok || len([]rune(key)) > 40 || strings.ContainsAny(key, "。.") {
			continue // 説明の文
		}
		norm := strings.ToLower(strings.Join(strings.Fields(key), " "))
		if g, ok := globalKeys[norm]; ok {
			s.setGlobal(g, key, value, func(format string, a ...any) { warn(n, format, a...) })
			continue
		}

		// 役割の行。モデルを並べた数が、同時に動かせる人数になる。
		var seats []Seat
		bad := ""
		for _, part := range strings.Split(value, ",") {
			m := seatRe.FindStringSubmatch(strings.TrimSpace(part))
			if m == nil {
				bad = strings.TrimSpace(part)
				break
			}
			model := m[1]
			switch strings.ToLower(model) {
			case "親と同じ", "auto", InheritParent:
				model = InheritParent
			}
			seats = append(seats, Seat{Model: model, Effort: effortValue(m[2])})
		}
		if bad != "" || len(seats) == 0 {
			warn(n, "「%s」の値が読めない（%q）。「モデル」か「モデル @思考量」をカンマで並べる", key, bad)
			continue
		}
		for _, role := range list(key) {
			name := RoleName(role)
			if _, dup := s.Roles[name]; dup {
				warn(n, "役割「%s」の行が二つある。あとの行を使う", RoleLabel(name))
			}
			s.Roles[name] = seats
		}
	}
	for role, seats := range s.Roles {
		for _, seat := range seats {
			if slices.Contains(s.ForbiddenModels, seat.Model) {
				s.Warnings = append(s.Warnings, fmt.Sprintf("役割「%s」に、禁止するモデル「%s」が書いてある。この席は使えない", RoleLabel(role), seat.Model))
			}
			if seat.Effort != "" && slices.Contains(s.ForbiddenEfforts, seat.Effort) {
				s.Warnings = append(s.Warnings, fmt.Sprintf("役割「%s」に、禁止する思考量「%s」が書いてある。この席は使えない", RoleLabel(role), seat.Effort))
			}
		}
	}
	slices.Sort(s.Warnings)
	return s
}

func (s *Settings) setGlobal(key, label, value string, warn func(string, ...any)) {
	number := func(dst *int) {
		if none(value) {
			*dst = 0
			return
		}
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			warn("「%s」は数で書く（%q）", label, value)
			return
		}
		*dst = n
	}
	switch key {
	case "default-effort":
		if strings.ContainsAny(value, " ,<>") {
			warn("「%s」は一つの値で書く（%q）", label, value)
			return
		}
		s.DefaultEffort = effortValue(value)
	case "effort-auto":
		switch strings.ToLower(value) {
		case "自動", "auto", "on":
			s.EffortAuto = true
		case "固定", "fixed", "off":
			s.EffortAuto = false
		default:
			warn("「%s」は「自動」か「固定」で書く（%q）", label, value)
		}
	case "effort-levels":
		if levels := list(value); len(levels) > 0 {
			s.EffortLevels = levels
		}
	case "raise":
		s.Raises = nil
		for _, part := range list(value) {
			m := raiseRe.FindStringSubmatch(part)
			if m == nil {
				warn("「%s」は「xhigh（検証器の時間切れ）」の形で書く（%q）", label, part)
				continue
			}
			s.Raises = append(s.Raises, Raise{Effort: m[1], Event: EventName(m[2] + m[3])})
		}
	case "forbidden-models":
		s.ForbiddenModels = list(value)
	case "forbidden-efforts":
		s.ForbiddenEfforts = list(value)
	case "alternatives":
		s.Alternatives = list(value)
	case "max-spawns":
		number(&s.MaxSpawns)
	case "verifier-timeout":
		number(&s.VerifierTimeout)
	case "max-rounds":
		number(&s.MaxRounds)
	case "enforce":
		switch strings.ToLower(value) {
		case "on", "はい", "する", "true":
			s.Enforce = true
		case "off", "いいえ", "しない", "false":
			s.Enforce = false
		default:
			warn("「%s」は on か off で書く（%q）", label, value)
		}
	case "polish":
		s.Polish = ""
		if !none(value) {
			s.Polish = value
		}
	}
}

// effortValue は、思考量の値をそろえる。「親と同じ」は、思考量を指定せずに起動するという意味。
func effortValue(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "親と同じ", "session", InheritParent:
		return InheritParent
	}
	return strings.TrimSpace(s)
}

// Path は、このプロジェクトだけの設定の表の場所。
func Path(root string) string { return filepath.Join(root, store.DirName, "models.md") }

// UserPaths は、どのプロジェクトでも使う設定の表の場所の候補。先にあるものを使う。
// 環境変数 PPDEV_USER_MODELS で場所を指定できる。off にすると探さない。
func UserPaths() []string {
	switch v := os.Getenv("PPDEV_USER_MODELS"); v {
	case "":
	case "off":
		return nil
	default:
		return []string{v}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(home, ".codex", "prepare-dev-models.md"),
		filepath.Join(home, ".claude", "prepare-dev-models.md"),
	}
}

// Load は設定の表を読む。
// このプロジェクトの表があればそれを使い、なければ、どのプロジェクトでも使う表を使う。
// どちらもなければ、役割が空の設定を返す。
func Load(root string) (*Settings, error) {
	for _, p := range append([]string{Path(root)}, UserPaths()...) {
		b, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("設定の表が読めない: %w", err)
		}
		s := Parse(string(b))
		s.Source = p
		return s, nil
	}
	return Default(), nil
}

// Template は、設定の表のひな形。＜＞の部分を書き換えて使う。
const Template = `# prepare-dev の担当の設定

- 一行が一つの役割。行のない役割は起動しない。
- モデルをカンマで並べると、並べた数が、同時に動かせる人数になる。
- 思考量は、モデルの後ろに @high のように付ける。付けなければ「通常の思考量」を使う。
- モデルや思考量の代わりに「親と同じ」と書くと、その値を指定せずに起動する。
- 思考量は、ここに書いた値が出発点。いちばん難しい作業では一段上げ、判断の作業では一段、定型の作業では二段以上下げる。
- モデルは、ここに書いたものだけを使う。別のモデルへ自動では切り替えない。

調査: <モデル>
設計: <モデル>, <モデル>
設計の評価: <モデル>
実装: <モデル>
仕様レビュー: <モデル>, <モデル>
実装レビュー: <モデル>, <モデル>
証明: <モデル> @<思考量>

通常の思考量: <思考量>

- ここから下は、要るものだけ残す。消した行は「なし」として扱う。

思考量の調整: 自動
禁止する思考量: <思考量>
禁止するモデル: なし
代わりに使ってよいモデル: なし
起動数の上限: なし
実行環境で止める: off
検証器の制限時間: 120
レビューの回数の上限: 3
日本語を整える skill: なし

## レビューで必ず確かめる観点

- 要求: 要求を落としていないか。AI が未決の条件を勝手に確定していないか。
- 成功する動作: 成功してほしい動作も含むか。常に拒否するだけで成立する弱い条件になっていないか。
- 意味の対応: 文章、具体例、図、設計案、形式条件の意味が、互いに合っているか。
- 前提: 前提、対象外、外部の部品への信頼、実装との関係が妥当か。
- 今の内容: 結果が、対象の今の内容に使えるか。
`
