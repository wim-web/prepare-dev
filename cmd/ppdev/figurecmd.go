package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wim-web/prepare-dev/internal/figure"
	"github.com/wim-web/prepare-dev/internal/policy"
	"github.com/wim-web/prepare-dev/internal/store"
	"github.com/wim-web/prepare-dev/internal/verify"
)

// cmdFigureGenerate は、検証したモデルを実行して得た動きから図を作る。
//
// 受理された検証結果を --evidence で渡すと、そのとき検証器に渡した保存内容から作り、
// 「検証対象から作った図」と表示する。渡さなければ、今のファイルから作った説明図になる。
func cmdFigureGenerate(root string, args []string) (any, error) {
	fs := flags("figure generate")
	frmID := fs.String("frm", "", "元にする形式条件の番号（必須）")
	evdID := fs.String("evidence", "", "使う検証結果の番号")
	title := fs.String("title", "", "図の題")
	out := fs.String("out", "", "書き出す場所。省くと .ppdev/figures/ に置く")
	seconds := fs.Int("timeout", 0, "制限時間（秒）")
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

	var frm store.Version
	var prevID string
	var prevN int
	var prev *figure.Mapping
	meta := figure.Meta{Title: *title}
	var sourceHash string
	err = s.View(func(st *store.State) error {
		o, ok := st.Objects[*frmID]
		if !ok || o.Kind != "FRM" {
			return store.Errf("not-found", "形式条件 %s がない", *frmID)
		}
		frm = *o.Head()
		if *evdID != "" {
			e, ok := st.Objects[*evdID]
			if !ok || e.Kind != "EVD" {
				return store.Errf("not-found", "検証結果 %s がない", *evdID)
			}
			ef := e.Head().Fields
			var a audit
			decode(ef["audit"], &a)
			why := ""
			switch {
			case str(ef, "method") != "theorem-proof" || str(ef, "frm") != *frmID:
				why = "この形式条件の形式証明の結果ではない"
			case int(num(ef, "frm_version")) != frm.N:
				why = "形式条件の古い版についての結果"
			case str(ef, "verdict") != "accepted":
				why = "検証器が受理していない"
			case len(a.Holes) > 0 || len(a.UnapprovedAxioms) > 0:
				why = "未証明の穴か、認めていない公理がある"
			case !boolean(ef, "statement_matches_registered"):
				why = "検証した主張が、記録した主張と違う"
			}
			if why != "" {
				return store.Errf("invalid", "%s からは「検証対象から作った図」を作れない：%s", *evdID, why)
			}
			meta.Verified = true
			meta.Evidence = *evdID + "（形式証明、検証器が受理）"
			meta.Guarantee = "動作ルール（モデル）についての形式証明。コードがこのルールどおりかは、別に確かめる"
			if str(ef, "target_kind") == "implementation" {
				meta.Guarantee = "指定した実装についての形式証明"
			}
			sourceHash = str(ef, "file_hash")
			meta.Unchecked = append(meta.Unchecked, "検証器とは別の検査器での再検査はしていない")
		} else {
			meta.Unchecked = append(meta.Unchecked, "この図は検証結果と結び付いていない。図にある動きが証明済みという意味ではない")
		}
		if g, ok := st.Objects[linkTo(&frm, "formalizes")]; ok {
			rep := guaranteeState(s, st, cfg, g)
			meta.Check = fmt.Sprintf("%s（%s「%s」）", rep.Summary, g.ID, g.Head().Title)
			for _, why := range rep.Missing {
				meta.Unchecked = append(meta.Unchecked, "保証要求に対して足りないもの："+why)
			}
			for _, p := range strs(g.Head().Fields, "premises") {
				meta.Unchecked = append(meta.Unchecked, "前提（確かめていない）："+p)
			}
			for _, p := range strs(g.Head().Fields, "out_of_scope") {
				meta.Unchecked = append(meta.Unchecked, "対象外："+p)
			}
		}
		for _, fig := range st.OfKind("FIG") {
			if str(fig.Head().Fields, "frm") == *frmID {
				var m figure.Mapping
				if decode(fig.Head().Fields["mapping"], &m) == nil {
					prev, prevID, prevN = &m, fig.ID, fig.Head().N
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if meta.Title == "" {
		meta.Title = frm.Title
	}

	file, exportFile := str(frm.Fields, "file"), str(frm.Fields, "export_file")
	if exportFile == "" {
		return nil, store.Errf("invalid", "形式条件 %s に、図のための書き出しの断片（export_file）がない", *frmID)
	}
	var content []byte
	if meta.Verified {
		if content, err = s.GetBlob(sourceHash); err != nil {
			return nil, err
		}
	} else {
		if content, err = os.ReadFile(abs(root, file)); err != nil {
			return nil, store.Errf("not-found", "ファイルが読めない: %s", file)
		}
		sourceHash = store.HashBytes(content)
	}
	export, err := os.ReadFile(abs(root, exportFile))
	if err != nil {
		return nil, store.Errf("not-found", "書き出しの断片が読めない: %s", exportFile)
	}
	if _, err := s.PutBlob(export); err != nil {
		return nil, err
	}
	program := append(append(append([]byte{}, content...), '\n'), export...)
	output, err := verify.RunLean(filepath.Dir(abs(root, file)), program, []string{"edges"}, verifierTimeout(cfg, *seconds))
	if err != nil {
		return nil, store.Errf("invalid", "%v", err)
	}
	bound, edges, err := figure.ParseExport(output)
	if err != nil {
		return nil, store.Errf("invalid", "%v", err)
	}
	mapping := figure.Assign(edges, prev)

	meta.Bound = bound
	meta.Source = fmt.Sprintf("%s 版%d（%s）", *frmID, frm.N, file)
	meta.SourceHash = sourceHash
	meta.GeneratedAt = s.Now().UTC().Format(time.RFC3339)
	kind := "explanatory"
	if meta.Verified {
		kind = "verified"
	}
	var page, path string
	res, _, err := s.Update("", func(st *store.State) ([]store.Op, func(*store.State) any, error) {
		op := store.Op{Type: "put", Kind: "FIG", Title: meta.Title, Origin: &store.Origin{Category: store.CatAI},
			Links: []store.Link{{Rel: "depicts", To: *frmID, Version: frm.N}}}
		if prevID != "" {
			op.ID, op.Base = prevID, prevN
		} else {
			op.ID = st.Reserve("FIG")
		}
		meta.ID, meta.Version = op.ID, prevN+1
		page = figure.Page(meta, mapping)
		path = *out
		if path == "" {
			path = filepath.Join(store.DirName, "figures", op.ID+".md")
		}
		if *evdID != "" {
			op.Links = append(op.Links, store.Link{Rel: "uses-evidence", To: *evdID})
		}
		op.Fields = map[string]any{"kind": kind, "frm": *frmID, "frm_version": frm.N, "file": file, "source_hash": sourceHash,
			"export_file": exportFile, "export_hash": store.HashBytes(export), "evidence": *evdID, "bound": bound,
			"mapping": mapping, "page": path, "page_hash": store.HashBytes([]byte(page)), "generated_at": meta.GeneratedAt}
		id := op.ID
		return []store.Op{op}, func(st *store.State) any {
			return map[string]any{"id": id, "version": st.Objects[id].Head().N, "kind": kind, "page": path,
				"boxes": len(mapping.Nodes), "arrows": len(mapping.Edges),
				"next": "利用者には page の文書を見せる。指摘は箱・矢印の番号を添えて ppdev reply record で記録する"}
		}, nil
	})
	if err != nil {
		return nil, err
	}
	full := abs(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(full, []byte(page), 0o644); err != nil {
		return nil, fmt.Errorf("図は記録したが、文書を書き出せなかった: %w", err)
	}
	return res, nil
}

// figureStale は、図が今の内容と合っているかを確かめ、違うところを返す。
func figureStale(s *store.Store, st *store.State, fig *store.Object, version int) []string {
	var reasons []string
	if head := fig.Head().N; version != head {
		reasons = append(reasons, fmt.Sprintf("この図（版 %d）より新しい版 %d がある", version, head))
	}
	f := fig.At(version).Fields
	if o, ok := st.Objects[str(f, "frm")]; !ok {
		reasons = append(reasons, "元の形式条件がない")
	} else if head := o.Head().N; head != int(num(f, "frm_version")) {
		reasons = append(reasons, fmt.Sprintf("元の形式条件が版 %d から版 %d に変わっている", int(num(f, "frm_version")), head))
	}
	for _, c := range []struct{ path, hash, name string }{
		{str(f, "file"), str(f, "source_hash"), "元のファイル"},
		{str(f, "export_file"), str(f, "export_hash"), "図への書き出し"},
	} {
		switch now := s.FileHash(c.path); {
		case now == "":
			reasons = append(reasons, c.name+"が読めず、照合できない")
		case now != c.hash:
			reasons = append(reasons, c.name+"が、図を作ったあとで変わっている")
		}
	}
	return reasons
}

func cmdFigureCheck(root string, args []string) (any, error) {
	pos, err := parse(flags("figure check <図の番号[@版]>"), args, 1)
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
		fig := st.Objects[t.ID]
		if fig.Kind != "FIG" {
			return store.Errf("invalid", "%s は図ではない", t.ID)
		}
		reasons := figureStale(s, st, fig, t.Version)
		res := map[string]any{"figure": t.String(), "generated_at": str(fig.At(t.Version).Fields, "generated_at")}
		if len(reasons) == 0 {
			res["state"], res["message"] = "current", "図の元になった内容は、今の内容と同じ"
		} else {
			res["state"], res["message"], res["reasons"] = "unverified-for-current", "現在への適用未確認。この図は、作った時点の内容についてのもの", reasons
		}
		out = res
		return nil
	})
	return out, err
}

// cmdReplyRecord は、図の箱や矢印への利用者の指摘を、どの図のどの版のどこへのものかと一緒に記録する。
// 記録するだけで、内容には当てない。古い図への指摘を、今の内容へ推測で付け替えない。
func cmdReplyRecord(root string, args []string) (any, error) {
	fs := flags("reply record")
	figArg := fs.String("figure", "", "利用者が見ていた図 番号@版（必須）")
	element := fs.String("element", "", "指した箱・矢印の番号（例: E3）")
	text := fs.String("text", "", "利用者の指摘（言葉どおりに）")
	replyID := fs.String("reply-id", "", "この返答の番号。同じ返答を二重に記録しないために使う")
	if _, err := parse(fs, args, 0); err != nil {
		return nil, err
	}
	if *text == "" {
		return nil, store.Errf("invalid", "指摘の内容（--text）が要る")
	}
	s, err := open(root)
	if err != nil {
		return nil, err
	}
	reqID := ""
	if *replyID != "" {
		reqID = "reply:" + *replyID
	}
	res, dup, err := s.Update(reqID, func(st *store.State) ([]store.Op, func(*store.State) any, error) {
		if !strings.Contains(*figArg, "@") {
			return nil, nil, store.Errf("invalid", "利用者が見ていた図の版まで指定する（例: FIG-0001@2）")
		}
		t, err := parseTarget(st, *figArg)
		if err != nil {
			return nil, nil, err
		}
		fig := st.Objects[t.ID]
		if fig.Kind != "FIG" {
			return nil, nil, store.Errf("invalid", "%s は図ではない", t.ID)
		}
		ff := fig.At(t.Version).Fields
		var m figure.Mapping
		decode(ff["mapping"], &m)
		fields := map[string]any{"figure": t.String(), "element": *element, "text": *text, "mapped": false,
			"frm": fmt.Sprintf("%s@%d", str(ff, "frm"), int(num(ff, "frm_version")))}
		if name, ok := m.Nodes[*element]; ok {
			fields["mapped"], fields["meaning"] = true, "状態「"+name+"」"
		} else if e, ok := m.Edges[*element]; ok {
			fields["mapped"] = true
			fields["meaning"] = fmt.Sprintf("「%s」から「%s」への動き：%s", e.From, e.To, e.Label)
			if e.Effect != "" {
				fields["meaning"] = fields["meaning"].(string) + "（" + e.Effect + "）"
			}
		} else {
			fields["unmapped_reason"] = fmt.Sprintf("図 %s に「%s」という番号の箱・矢印はない", t.String(), *element)
		}
		stale := figureStale(s, st, fig, t.Version)
		fields["applicability"] = "current"
		if len(stale) > 0 {
			fields["applicability"], fields["applicability_reasons"] = "unverified-for-current", stale
		}
		id := st.Reserve("RPL")
		links := []store.Link{{Rel: "about", To: t.ID, Version: t.Version}}
		return []store.Op{{Type: "put", ID: id, Kind: "RPL", Title: "図への指摘：" + *element, Body: *text, Fields: fields, Links: links,
			Origin: &store.Origin{Category: store.CatUser, Source: "図 " + t.String() + " への返答"}}}, func(st *store.State) any {
			out := map[string]any{"id": id, "status": st.Objects[id].Head().Status, "figure": t.String(),
				"meaning": fields["meaning"], "applicability": fields["applicability"]}
			switch {
			case fields["mapped"] == false:
				out["next"] = "どこへの指摘か決まっていない。推測で付けず、その箇所だけ利用者に確かめる"
				out["unmapped_reason"] = fields["unmapped_reason"]
			case len(stale) > 0:
				out["next"] = "古い図への指摘。今の内容との差を確かめてから扱う。そのまま今の内容に当てない"
				out["reasons"] = stale
			default:
				out["next"] = "記録した。まだ内容には当てていない。意味を変える指摘なら要件整理へ戻す"
			}
			return out
		}, nil
	})
	if err != nil {
		return nil, err
	}
	if dup {
		return map[string]any{"duplicate": true, "note": "同じ返答は既に記録済み。二重には記録していない", "result": res}, nil
	}
	return res, nil
}
