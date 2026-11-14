package main

import (
	"fmt"
	"path/filepath"
	"strings"

	preparedev "github.com/wim-web/prepare-dev"
	"github.com/wim-web/prepare-dev/internal/skills"

	"github.com/wim-web/prepare-dev/internal/policy"
	"github.com/wim-web/prepare-dev/internal/store"
)

// recheckItems は、関係する内容が変わって、今の内容に使えるか確かめ直す必要があるものを集める。
// 過去の結果は消さない。今の内容に使えるかを、別に判定する。
func recheckItems(s *store.Store, st *store.State) []map[string]any {
	items := []map[string]any{}
	add := func(id, kind, title string, reasons []string) {
		if len(reasons) > 0 {
			items = append(items, map[string]any{"id": id, "kind": kind, "title": title, "state": "要再確認", "reasons": reasons})
		}
	}
	for _, o := range st.OfKind("EVD") {
		add(o.ID, "証拠", o.Head().Title, evidenceCurrent(s, st, o.Head()))
	}
	for _, o := range st.OfKind("FIG") {
		add(o.ID, "図", o.Head().Title, figureStale(s, st, o, o.Head().N))
	}
	for _, o := range st.OfKind("FND") {
		h := o.Head()
		if l, ok := hasAbout(h); ok && h.Status == "open" {
			if t, ok := st.Objects[l.To]; ok && t.Head().N != l.Version {
				add(o.ID, "指摘", h.Title, []string{fmt.Sprintf("%s の版 %d への指摘。今は版 %d。残っているかは再レビューで確かめる", l.To, l.Version, t.Head().N)})
			}
		}
	}
	for _, o := range st.OfKind("RPL") {
		h := o.Head()
		if h.Status == "recorded" && str(h.Fields, "applicability") != "current" {
			add(o.ID, "返答", h.Title, strs(h.Fields, "applicability_reasons"))
		}
	}
	for slot, state := range st.Slots {
		if state.Current == nil {
			continue
		}
		r := st.Runs[state.Current.Run]
		for _, t := range r.Targets {
			if o, ok := st.Objects[t.ID]; ok && o.Head().N != t.Version {
				add(r.ID, "現在の結果", slot, []string{fmt.Sprintf("%s の版 %d を読んだ結果。今は版 %d", t.ID, t.Version, o.Head().N)})
			}
		}
	}
	return items
}

func cmdImpact(root string, args []string) (any, error) {
	if _, err := parse(flags("impact"), args, 0); err != nil {
		return nil, err
	}
	s, err := open(root)
	if err != nil {
		return nil, err
	}
	var items []map[string]any
	err = s.View(func(st *store.State) error {
		items = recheckItems(s, st)
		return nil
	})
	return map[string]any{"needs_recheck": items}, err
}

