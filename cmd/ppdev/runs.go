package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/wim-web/prepare-dev/internal/policy"
	"github.com/wim-web/prepare-dev/internal/store"
)

func runResult(id string) func(*store.State) any {
	return func(st *store.State) any { return st.Runs[id] }
}

func cmdRunStart(root string, args []string) (any, error) {
	fs := flags("run start")
	role := fs.String("role", "", "役割（例: spec-review, verify）")
	ticket := fs.String("ticket", "", "spawn check で許可された実行の番号。担当が動き始めたことを記録する")
	retryOf := fs.String("retry-of", "", "やり直す元の実行")
	fresh := fs.Bool("fresh-context", false, "新しい文脈（会話の経緯を持たない）で行う実行か")
	var targets, files, viewpoints multi
	fs.Var(&targets, "target", "対象 番号[@版]（繰り返せる）")
	fs.Var(&files, "file", "読むファイル（内容を保存する。繰り返せる）")
	fs.Var(&viewpoints, "viewpoint", "確かめる観点（繰り返せる）")
	if _, err := parse(fs, args, 0); err != nil {
		return nil, err
	}
	s, err := open(root)
	if err != nil {
		return nil, err
	}
	if *ticket != "" {
		res, _, err := s.Update("", func(*store.State) ([]store.Op, func(*store.State) any, error) {
			return []store.Op{{Type: "run.begin", ID: *ticket}}, runResult(*ticket), nil
		})
		return res, err
	}
	snapshot, err := s.Snapshot(files)
	if err != nil {
		return nil, err
	}
	res, _, err := s.Update("", func(st *store.State) ([]store.Op, func(*store.State) any, error) {
		ts, err := parseTargets(st, targets)
		if err != nil {
			return nil, nil, err
		}
		id := st.Reserve("RUN")
		run := &store.Run{ID: id, Role: policy.RoleName(*role), State: "running", Targets: ts, Snapshot: snapshot,
			Viewpoints: viewpoints, FreshContext: *fresh, RetryOf: *retryOf}
		return []store.Op{{Type: "run.new", Run: run}}, runResult(id), nil
	})
	return res, err
}

func cmdRunCancel(root string, args []string) (any, error) {
	pos, err := parse(flags("run cancel <実行の番号>"), args, 1)
	if err != nil {
		return nil, err
	}
	s, err := open(root)
	if err != nil {
		return nil, err
	}
	res, _, err := s.Update("", func(*store.State) ([]store.Op, func(*store.State) any, error) {
		return []store.Op{{Type: "run.cancel", ID: pos[0]}}, runResult(pos[0]), nil
	})
	return res, err
}

func cmdRunAnswer(root string, args []string) (any, error) {
	fs := flags("run answer <実行の番号>")
	file := fs.String("file", "", "回答の内容を読むファイル")
	text := fs.String("text", "", "回答の内容")
	sum := fs.String("summary", "", "回答の要点（例: 指摘なし）")
	reqID := fs.String("request-id", "", "同じ回答を二重に当てないための番号")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return nil, err
	}
	data := []byte(*text)
	if *file != "" {
		if data, err = os.ReadFile(*file); err != nil {
			return nil, store.Errf("not-found", "回答のファイルが読めない: %s", *file)
		}
	}
	if len(data) == 0 {
		return nil, store.Errf("invalid", "回答の内容が要る（--file か --text）")
	}
	s, err := open(root)
	if err != nil {
		return nil, err
	}
	hash, err := s.PutBlob(data)
	if err != nil {
		return nil, err
	}
	id := pos[0]
	res, _, err := s.Update(*reqID, func(*store.State) ([]store.Op, func(*store.State) any, error) {
		return []store.Op{{Type: "run.answer", ID: id, Answer: &store.RunAnswer{Hash: hash, Summary: *sum}}},
			func(st *store.State) any { return answerReport(st, id) }, nil
	})
	return res, err
}

