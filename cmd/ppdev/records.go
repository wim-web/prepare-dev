package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	preparedev "github.com/wim-web/prepare-dev"
	"github.com/wim-web/prepare-dev/internal/skills"
	"github.com/wim-web/prepare-dev/internal/store"
)

// cmdInit は、記録の置き場所を作り、この実行ファイルに入っている skill をプロジェクトに入れる。
//
// skill は、前に入れたときから編集されていなければ、この版に置き換える。
// 編集されていれば残して知らせる。リンクやファイルになっている場所には触らない。
func cmdInit(root string, args []string) (any, error) {
	fs := flags("init")
	noSkills := fs.Bool("no-skills", false, "skill を入れない")
	replace := fs.Bool("replace-skills", false, "中身が違う skill を、.ppdev/skills-backup に退避してから置き換える")
	var dirs multi
	fs.Var(&dirs, "skills-dir", "skill を入れる場所（プロジェクトからの道筋。繰り返せる）。省くと .agents/skills と .claude/skills")
	if _, err := parse(fs, args, 0); err != nil {
		return nil, err
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	s, err := store.Init(root)
	if err != nil {
		return nil, err
	}
	if err := ignoreLines(filepath.Join(s.Dir, ".gitignore"), ".lock", "skills-backup/"); err != nil {
		return nil, err
	}
	out := map[string]any{
		"dir":  s.Dir,
		"next": "依頼を受けたら、作る前に、どう理解したかを HTML のページで利用者に見せる（prepare-dev の skill）。担当ごとの設定は、別の担当を使うと利用者が決めたときに要る。それまでは聞かない",
	}

	var dests []string
	switch {
	case *noSkills:
		out["skills_note"] = "skill は入れていない（--no-skills）"
		return out, nil
	case len(dirs) > 0:
		for _, d := range dirs {
			dests = append(dests, filepath.Join(root, d))
		}
	default:
		dests = []string{filepath.Join(root, ".agents", "skills"), filepath.Join(root, ".claude", "skills")}
	}
	res, err := skills.Copy(preparedev.Skills(), dests, root, skills.Options{Replace: *replace})
	if err != nil {
		return nil, err
	}
	rel := func(paths []string) []string {
		list := []string{}
		for _, p := range paths {
			if r, err := filepath.Rel(root, p); err == nil {
				p = r
			}
			list = append(list, p)
		}
		return list
	}
	replaced := []map[string]string{}
	for _, r := range res.Replaced {
		replaced = append(replaced, map[string]string{"skill": rel([]string{r.Path})[0], "backup": rel([]string{r.Backup})[0]})
	}
	out["skills"] = map[string]any{
		"copied": rel(res.Copied), "updated": rel(res.Updated), "unchanged": rel(res.Unchanged),
		"replaced": replaced, "left_as_is": rel(res.Conflicts),
	}
	if len(res.Conflicts) > 0 {
		out["skills_note"] = "left_as_is の skill は、中身がこの版と違うので、そのままにした。編集したものか、init 以外で入れたものか、リンクになっているもの。" +
			"この版に置き換えるなら ppdev init --replace-skills（元の中身は退避する）。リンクになっている場所は、それでも触らない"
	}
	return out, nil
}

// ignoreLines は、.gitignore に行がなければ足す。
func ignoreLines(path string, lines ...string) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	text := string(data)
	have := strings.Split(text, "\n")
	changed := false
	for _, l := range lines {
		if !slices.Contains(have, l) {
			if text != "" && !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			text += l + "\n"
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

func putResult(id string) func(*store.State) any {
	return func(st *store.State) any {
		h := st.Objects[id].Head()
		return map[string]any{"id": id, "version": h.N, "status": h.Status, "hash": h.Hash}
	}
}

func cmdNew(root string, args []string) (any, error) {
	fs := flags("new <種類>")
	var c content
	c.register(fs)
	reqID := fs.String("request-id", "", "同じ要求を二重に当てないための番号")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return nil, err
	}
	kind := strings.ToUpper(pos[0])
	body, err := c.readBody()
	if err != nil {
		return nil, err
	}
	fields, err := c.readFields()
	if err != nil {
		return nil, err
	}
	s, err := open(root)
	if err != nil {
		return nil, err
	}
	res, _, err := s.Update(*reqID, func(st *store.State) ([]store.Op, func(*store.State) any, error) {
		links, err := c.readLinks(st)
		if err != nil {
			return nil, nil, err
		}
		if _, ok := store.Kinds[kind]; !ok {
			return nil, nil, store.Errf("invalid", "知らない種類: %s", kind)
		}
		id := st.Reserve(kind)
		return []store.Op{{Type: "put", ID: id, Kind: kind, Title: c.title, Body: body, Fields: fields, Links: links, Origin: c.originValue()}}, putResult(id), nil
	})
	return res, err
}

func cmdRevise(root string, args []string) (any, error) {
	fs := flags("revise <番号>")
	var c content
	c.register(fs)
	base := fs.Int("base", 0, "元にした版（必須）。今の版と違えば衝突として拒否する")
	reqID := fs.String("request-id", "", "同じ要求を二重に当てないための番号")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return nil, err
	}
	if *base < 1 {
		return nil, store.Errf("invalid", "--base に、元にした版を渡す")
	}
	id := pos[0]
	body, err := c.readBody()
	if err != nil {
		return nil, err
	}
	fields, err := c.readFields()
	if err != nil {
		return nil, err
	}
	s, err := open(root)
	if err != nil {
		return nil, err
	}
	res, _, err := s.Update(*reqID, func(st *store.State) ([]store.Op, func(*store.State) any, error) {
		o, ok := st.Objects[id]
		if !ok {
			return nil, nil, store.Errf("not-found", "記録 %s がない", id)
		}
		prev := o.At(*base)
		if prev == nil {
			return nil, nil, store.Errf("not-found", "%s に版 %d はない", id, *base)
		}
		links, err := c.readLinks(st)
		if err != nil {
			return nil, nil, err
		}
		// 渡さなかった部分は、元にした版の内容を引き継ぐ。
		op := store.Op{Type: "put", ID: id, Base: *base, Title: c.title, Body: body, Fields: fields, Links: links, Origin: c.originValue()}
		if op.Title == "" {
			op.Title = prev.Title
		}
		if c.body == "" && c.bodyFile == "" {
			op.Body = prev.Body
		}
		if len(prev.Fields) > 0 {
			merged := map[string]any{}
			for k, v := range prev.Fields {
				merged[k] = v
			}
			for k, v := range fields {
				merged[k] = v
			}
			op.Fields = merged
		}
		if links == nil {
			op.Links = prev.Links
		}
		return []store.Op{op}, putResult(id), nil
	})
	return res, err
}

func cmdDecide(root string, args []string) (any, error) {
	fs := flags("decide")
	title := fs.String("title", "", "何を決めたか")
	body := fs.String("body", "", "利用者が述べた理由。述べていなければ空のままにする")
	source := fs.String("source", "", "根拠となる利用者の発言・操作（必須）")
	by := fs.String("by", "", "決めた人")
	var adopt, reject, questions multi
	fs.Var(&adopt, "adopt", "採用する提案 番号@版（繰り返せる）")
	fs.Var(&reject, "reject", "不採用にする提案 番号@版（繰り返せる）")
	fs.Var(&questions, "resolve", "この決定で閉じる未決事項の番号（繰り返せる）")
	reqID := fs.String("request-id", "", "同じ要求を二重に当てないための番号")
	if _, err := parse(fs, args, 0); err != nil {
		return nil, err
	}
	s, err := open(root)
	if err != nil {
		return nil, err
	}
	res, _, err := s.Update(*reqID, func(st *store.State) ([]store.Op, func(*store.State) any, error) {
		dec := st.Reserve("DEC")
		links := []store.Link{}
		ops := []store.Op{{}}
		for as, list := range map[string]multi{"adopted": adopt, "rejected": reject} {
			for _, a := range list {
				if !strings.Contains(a, "@") {
					return nil, nil, store.Errf("invalid", "採否は版を指定して決める（例: EX-0001@2）: %s", a)
				}
				t, err := parseTarget(st, a)
				if err != nil {
					return nil, nil, err
				}
				links = append(links, store.Link{Rel: as, To: t.ID, Version: t.Version})
				ops = append(ops, store.Op{Type: "adopt", ID: t.ID, Version: t.Version, As: as, Decision: dec})
			}
		}
		for _, q := range questions {
			links = append(links, store.Link{Rel: "resolves", To: q})
			ops = append(ops, store.Op{Type: "resolve", ID: q, Resolution: &store.Resolution{To: "resolved", Decision: dec}})
		}
		ops[0] = store.Op{Type: "put", ID: dec, Kind: "DEC", Title: *title, Body: *body, Links: links,
			Origin: &store.Origin{Category: store.CatUser, Source: *source, By: *by}}
		return ops, func(*store.State) any {
			return map[string]any{"decision": dec, "adopted": []string(adopt), "rejected": []string(reject), "resolved": []string(questions)}
		}, nil
	})
	return res, err
}

func cmdDerive(root string, args []string) (any, error) {
	fs := flags("derive")
	from := fs.String("from", "", "元になる利用者の決定の番号（必須）")
	title := fs.String("title", "", "何を導いたか")
	body := fs.String("body", "", "どう導いたか（必須）")
	var adopt multi
	fs.Var(&adopt, "adopt", "採用する提案 番号@版（繰り返せる）")
	reqID := fs.String("request-id", "", "同じ要求を二重に当てないための番号")
	if _, err := parse(fs, args, 0); err != nil {
		return nil, err
	}
	s, err := open(root)
	if err != nil {
		return nil, err
	}
	res, _, err := s.Update(*reqID, func(st *store.State) ([]store.Op, func(*store.State) any, error) {
		dec := st.Reserve("DEC")
		links := []store.Link{{Rel: "derived-from", To: *from}}
		ops := []store.Op{{}}
		for _, a := range adopt {
			if !strings.Contains(a, "@") {
				return nil, nil, store.Errf("invalid", "採否は版を指定して決める（例: EX-0001@2）: %s", a)
			}
			t, err := parseTarget(st, a)
			if err != nil {
				return nil, nil, err
			}
			links = append(links, store.Link{Rel: "adopted", To: t.ID, Version: t.Version})
			ops = append(ops, store.Op{Type: "adopt", ID: t.ID, Version: t.Version, As: "adopted", Decision: dec})
		}
		ops[0] = store.Op{Type: "put", ID: dec, Kind: "DEC", Title: *title, Body: *body, Links: links,
			Origin: &store.Origin{Category: store.CatDerived}}
		return ops, func(*store.State) any {
			return map[string]any{"decision": dec, "derived_from": *from, "adopted": []string(adopt)}
		}, nil
	})
	return res, err
}

func cmdResolve(root string, args []string) (any, error) {
	fs := flags("resolve <番号>")
	to := fs.String("to", "", "resolved | rejected | minor（指摘）、applied | dismissed（返答）")
	reason := fs.String("reason", "", "理由")
	decision := fs.String("decision", "", "根拠になる利用者の決定")
	recheck := fs.String("recheck-run", "", "新しい文脈で再確認した実行")
	refutation := fs.String("refutation", "", "反証になる保存内容 番号@版")
	fix := fs.String("fix", "", "直した版 番号@版")
	reqID := fs.String("request-id", "", "同じ要求を二重に当てないための番号")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return nil, err
	}
	s, err := open(root)
	if err != nil {
		return nil, err
	}
	res, _, err := s.Update(*reqID, func(st *store.State) ([]store.Op, func(*store.State) any, error) {
		r := &store.Resolution{To: *to, Reason: *reason, Decision: *decision, RecheckRun: *recheck}
		for ref, dst := range map[string]**store.Target{*refutation: &r.Refutation, *fix: &r.Fix} {
			if ref == "" {
				continue
			}
			t, err := parseTarget(st, ref)
			if err != nil {
				return nil, nil, err
			}
			*dst = &t
		}
		return []store.Op{{Type: "resolve", ID: pos[0], Resolution: r}}, putResult(pos[0]), nil
	})
	return res, err
}