// cmdStatus は、中断したあとに戻るための全体像を返す。
func cmdStatus(root string, args []string) (any, error) {
	if _, err := parse(flags("status"), args, 0); err != nil {
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
	out := map[string]any{}
	err = s.View(func(st *store.State) error {
		var decided, proposed, waiting, replies, open, unmapped, adopted []map[string]any
		for _, id := range st.Order {
			o := st.Objects[id]
			switch h := o.Head(); {
			case o.Kind == "DEC" && (h.Status == "decided" || h.Status == "derived"):
				decided = append(decided, summary(o))
			case o.Kind == "Q" && h.Status == "open":
				waiting = append(waiting, summary(o))
			case o.Kind == "RPL" && (h.Status == "recorded" || h.Status == "unmapped"):
				replies = append(replies, summary(o))
			case o.Kind == "FND" && h.Status == "open":
				open = append(open, summary(o))
			case o.Kind == "FND" && h.Status == "unmapped":
				unmapped = append(unmapped, summary(o))
			case h.Status == "proposed":
				proposed = append(proposed, summary(o))
			case h.Status == "adopted":
				adopted = append(adopted, summary(o))
			}
		}
		var active, late, failed []map[string]any
		for _, id := range st.RunOrder {
			r := st.Runs[id]
			item := map[string]any{"id": r.ID, "role": r.Role, "state": r.State, "targets": r.Targets}
			switch {
			case r.Cancelled && len(r.Answers) > 0:
				item["note"] = "取消済みの実行から回答が届いている。履歴に保存し、現在の結果には入れていない"
				late = append(late, item)
			case r.Cancelled:
			case r.State == "approved" || r.State == "running":
				active = append(active, item)
			case r.State == "failed":
				item["reason"] = r.Fail.Reason
				failed = append(failed, item)
			}
		}
		var guarantees []guaranteeReport
		for _, o := range st.OfKind("GRT") {
			guarantees = append(guarantees, guaranteeState(s, st, cfg, o))
		}
		recheck := recheckItems(s, st)

		next := []string{}
		hint := func(n int, text string) {
			if n > 0 {
				next = append(next, fmt.Sprintf("%s（%d 件）", text, n))
			}
		}
		hint(len(unmapped)+countStatus(st, "RPL", "unmapped"), "どこへの指摘か決まっていないものを確かめる")
		hint(len(waiting), "利用者の判断を待っている。差が出る具体例を示して確認する。返事が来るまで、作る作業は進めない")
		hint(len(proposed), "AI の提案が採否待ち。利用者の決定か、既にある決定からの導出を記録する")
		hint(len(replies)-countStatus(st, "RPL", "unmapped"), "記録しただけで、まだ扱っていない返答がある")
		hint(len(open), "閉じていない指摘に対応する")
		hint(len(recheck), "内容が変わったので、確かめ直す")
		for _, g := range guarantees {
			if g.State != "fulfilled" {
				next = append(next, fmt.Sprintf("%s「%s」：%s", g.ID, g.Title, g.Summary))
			}
		}
		hint(len(active), "動いている、または起動を許可した実行がある")
		if len(st.Objects) == 0 {
			next = append(next, "記録はまだない。依頼を受けたら、作る前に、どう理解したかを HTML のページで利用者に見せる（prepare-dev の skill）")
		}
		if len(cfg.Roles) == 0 {
			next = append(next, "担当ごとのモデル・思考量・人数が未設定。要るのは、別の担当を使うと利用者が決めたときだけ。それまでは聞かない。未設定のあいだ、担当は起動しない")
		}
		next = append(next, skillHints(root)...)

		out = map[string]any{
			"decided": decided, "adopted": adopted, "proposed": proposed, "waiting_for_user": waiting,
			"replies_not_applied": replies,
			"findings":            map[string]any{"open": open, "unmapped": unmapped},
			"runs":                map[string]any{"active": active, "cancelled_with_late_answers": late, "failed": failed},
			"guarantees":          guarantees, "needs_recheck": recheck, "next": next,
		}
		return nil
	})
	return out, err
}

// skillHints は、入れてある skill が、この実行ファイルの版と違うときに、どうすればよいかを返す。
func skillHints(root string) []string {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	plan, err := skills.Plan(preparedev.Skills(), abs)
	if err != nil {
		return []string{"入れてある skill を、この版と比べられない（.ppdev/skills.json）: " + err.Error()}
	}
	if plan == nil {
		return nil
	}
	var out []string
	add := func(list []string, text string) {
		if len(list) > 0 {
			out = append(out, fmt.Sprintf(text, strings.Join(list, "、")))
		}
	}
	add(plan.Update, "この版より古い skill がある（%s）。ppdev init で新しくする")
	add(plan.Copy, "入っていない skill がある（%s）。ppdev init で入れる")
	add(plan.Leave, "中身がこの版と違う skill がある（%s）。編集したものか、init 以外で入れたもの。そのまま残る。置き換えるなら ppdev init --replace-skills")
	add(plan.Manual, "init が触らない場所に skill がある（%s）。リンクかファイル、またはプロジェクトの外。手で直す")
	return out
}

func countStatus(st *store.State, kind, status string) int {
	n := 0
	for _, o := range st.OfKind(kind) {
		if o.Head().Status == status {
			n++
		}
	}
	return n
}
