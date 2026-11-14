package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/wim-web/prepare-dev/internal/policy"
	"github.com/wim-web/prepare-dev/internal/store"
)

var originNames = map[string]string{
	store.CatUser: "利用者の発言", store.CatAI: "AI の提案", store.CatObserved: "観測した事実", store.CatDerived: "既にある決定からの導出",
}

// cmdReviewBundle は、記録の保存内容からレビュー用の文書を作る。
// 見出しに記録の番号と版を入れるので、行番号から、どの記録のどの版への指摘かが分かる。
func cmdReviewBundle(root string, args []string) (any, error) {
	fs := flags("review bundle")
	var targets multi
	fs.Var(&targets, "target", "レビューする記録 番号[@版]（繰り返せる）")
	out := fs.String("out", "", "書き出す場所。省くと .ppdev/reviews/ に置く")
	if _, err := parse(fs, args, 0); err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, store.Errf("invalid", "レビューする記録を --target で渡す")
	}
	s, err := open(root)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	var resolved []store.Target
	err = s.View(func(st *store.State) error {
		ts, err := parseTargets(st, targets)
		if err != nil {
			return err
		}
		resolved = ts
		b.WriteString("# レビュー文書\n\n")
		b.WriteString("この文書は、記録の保存内容から作った。指摘には、この文書の行番号を添える。\n")
		b.WriteString("見出しの「番号@版」が、どの記録のどの版かを表す。\n")
		for _, t := range ts {
			o := st.Objects[t.ID]
			v := o.At(t.Version)
			fmt.Fprintf(&b, "\n## %s@%d %s\n\n", t.ID, t.Version, v.Title)
			fmt.Fprintf(&b, "- 種類：%s\n- 状態：%s\n- 来歴：%s", store.Kinds[o.Kind], v.Status, originNames[v.Origin.Category])
			if v.Origin.Source != "" {
				fmt.Fprintf(&b, "（%s）", v.Origin.Source)
			}
			b.WriteString("\n")
			for _, l := range v.Links {
				if l.Version > 0 {
					fmt.Fprintf(&b, "- つながり：%s → %s@%d\n", l.Rel, l.To, l.Version)
				} else {
					fmt.Fprintf(&b, "- つながり：%s → %s\n", l.Rel, l.To)
				}
			}
			if v.Body != "" {
				b.WriteString("\n" + strings.TrimRight(demote(v.Body), "\n") + "\n")
			}
			if len(v.Fields) > 0 {
				j, _ := json.MarshalIndent(v.Fields, "", "  ")
				b.WriteString("\n項目：\n\n```json\n" + string(j) + "\n```\n")
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	data := []byte(b.String())
	hash, err := s.PutBlob(data)
	if err != nil {
		return nil, err
	}
	path := *out
	if path == "" {
		path = filepath.Join(store.DirName, "reviews", "bundle-"+hash[:12]+".md")
	}
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(root, path)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		return nil, err
	}
	return map[string]any{"bundle": hash, "path": path, "targets": resolved,
		"next": "新しい文脈の確認役にこの文書を渡す。指摘は ppdev review finding に、bundle とこの文書の行番号を添えて記録する"}, nil
}

// demote は、本文の中の見出しが、記録の区切り（##）と紛れないようにする。
func demote(body string) string {
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "#") {
			lines[i] = "##" + l
		}
	}
	return strings.Join(lines, "\n")
}

var sectionRe = regexp.MustCompile(`^## ([A-Z]+-\d{4})@(\d+) `)

// locate は、レビュー文書の行の範囲から、どの記録のどの版への指摘かを求める。
// 一つに決まらなければ、理由を返す。
func locate(bundle []byte, from, to int) (store.Target, string) {
	lines := strings.Split(string(bundle), "\n")
	if from < 1 || to < from || to > len(lines) {
		return store.Target{}, fmt.Sprintf("行 %d-%d は、この文書（%d 行）の範囲にない", from, to, len(lines))
	}
	var found *store.Target
	for i := 0; i < to; i++ {
		m := sectionRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		if i+1 > from {
			return store.Target{}, "指摘の範囲が、複数の記録にまたがっている"
		}
		n, _ := strconv.Atoi(m[2])
		found = &store.Target{ID: m[1], Version: n}
	}
	if found == nil {
		return store.Target{}, "指摘の行は、どの記録の区切りよりも前にある"
	}
	return *found, ""
}