// answerReport は、届いた回答をどう扱ったかを言葉で返す。
func answerReport(st *store.State, id string) map[string]any {
	r := st.Runs[id]
	a := r.Answers[len(r.Answers)-1]
	out := map[string]any{"run": id, "saved_to_history": true, "adopted": a.Adopted, "answer": a.Hash}
	switch {
	case a.Adopted:
		out["note"] = "この実行の履歴へ保存し、現在の結果に採用した"
	case r.Cancelled:
		out["note"] = "この実行は取消済み。回答は履歴へ保存し、現在の結果は変えていない"
	case !a.Fresh:
		out["note"] = "対象がこの実行の読んだ版から変わっている。回答は履歴へ保存し、現在の結果は変えていない"
	}
	if cur := st.Slots[r.Slot()].Current; cur != nil {
		out["current"] = cur
	}
	return out
}

func cmdRunFail(root string, args []string) (any, error) {
	fs := flags("run fail <実行の番号>")
	reason := fs.String("reason", "", "timeout | error | unproven | limit | stopped")
	detail := fs.String("detail", "", "補足")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return nil, err
	}
	s, err := open(root)
	if err != nil {
		return nil, err
	}
	res, _, err := s.Update("", func(*store.State) ([]store.Op, func(*store.State) any, error) {
		return []store.Op{{Type: "run.fail", ID: pos[0], Fail: &store.Failure{Reason: *reason, Detail: *detail}}}, runResult(pos[0]), nil
	})
	return res, err
}

func cmdRunShow(root string, args []string) (any, error) {
	pos, err := parse(flags("run show <実行の番号>"), args, 1)
	if err != nil {
		return nil, err
	}
	s, err := open(root)
	if err != nil {
		return nil, err
	}
	var out any
	err = s.View(func(st *store.State) error {
		r, ok := st.Runs[pos[0]]
		if !ok {
			return store.Errf("not-found", "実行 %s がない", pos[0])
		}
		out = map[string]any{"run": r, "slot": st.Slots[r.Slot()]}
		return nil
	})
	return out, err
}

func ticketLine(id string) string { return fmt.Sprintf("[ppdev-ticket: %s]", id) }

var ticketRe = regexp.MustCompile(`\[ppdev-ticket: (RUN-\d+)\]`)