func cmdShow(root string, args []string) (any, error) {
	pos, err := parse(flags("show <番号[@版]>"), args, 1)
	if err != nil {
		return nil, err
	}
	s, err := open(root)
	if err != nil {
		return nil, err
	}
	var out any
	err = s.View(func(st *store.State) error {
		t, err := parseTarget(st, pos[0])
		if err != nil {
			return err
		}
		o := st.Objects[t.ID]
		out = map[string]any{"id": o.ID, "kind": store.Kinds[o.Kind], "latest": o.Head().N, "version": o.At(t.Version)}
		return nil
	})
	return out, err
}

func cmdList(root string, args []string) (any, error) {
	fs := flags("list")
	kind := fs.String("kind", "", "種類で絞る（例: REQ）")
	status := fs.String("status", "", "状態で絞る（例: proposed）")
	if _, err := parse(fs, args, 0); err != nil {
		return nil, err
	}
	s, err := open(root)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	err = s.View(func(st *store.State) error {
		for _, id := range st.Order {
			o := st.Objects[id]
			if *kind != "" && !strings.EqualFold(*kind, o.Kind) {
				continue
			}
			if *status != "" && o.Head().Status != *status {
				continue
			}
			out = append(out, summary(o))
		}
		return nil
	})
	return out, err
}

// cmdTx は、ある要求が確定しているかを答える。保存できたか分からないときに照合する。
func cmdTx(root string, args []string) (any, error) {
	pos, err := parse(flags("tx <要求の番号>"), args, 1)
	if err != nil {
		return nil, err
	}
	s, err := open(root)
	if err != nil {
		return nil, err
	}
	res, ok, err := s.Applied(pos[0])
	if err != nil {
		return nil, err
	}
	return map[string]any{"request": pos[0], "applied": ok, "result": res}, nil
}