func cmdReviewFinding(root string, args []string) (any, error) {
	fs := flags("review finding")
	run := fs.String("run", "", "指摘を出したレビューの実行（必須）")
	bundle := fs.String("bundle", "", "読んだレビュー文書の印（review bundle が返した値）")
	lineArg := fs.String("line", "", "指摘の行。一行なら 25、範囲なら 25-31")
	title := fs.String("title", "", "指摘の題")
	viewpoint := fs.String("viewpoint", "", "どの観点からの指摘か")
	problem := fs.String("problem", "", "何が問題か")
	evidence := fs.String("evidence", "", "根拠")
	example := fs.String("example", "", "差が出る具体例")
	impact := fs.String("impact", "", "影響")
	fix := fs.String("fix", "", "修正案")
	minor := fs.Bool("minor", false, "確認役が、受入条件の成否に影響しないと判断した指摘")
	reqID := fs.String("request-id", "", "同じ指摘を二重に記録しないための番号")
	if _, err := parse(fs, args, 0); err != nil {
		return nil, err
	}
	if *problem == "" {
		return nil, store.Errf("invalid", "何が問題か（--problem）が要る")
	}
	s, err := open(root)
	if err != nil {
		return nil, err
	}

	fields := map[string]any{"run": *run, "bundle": *bundle, "lines": *lineArg, "viewpoint": *viewpoint,
		"problem": *problem, "evidence": *evidence, "example": *example, "impact": *impact, "fix": *fix,
		"affects_acceptance": !*minor, "mapped": false}
	var links []store.Link
	reason := ""
	data, err := s.GetBlob(*bundle)
	switch {
	case err != nil:
		reason = "読んだレビュー文書を、保存した内容から特定できない"
	default:
		a, b, isRange := strings.Cut(*lineArg, "-")
		from, err1 := strconv.Atoi(a)
		to := from
		var err2 error
		if isRange {
			to, err2 = strconv.Atoi(b)
		}
		if err1 != nil || err2 != nil {
			reason = "行番号が読めない"
			break
		}
		t, why := locate(data, from, to)
		if why != "" {
			reason = why
			break
		}
		fields["mapped"] = true
		fields["target"] = t.String()
		links = append(links, store.Link{Rel: "about", To: t.ID, Version: t.Version})
	}
	if reason != "" {
		fields["unmapped_reason"] = reason
	}
	links = append(links, store.Link{Rel: "raised-by", To: *run})

	res, _, err := s.Update(*reqID, func(st *store.State) ([]store.Op, func(*store.State) any, error) {
		id := st.Reserve("FND")
		return []store.Op{{Type: "put", ID: id, Kind: "FND", Title: *title, Body: *problem, Fields: fields, Links: links,
			Origin: &store.Origin{Category: store.CatAI, By: *run}}}, func(st *store.State) any {
			out := map[string]any{"id": id, "status": st.Objects[id].Head().Status, "mapped": fields["mapped"]}
			if reason != "" {
				out["unmapped_reason"] = reason
				out["next"] = "どの記録への指摘か決まっていない。推測で別の記録に付けない。確認役に場所を確かめるか、レビューし直す"
			} else {
				out["target"] = fields["target"]
			}
			return out
		}, nil
	})
	return res, err
}

// reviewReport は、ある記録のレビューがどこまで済んだか。
type reviewReport struct {
	Target      string   `json:"target"`
	Complete    bool     `json:"complete"`
	Covered     []string `json:"covered_viewpoints"`
	Missing     []string `json:"missing_viewpoints,omitempty"`
	Open        []string `json:"open_findings,omitempty"`
	Unmapped    []string `json:"unmapped_findings,omitempty"`
	NotCounted  []string `json:"runs_not_counted,omitempty"`
	Explanation string   `json:"explanation"`
}