// cmdSpawnCheck は、担当を起動してよいかを設定と照合し、結果を記録する。
// 許可したときは実行の番号（起動の札）を返す。拒否したときは終了コード 2 で終わる。
func cmdSpawnCheck(root string, args []string) (any, error) {
	fs := flags("spawn check")
	role := fs.String("role", "", "役割（必須）")
	model := fs.String("model", "", "使いたいモデル。省くと役割の設定を使う")
	effort := fs.String("effort", "", "使いたい思考量。省くと通常の値を使う。いちばん難しい作業では一段上、判断の作業では一段下、定型の作業では二段以上下を選べる")
	reason := fs.String("reason", "", "思考量を通常の値から変える理由（変えるときは必須）")
	event := fs.String("event", "", "通常値以外の思考量を使う根拠の出来事（例: verifier-timeout）")
	eventRun := fs.String("event-run", "", "その出来事が起きた実行")
	parent := fs.String("parent", "", "追加を求めた担当の実行")
	retryOf := fs.String("retry-of", "", "やり直す元の実行")
	var targets, files, viewpoints multi
	fs.Var(&targets, "target", "対象 番号[@版]（繰り返せる）")
	fs.Var(&files, "file", "担当に読ませるファイル（内容を保存する。繰り返せる）")
	fs.Var(&viewpoints, "viewpoint", "確かめる観点（繰り返せる）")
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
	snapshot, err := s.Snapshot(files)
	if err != nil {
		return nil, err
	}
	var allowed bool
	res, _, err := s.Update("", func(st *store.State) ([]store.Op, func(*store.State) any, error) {
		ts, err := parseTargets(st, targets)
		if err != nil {
			return nil, nil, err
		}
		d := policy.Check(st, cfg, policy.Request{Role: *role, Model: *model, Effort: *effort, Reason: *reason,
			Event: *event, EventRun: *eventRun, Targets: ts}, s.Now())
		allowed = d.Allowed
		id := st.Reserve("RUN")
		run := &store.Run{ID: id, Role: d.Role, Targets: ts, Snapshot: snapshot, Viewpoints: viewpoints,
			Parent: *parent, RetryOf: *retryOf, FreshContext: d.Context == "fresh",
			Requested: store.Setting{Model: *model, Effort: *effort}, Resolved: d.Resolved,
			Enforce: d.Enforce, Exception: d.Exception, Event: *event, Note: d.Note, Reasons: d.Reasons, State: "denied"}
		out := map[string]any{"allowed": d.Allowed, "run": id, "role": policy.RoleLabel(d.Role)}
		if d.Allowed {
			run.State = "approved"
			out["use"] = d.Resolved
			out["context"] = d.Context
			out["ticket_line"] = ticketLine(id)
			out["next"] = "担当への依頼文に ticket_line の一行をそのまま入れ、" + launchHint(d.Resolved) + "。ほかの値では起動しない"
			if d.Note != "" {
				out["adjusted"] = d.Note
				out["if_rejected"] = "実行環境がこの思考量を受け付けなければ、別の値は試さない。この実行を ppdev run fail で閉じ、--effort を付けずに取り直す"
			}
			if d.Enforce != "machine" {
				out["enforcement"] = "この制限は手順としての指示。実行環境では止めていない"
			}
			if d.Exception != "" {
				out["exception"] = d.Exception
			}
		} else {
			out["reasons"] = d.Reasons
			out["alternatives"] = d.Alternatives
			out["next"] = "起動しない。理由を利用者に示す。許可されていない値へ自動で切り替えない"
		}
		return []store.Op{{Type: "run.new", Run: run}}, func(*store.State) any { return out }, nil
	})
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, &exitError{code: 2, result: json.RawMessage(res)}
	}
	return res, nil
}