// reviewState は、記録の最新の版について、レビューが完了しているかを判定する。
//
// 完了は、必要と決めた観点をすべて、新しい文脈の実行が最後まで確かめ、
// 受入条件に影響しうる指摘が残っていない状態。上限で止まった・取り消した・
// 古い版を読んだ実行は数えない。
func reviewState(st *store.State, cfg *policy.Settings, id string) reviewReport {
	o := st.Objects[id]
	head := o.Head().N
	rep := reviewReport{Target: fmt.Sprintf("%s@%d", id, head), Covered: []string{}}
	covered := map[string]bool{}
	for _, rid := range st.RunOrder {
		r := st.Runs[rid]
		if !strings.Contains(r.Role, "review") || !slices.ContainsFunc(r.Targets, func(t store.Target) bool { return t.ID == id }) {
			continue
		}
		why := ""
		switch {
		case !slices.Contains(r.Targets, store.Target{ID: id, Version: head}):
			why = "古い版を読んだ"
		case r.Cancelled:
			why = "取り消した"
		case r.State == "failed":
			why = "最後まで終わらなかった（" + r.Fail.Reason + "）"
		case r.State != "answered":
			why = "まだ終わっていない"
		case !r.FreshContext:
			why = "新しい文脈での実行ではない"
		}
		if why != "" {
			rep.NotCounted = append(rep.NotCounted, rid+"："+why)
			continue
		}
		for _, v := range r.Viewpoints {
			covered[v] = true
		}
	}
	for _, v := range cfg.Viewpoints {
		if covered[v.Key] {
			rep.Covered = append(rep.Covered, v.Key)
		} else {
			rep.Missing = append(rep.Missing, v.Key+"："+v.Text)
		}
	}
	for _, f := range st.OfKind("FND") {
		h := f.Head()
		about, ok := hasAbout(h)
		switch {
		case h.Status == "unmapped" && findingFromReviewOf(st, h, id):
			rep.Unmapped = append(rep.Unmapped, f.ID+"："+h.Title)
		case h.Status == "open" && ok && about.To == id:
			note := ""
			if about.Version != head {
				note = fmt.Sprintf("（版 %d への指摘。今の版に残っているかは要再レビュー）", about.Version)
			}
			if a, isBool := h.Fields["affects_acceptance"].(bool); isBool && !a {
				note += "（軽微。扱いは未記録）"
			}
			rep.Open = append(rep.Open, f.ID+"："+h.Title+note)
		}
	}
	switch {
	case len(cfg.Viewpoints) == 0:
		rep.Explanation = "必要な観点が設定されていない。完了とは判定しない"
	case len(rep.Missing) > 0:
		rep.Explanation = "まだ確かめていない観点がある"
	case len(rep.Open)+len(rep.Unmapped) > 0:
		rep.Explanation = "閉じていない指摘がある"
	default:
		rep.Complete = true
		rep.Explanation = "必要な観点をすべて確かめ、指摘は残っていない"
	}
	return rep
}

func hasAbout(v *store.Version) (store.Link, bool) {
	for _, l := range v.Links {
		if l.Rel == "about" {
			return l, true
		}
	}
	return store.Link{}, false
}

// findingFromReviewOf は、対応先の決まっていない指摘が、この記録のレビューから出たものかを返す。
func findingFromReviewOf(st *store.State, v *store.Version, id string) bool {
	r, ok := st.Runs[str(v.Fields, "run")]
	return ok && slices.ContainsFunc(r.Targets, func(t store.Target) bool { return t.ID == id })
}

func cmdReviewStatus(root string, args []string) (any, error) {
	fs := flags("review status")
	target := fs.String("target", "", "記録の番号（必須）")
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
	var rep reviewReport
	err = s.View(func(st *store.State) error {
		if _, ok := st.Objects[*target]; !ok {
			return store.Errf("not-found", "記録 %s がない", *target)
		}
		rep = reviewState(st, cfg, *target)
		return nil
	})
	return rep, err
}