// cmdSpawnGate は、実行環境が担当を起動する直前に呼ぶ入口（Claude Code の PreToolUse フック）。
// 許可された札のない起動、札と違うモデル・思考量での起動を、終了コード 2 で止める。
func cmdSpawnGate(root string, args []string) (any, error) {
	if _, err := parse(flags("spawn gate"), args, 0); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, err
	}
	var in struct {
		Cwd       string `json:"cwd"`
		ToolName  string `json:"tool_name"`
		ToolInput struct {
			Prompt string `json:"prompt"`
			Model  string `json:"model"`
			Effort string `json:"effort"`
		} `json:"tool_input"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, store.Errf("invalid", "フックの入力が読めない: %v", err)
	}
	if in.ToolName != "Task" && in.ToolName != "Agent" {
		return nil, nil
	}
	if os.Getenv("PPDEV_ROOT") == "" && in.Cwd != "" {
		root = in.Cwd
	}
	s, err := store.Open(root)
	if err != nil {
		return nil, nil // prepare-dev を使っていない場所では何もしない
	}
	deny := func(format string, a ...any) (any, error) {
		fmt.Fprintf(os.Stderr, "prepare-dev が起動を止めた: "+format+"\n先に ppdev spawn check で許可を取り、返ってきた ticket_line を依頼文に入れる。\n", a...)
		return nil, &exitError{code: 2}
	}
	m := ticketRe.FindStringSubmatch(in.ToolInput.Prompt)
	if m == nil {
		return deny("依頼文に起動の札（ticket_line）がない。")
	}
	id := m[1]
	var problem string
	_, _, err = s.Update("", func(st *store.State) ([]store.Op, func(*store.State) any, error) {
		r, ok := st.Runs[id]
		switch {
		case !ok:
			problem = fmt.Sprintf("札 %s は記録にない。", id)
		case r.Cancelled:
			problem = fmt.Sprintf("札 %s の実行は取り消されている。", id)
		case r.State != "approved":
			problem = fmt.Sprintf("札 %s は使えない状態（%s）。札は一度だけ使える。", id, r.State)
		case in.ToolInput.Model != "" && r.Resolved.Model != policy.InheritParent && !sameSetting(in.ToolInput.Model, r.Resolved.Model):
			problem = fmt.Sprintf("モデル「%s」は、許可された「%s」と違う。", in.ToolInput.Model, r.Resolved.Model)
		case in.ToolInput.Effort != "" && r.Resolved.Effort != policy.InheritParent && !sameSetting(in.ToolInput.Effort, r.Resolved.Effort):
			problem = fmt.Sprintf("思考量「%s」は、許可された「%s」と違う。", in.ToolInput.Effort, r.Resolved.Effort)
		}
		if problem != "" {
			return nil, nil, store.Errf("denied", "%s", problem)
		}
		observed := &store.Setting{Model: in.ToolInput.Model, Effort: in.ToolInput.Effort}
		return []store.Op{{Type: "run.begin", ID: id, Observed: observed}}, nil, nil
	})
	if problem != "" {
		return deny("%s", problem)
	}
	return nil, err
}

// launchHint は、許可された値で担当を起動するやり方を言葉にする。
func launchHint(use store.Setting) string {
	model := "use のモデルで"
	if use.Model == policy.InheritParent {
		model = "モデルは指定せずに（親と同じモデルで）"
	}
	effort := "use の思考量で起動する"
	if use.Effort == policy.InheritParent {
		effort = "思考量も指定せずに起動する"
	}
	return model + "、" + effort
}

func sameSetting(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func cmdSpawnObserved(root string, args []string) (any, error) {
	fs := flags("spawn observed <実行の番号>")
	model := fs.String("model", "", "実際に使われたと確認できたモデル")
	effort := fs.String("effort", "", "実際に使われたと確認できた思考量")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return nil, err
	}
	s, err := open(root)
	if err != nil {
		return nil, err
	}
	res, _, err := s.Update("", func(*store.State) ([]store.Op, func(*store.State) any, error) {
		return []store.Op{{Type: "run.observed", ID: pos[0], Observed: &store.Setting{Model: *model, Effort: *effort}}}, runResult(pos[0]), nil
	})
	return res, err
}

func cmdPolicyShow(root string, args []string) (any, error) {
	if _, err := parse(flags("policy show"), args, 0); err != nil {
		return nil, err
	}
	cfg, err := policy.Load(root)
	if err != nil {
		return nil, err
	}
	roles := []map[string]any{}
	names := make([]string, 0, len(cfg.Roles))
	for name := range cfg.Roles {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		seats := cfg.Roles[name]
		use := []string{}
		for _, seat := range seats {
			effort := seat.Effort
			if effort == "" {
				effort = cfg.DefaultEffort
			}
			if effort == "" || effort == policy.InheritParent {
				effort = "親と同じ"
			}
			model := seat.Model
			if model == policy.InheritParent {
				model = "親と同じ"
			}
			use = append(use, model+" @"+effort)
		}
		roles = append(roles, map[string]any{"role": policy.RoleLabel(name), "name": name, "people": len(seats), "use": use})
	}
	out := map[string]any{"roles": roles, "settings": cfg, "project_file": policy.Path(root), "user_files": policy.UserPaths()}
	switch {
	case cfg.Source == "":
		out["note"] = "設定の表がない。prepare-setup の skill で利用者に確認して作るまで、担当は起動しない"
	case len(cfg.Roles) == 0:
		out["note"] = "設定の表に役割の行がない。担当は起動しない"
	}
	if cfg.EffortAuto {
		out["effort"] = "表の値が出発点。いちばん難しい作業では一段上げ、判断の作業では一段、定型の作業では二段以上下げる（理由を記録する）"
	} else {
		out["effort"] = "表の値に固定。上げられるのは「思考量の引き上げ」に書いた出来事が記録にあるときだけ"
	}
	if cfg.Enforce {
		out["enforcement"] = "実行環境で止める設定。起動の入口（ppdev spawn gate）を実行環境に入れていなければ、止まらない"
	} else {
		out["enforcement"] = "手順としての指示。実行環境では止めていない"
	}
	s, err := store.Open(root)
	if err == nil {
		exceptions := []map[string]any{}
		adjusted := []map[string]any{}
		s.View(func(st *store.State) error {
			for _, id := range st.RunOrder {
				if r := st.Runs[id]; r.Note != "" && r.State != "denied" {
					adjusted = append(adjusted, map[string]any{"run": r.ID, "role": policy.RoleLabel(r.Role), "note": r.Note})
				}
			}
			for _, o := range st.OfKind("EXC") {
				exceptions = append(exceptions, map[string]any{"id": o.ID, "title": o.Head().Title, "status": o.Head().Status, "scope": o.Head().Fields})
			}
			return nil
		})
		out["exceptions"] = exceptions
		out["effort_changed_by_ai"] = adjusted
	}
	return out, nil
}

// cmdPolicyTemplate は、設定の表のひな形を出す。
func cmdPolicyTemplate(root string, args []string) (any, error) {
	if _, err := parse(flags("policy template"), args, 0); err != nil {
		return nil, err
	}
	return policy.Template, nil
}

// cmdPolicyExcept は、利用者が明示した例外を、範囲を付けて記録する。
func cmdPolicyExcept(root string, args []string) (any, error) {
	fs := flags("policy except")
	decision := fs.String("decision", "", "根拠になる利用者の決定（必須）")
	title := fs.String("title", "", "何を許すか")
	role := fs.String("role", "", "役割（すべての役割なら *）")
	model := fs.String("model", "", "許すモデル")
	effort := fs.String("effort", "", "許す思考量")
	until := fs.String("until", "", "いつまで有効か（例: 2026-11-14T00:00:00Z）")
	target := fs.String("target", "", "この記録を対象にする実行に限る")
	uses := fs.Int("uses", 0, "何回まで使えるか。今回だけなら 1")
	if _, err := parse(fs, args, 0); err != nil {
		return nil, err
	}
	if *role == "" || (*model == "" && *effort == "") {
		return nil, store.Errf("invalid", "例外には、役割と、許す値（モデルか思考量）が要る")
	}
	if *until == "" && *target == "" && *uses == 0 {
		return nil, store.Errf("invalid", "例外には有効な範囲が要る（--uses / --until / --target）。範囲のない例外は、設定そのものを変える形で利用者に確認する")
	}
	if *until != "" {
		if _, err := time.Parse(time.RFC3339, *until); err != nil {
			return nil, store.Errf("invalid", "--until の書き方が違う: %s", *until)
		}
	}
	fields := map[string]any{"role": policy.RoleName(*role)}
	for k, v := range map[string]string{"model": *model, "effort": *effort, "until": *until, "target": *target} {
		if v != "" {
			fields[k] = v
		}
	}
	if *uses > 0 {
		fields["uses"] = *uses
	}
	s, err := open(root)
	if err != nil {
		return nil, err
	}
	res, _, err := s.Update("", func(st *store.State) ([]store.Op, func(*store.State) any, error) {
		id := st.Reserve("EXC")
		return []store.Op{{Type: "put", ID: id, Kind: "EXC", Title: *title, Fields: fields,
			Links: []store.Link{{Rel: "decided-by", To: *decision}}, Origin: &store.Origin{Category: store.CatAI}}}, putResult(id), nil
	})
	return res, err
}
