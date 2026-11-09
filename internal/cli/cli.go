// Package cli is the ppdev command line. Agents run it from the skills; the
// commands, flags and output formats are part of that contract.
package cli

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	preparedev "github.com/wim-web/prepare-dev"
	"github.com/wim-web/prepare-dev/internal/core"
	"github.com/wim-web/prepare-dev/internal/daemon"
	"github.com/wim-web/prepare-dev/internal/plugin"
	"github.com/wim-web/prepare-dev/internal/plugin/gotest"
	"github.com/wim-web/prepare-dev/internal/plugin/lean4"
	"github.com/wim-web/prepare-dev/internal/plugin/vitest"
	"github.com/wim-web/prepare-dev/internal/runtime"
	"github.com/wim-web/prepare-dev/internal/server"
	"github.com/wim-web/prepare-dev/internal/skills"
)

// Plugins are the verification plugins a daemon registers.
func Plugins() []plugin.Plugin {
	return []plugin.Plugin{lean4.New(), vitest.New(), gotest.New()}
}

// ---------- arguments ----------

type flagVal struct {
	v    string
	bare bool // given as "--key" without a value
}

type args struct {
	pos   []string
	flags map[string][]flagVal
}

// booleanFlags never take the next token as their value.
var booleanFlags = map[string]bool{
	"json": true, "wait": true, "detach": true, "no-start": true, "no-skills": true, "no-free-text": true,
	"no-open": true, "help": true, "version": true, "stdout": true, "no-expand": true, "replace-skills": true,
}

func parseArgs(argv []string) *args {
	a := &args{flags: map[string][]flagVal{}}
	for i := 0; i < len(argv); i++ {
		x := argv[i]
		if !strings.HasPrefix(x, "--") {
			a.pos = append(a.pos, x)
			continue
		}
		key := x[2:]
		fv := flagVal{v: "true", bare: true}
		if eq := strings.IndexByte(key, '='); eq >= 0 {
			fv = flagVal{v: key[eq+1:]}
			key = key[:eq]
		} else if !booleanFlags[key] && i+1 < len(argv) && !strings.HasPrefix(argv[i+1], "--") {
			i++
			fv = flagVal{v: argv[i]}
		}
		a.flags[key] = append(a.flags[key], fv)
	}
	return a
}

// str returns the last value of a flag; a single bare flag has no value.
func (a *args) str(key string) (string, bool) {
	v := a.flags[key]
	if len(v) == 0 || (len(v) == 1 && v[0].bare) {
		return "", false
	}
	return v[len(v)-1].v, true
}

// list returns every value of a repeatable flag.
func (a *args) list(key string) []string {
	v := a.flags[key]
	if len(v) == 0 || (len(v) == 1 && v[0].bare) {
		return nil
	}
	out := make([]string, len(v))
	for i, x := range v {
		out[i] = x.v
	}
	return out
}

func (a *args) has(key string) bool {
	v := a.flags[key]
	return len(v) > 0 && !(len(v) == 1 && !v[0].bare && v[0].v == "")
}

func (a *args) pos1(i int) string {
	if i < len(a.pos) {
		return a.pos[i]
	}
	return ""
}

func (a *args) root() string {
	r, ok := a.str("root")
	if !ok {
		r = os.Getenv("PREPARE_DEV_ROOT")
	}
	if r == "" {
		r, _ = os.Getwd()
	}
	abs, err := filepath.Abs(r)
	if err != nil {
		return r
	}
	return abs
}

// ---------- output ----------

type exitError struct{ msg string }

func (e *exitError) Error() string { return e.msg }

func die(format string, args ...any) error { return &exitError{msg: fmt.Sprintf(format, args...)} }

func printOut(w io.Writer, v any) error {
	if s, ok := v.(string); ok {
		_, err := fmt.Fprintln(w, s)
		return err
	}
	data, err := core.PrettyJSON(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

func readBody(a *args) (string, error) {
	if f, ok := a.str("body-file"); ok {
		data, err := os.ReadFile(f)
		return string(data), err
	}
	b, _ := a.str("body")
	return b, nil
}

// Main runs the CLI and returns the exit code.
func Main(argv []string, stdout, stderr io.Writer) int {
	a := parseArgs(argv)
	err := run(a, stdout)
	if err == nil {
		return 0
	}
	var api *daemon.APIError
	if errors.As(err, &api) {
		msg := api.Code + ": " + api.Message
		if api.Details != nil {
			if data, e := core.MarshalJSONNoEscape(api.Details); e == nil && string(data) != "{}" {
				msg += " " + string(data)
			} else if e == nil {
				msg += " {}"
			}
		}
		fmt.Fprintln(stderr, "error: "+msg)
		return 1
	}
	fmt.Fprintln(stderr, "error: "+err.Error())
	return 1
}

const usage = `ppdev — 意図から判断・仕様・検証へ

ppdev init [--root dir] [--no-skills] [--skills-dir dir] [--replace-skills]
ppdev status [--json] [--root dir]
ppdev ui [Q-ID] [--no-open] [--root dir]
ppdev record create --kind requirement|spec|design|discovery|review|record|proposal --title text --body-file file [--fm-file json]
ppdev record get ID
ppdev record put ID --expected-rev N --expected-sha256 HASH [--body-file file] [--fm-file json]
ppdev record list [--kind kind]
ppdev q create --subject REQ-ID --prompt-file question.md [--options-file options.json]
  --kind single_choice|multi_choice|free_text|review (default: single_choice) [--no-free-text]
ppdev q apply Q-ID ANSWER-ID --title text --body-file file [--chosen key] [--alternative text]
ppdev q list|show|wait|pending|ack|apply|close|cancel ...
ppdev verify run --plugin gotest|vitest|lean4 --claim SPEC-ID/CLAIM --target path [--config-json json] --wait [--json]
ppdev verify wait|get RUN-ID [--json] | verify list [--json]
ppdev trace ID [--json]
ppdev serve [--detach] | stop
ppdev bundle RECORD-ID... [--stdout]
ppdev version [--json]

全操作で --root dir を指定できます。省略時は PREPARE_DEV_ROOT または現在のディレクトリ。
init はskillsをコピーします。prepare-devが入れて編集されていないskillは新しい版に置き換え、
編集されたskillは残して差異を出力します（--replace-skills で .prepare-dev/skills-backup に退避して置き換え）。
MCPの設定は作りません。回答は判断画面（ppdev ui）からだけ行います。
verify と trace は、入力ファイルの一覧を件数に、不一致と実行したテストの一覧を先頭の数件に縮めて出力します。
すべてを見るには --json を付けます。
検証器（lean・vitest・go など）は対象プロジェクトへ別途導入してください。`

func run(a *args, out io.Writer) error {
	cmd := a.pos1(0)
	if a.has("version") || cmd == "version" {
		id, err := daemon.Identity()
		if err != nil {
			return err
		}
		if a.has("json") {
			// the plugin versions let an agent tell whether the guidance in its skills
			// (references/<plugin>.md names the version it describes) is older than this build
			plugins := core.NewOMap()
			for _, p := range Plugins() {
				plugins.Set(p.Name(), p.Version())
			}
			return printOut(out, core.OM("version", id.Version, "build", id.Build, "path", id.Path, "root", id.Root, "mode", id.Mode, "plugins", plugins))
		}
		return printOut(out, id.Version)
	}
	if a.has("help") || cmd == "help" || cmd == "" {
		return printOut(out, usage)
	}
	switch cmd {
	case "serve":
		return cmdServe(a, out)
	case "init":
		return cmdInit(a, out)
	case "status":
		return cmdStatus(a, out)
	case "pending":
		c, err := client(a)
		if err != nil {
			return err
		}
		return getPrint(c, "/api/pending", out)
	case "q":
		return cmdQ(a, out)
	case "record":
		return cmdRecord(a, out)
	case "verify":
		return cmdVerify(a, out)
	case "trace":
		id := a.pos1(1)
		if id == "" {
			return die("trace <id>")
		}
		c, err := client(a)
		if err != nil {
			return err
		}
		v, err := c.Get("/api/trace/"+url.PathEscape(id), 0)
		if err != nil {
			return err
		}
		if a.has("json") {
			return printOut(out, v)
		}
		return printOut(out, compact(v))
	case "bundle":
		return cmdBundle(a, out)
	case "ui":
		return cmdUI(a, out)
	case "stop":
		ok, err := daemon.Stop(a.root())
		if err != nil {
			return err
		}
		if ok {
			return printOut(out, "daemon stopped")
		}
		return printOut(out, "no daemon running")
	}
	return die("usage: ppdev serve|init|status|pending|q|record|verify|trace|bundle|ui|stop|version [--root dir]")
}

func client(a *args) (*daemon.Client, error) {
	return daemon.Connect(a.root(), !a.has("no-start"))
}

func getPrint(c *daemon.Client, path string, out io.Writer) error {
	v, err := c.Get(path, 0)
	if err != nil {
		return err
	}
	return printOut(out, v)
}

// ---------- serve / init / ui ----------

func cmdServe(a *args, out io.Writer) error {
	root, err := daemon.CanonicalRoot(a.root())
	if err != nil {
		return err
	}
	if a.has("detach") {
		info, err := daemon.Ensure(root, true, 0)
		if err != nil {
			return err
		}
		return printOut(out, info)
	}
	id, err := daemon.Identity()
	if err != nil {
		return err
	}
	// the TypeScript version's lease is a TCP port, which the file lock does not exclude
	if err := daemon.CheckForeignDaemon(root); err != nil {
		return err
	}
	lease, err := daemon.AcquireLease(root)
	if err != nil {
		return err
	}
	rt, err := runtime.Open(root, runtime.Options{Plugins: Plugins()})
	if err != nil {
		lease.Release()
		return err
	}
	// every operation first checks that this daemon still holds the lease file and that
	// the journal is the one it read; when either changed, the daemon stops
	rt.Store.Guard = lease.Check
	crashAfter, _ := a.str("crash-after")
	if crashAfter == "" {
		crashAfter = os.Getenv("PREPARE_DEV_CRASH_AFTER")
	}
	port := 0
	if p, ok := a.str("port"); ok {
		port, _ = strconv.Atoi(p)
	}
	stopped := make(chan struct{})
	var srv *server.Server
	stop := func() {
		select {
		case <-stopped:
			return
		default:
		}
		srv.Close()
		lease.Release()
		close(stopped)
	}
	srv = server.New(rt, server.Options{Port: port, UIHTML: preparedev.UIHTML(), CrashAfter: crashAfter, Tool: &id, OnShutdown: stop})
	rt.Store.OnDead = func(reason string) {
		fmt.Fprintf(os.Stderr, "[ppdev] %s %s\n", core.NowISO(), reason)
		stop()
	}
	info, err := srv.Listen()
	if err != nil {
		lease.Release()
		return err
	}
	// a daemon whose lease file was removed stops, so that a new one can take over
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				err := lease.Check()
				if err == nil {
					err = rt.Store.CheckJournal()
				}
				if err != nil {
					fmt.Fprintf(os.Stderr, "[ppdev] %s %v\n", core.NowISO(), err)
					stop()
					return
				}
			case <-stopped:
				return
			}
		}
	}()
	shown := core.OM("pid", int64(info.PID), "port", int64(info.Port), "url", info.URL, "root", info.Root, "started_at", info.StartedAt, "tool", info.Tool)
	shown.Set("recovery", rt.Recovery)
	shown.Set("plugins", rt.Runs.PluginNames())
	if err := printOut(out, shown); err != nil {
		stop()
		return err
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sig:
		stop()
	case <-stopped:
	}
	return nil
}

func cmdInit(a *args, out io.Writer) error {
	root := a.root()
	stateDir := filepath.Join(root, ".prepare-dev")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	// keep the daemon's files out of commits, git clean -fd and git stash -u: removing
	// the lease while a daemon runs would let a second one start
	ignoreFile := filepath.Join(stateDir, ".gitignore")
	if f, err := os.OpenFile(ignoreFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644); err == nil {
		_, werr := f.WriteString("# files of the running ppdev daemon\ndaemon.json\ndaemon.log\nlease\nindex.json\ntmp/\nskills-backup/\n")
		cerr := f.Close()
		if werr != nil || cerr != nil {
			return errors.Join(werr, cerr)
		}
	} else if !errors.Is(err, os.ErrExist) {
		return err
	}
	cfgFile := filepath.Join(stateDir, "config.json")
	if f, err := os.OpenFile(cfgFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644); err == nil {
		cfg := core.OM("records_dir", "prepare", "wait", core.OM("default_timeout_ms", int64(50000)), "plugins", core.NewOMap())
		data, _ := core.PrettyJSON(cfg)
		_, werr := f.Write(append(data, '\n'))
		cerr := f.Close()
		if werr != nil || cerr != nil {
			return errors.Join(werr, cerr)
		}
	} else if !errors.Is(err, os.ErrExist) {
		return err
	}
	var dests []string
	switch {
	case a.has("no-skills"):
	case a.has("skills-dir"):
		for _, d := range a.list("skills-dir") {
			dests = append(dests, filepath.Join(root, d))
		}
	default:
		dests = []string{filepath.Join(root, ".agents", "skills"), filepath.Join(root, ".claude", "skills")}
	}
	res, err := skills.Copy(preparedev.Skills(), dests, root, skills.Options{Replace: a.has("replace-skills")})
	if err != nil {
		return err
	}
	info, err := daemon.Ensure(root, true, 0)
	if err != nil {
		return err
	}
	id, err := daemon.Identity()
	if err != nil {
		return err
	}
	return printOut(out, core.OM("root", info.Root, "initialized", true, "skills", res, "ui", info.URL, "tool", id))
}

func cmdUI(a *args, out io.Writer) error {
	info, err := daemon.Ensure(a.root(), true, 0)
	if err != nil {
		return err
	}
	u := info.URL
	if id := a.pos1(1); id != "" {
		u += "/q/" + url.PathEscape(id)
	}
	if err := printOut(out, u); err != nil {
		return err
	}
	if !a.has("no-open") && goruntime.GOOS == "darwin" {
		c := exec.Command("open", u)
		if err := c.Start(); err == nil {
			_ = c.Process.Release()
		}
	}
	return nil
}

// ---------- status ----------

func asMap(v any) *core.OMap {
	m, _ := v.(*core.OMap)
	if m == nil {
		return core.NewOMap()
	}
	return m
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func field(m *core.OMap, k string) any {
	v, _ := m.Get(k)
	return v
}

func fstr(m *core.OMap, k string) string {
	switch t := field(m, k).(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		data, _ := core.MarshalJSONNoEscape(t)
		return string(data)
	}
}

func jsonOf(v any) string {
	data, _ := core.MarshalJSONNoEscape(v)
	return string(data)
}

// strList reads a JSON array of strings.
func strList(v any) []string {
	out := []string{}
	for _, x := range asList(v) {
		if t, ok := x.(string); ok {
			out = append(out, t)
		}
	}
	return out
}

func cmdStatus(a *args, out io.Writer) error {
	c, err := client(a)
	if err != nil {
		return err
	}
	v, err := c.Get("/api/status", 0)
	if err != nil {
		return err
	}
	s := asMap(v)
	// the skills init installed are compared here and not in the daemon: the daemon is the
	// same build, and the comparison writes nothing
	plan, planErr := skills.Plan(preparedev.Skills(), fstr(s, "root"))
	if planErr != nil {
		s.Set("skills_error", ".prepare-dev/skills.json: "+planErr.Error())
	} else if plan != nil {
		s.Set("skills", plan)
	}
	if a.has("json") {
		return printOut(out, s)
	}
	pending := asList(field(s, "pending"))
	findings := asList(field(s, "open_findings"))
	waiting := asList(field(s, "waiting_questions"))
	problems := asList(field(s, "problems"))
	bySev := func(sev string) int {
		n := 0
		for _, f := range findings {
			if fstr(asMap(f), "severity") == sev {
				n++
			}
		}
		return n
	}
	var lines []string
	lines = append(lines, "root: "+fstr(s, "root"), fmt.Sprintf("pending answers: %d", len(pending)))
	for _, p := range pending {
		m := asMap(p)
		lines = append(lines, fmt.Sprintf("  %s %s stale=%s received=%s choice=%s", fstr(m, "question_id"), fstr(m, "answer_id"), jsonOf(field(m, "stale")), jsonOf(field(m, "received")), jsonOf(field(m, "choice"))))
	}
	lines = append(lines, fmt.Sprintf("inflight: %d", len(asList(field(s, "inflight")))))
	var stale []string
	for _, d := range asList(field(s, "stale_decisions")) {
		stale = append(stale, fstr(asMap(d), "id"))
	}
	if len(stale) == 0 {
		lines = append(lines, "stale decisions: none")
	} else {
		lines = append(lines, "stale decisions: "+strings.Join(stale, ", "))
	}
	notApp := asList(field(s, "runs_not_applicable"))
	cur := asList(field(s, "runs_current"))
	lines = append(lines, fmt.Sprintf("runs that decide the current results (the newest run of each plugin for each claim): %d apply, %d do not apply to the current versions; older runs kept as history: %s", len(cur), len(notApp), jsonOf(field(s, "runs_older"))))
	for _, r := range notApp {
		m := asMap(r)
		lines = append(lines, fmt.Sprintf("  %s %s (%d mismatches): no current evidence for %s until run again", fstr(m, "run_id"), fstr(m, "result"), len(asList(field(m, "mismatches"))), strings.Join(strList(field(m, "claims")), ", ")))
	}
	for _, r := range cur {
		m := asMap(r)
		if fstr(m, "result") != "verified" {
			lines = append(lines, fmt.Sprintf("  %s %s: the current result of %s", fstr(m, "run_id"), fstr(m, "result"), strings.Join(strList(field(m, "claims")), ", ")))
		}
	}
	if len(notApp) > 0 || len(cur) > 0 {
		lines = append(lines, "  the current result of each claim: ppdev trace <SPEC or REQ id> (current and missing)")
	}
	if inc := asList(field(s, "runs_incomplete")); len(inc) > 0 {
		var ids []string
		for _, x := range inc {
			if id, ok := x.(string); ok {
				ids = append(ids, id)
			}
		}
		lines = append(lines, fmt.Sprintf("runs started but never recorded: %d (%s); run them again", len(ids), strings.Join(ids, ", ")))
	}
	if planErr != nil {
		lines = append(lines, "skills: cannot compare with this build: .prepare-dev/skills.json: "+planErr.Error())
	}
	if plan != nil {
		if len(plan.Update) > 0 {
			lines = append(lines, "skills older than this build: "+strings.Join(plan.Update, ", ")+"; run ppdev init to update them")
		}
		if len(plan.Copy) > 0 {
			lines = append(lines, "skills of this build not installed: "+strings.Join(plan.Copy, ", ")+"; run ppdev init to copy them")
		}
		if len(plan.Leave) > 0 {
			lines = append(lines, "skills init leaves as they are (edited, or not installed by init): "+strings.Join(plan.Leave, ", ")+"; ppdev init --replace-skills backs them up and replaces them")
		}
		if len(plan.Manual) > 0 {
			lines = append(lines, "skills init does not touch even with --replace-skills (a symlink, a file, or outside the root): "+strings.Join(plan.Manual, ", ")+"; fix them by hand")
		}
	}
	lines = append(lines, fmt.Sprintf("jobs: %d", len(asList(field(s, "jobs")))), fmt.Sprintf("questions waiting for an answer: %d", len(waiting)))
	for _, q := range waiting {
		m := asMap(q)
		var subjects []string
		for _, x := range asList(field(m, "subjects")) {
			if s, ok := x.(string); ok {
				subjects = append(subjects, s)
			}
		}
		suffix := ""
		if b, _ := field(m, "subjects_current").(bool); !b {
			suffix = " subjects changed since asked"
		}
		lines = append(lines, fmt.Sprintf("  %s (%s)%s", fstr(m, "question_id"), strings.Join(subjects, ", "), suffix))
	}
	lines = append(lines, fmt.Sprintf("open review findings: %d (blocker %d, major %d, nit %d)", len(findings), bySev("blocker"), bySev("major"), bySev("nit")))
	for _, f := range findings {
		m := asMap(f)
		var subjects []string
		for _, x := range asList(field(m, "subjects")) {
			if s, ok := x.(string); ok {
				subjects = append(subjects, s)
			}
		}
		sum := fstr(m, "summary")
		if r := []rune(sum); len(r) > 70 {
			sum = string(r[:70]) + "…"
		}
		lines = append(lines, fmt.Sprintf("  %s/%s %s [%s] %s", fstr(m, "review_id"), fstr(m, "finding_id"), fstr(m, "severity"), strings.Join(subjects, ", "), sum))
	}
	if len(problems) > 0 {
		lines = append(lines, fmt.Sprintf("problems: %d", len(problems)))
		for _, p := range problems {
			m := asMap(p)
			lines = append(lines, fmt.Sprintf("  %s: %s", fstr(m, "path"), fstr(m, "message")))
		}
	}
	return printOut(out, strings.Join(lines, "\n"))
}

// ---------- questions ----------

func readJSONFile(path string) (any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return core.DecodeJSON(data)
}

func esc(s string) string { return url.PathEscape(s) }

func cmdQ(a *args, out io.Writer) error {
	sub, id, aid := a.pos1(1), a.pos1(2), a.pos1(3)
	valid := map[string]bool{"create": true, "list": true, "show": true, "wait": true, "pending": true, "ack": true, "apply": true, "close": true, "cancel": true}
	if sub == "answer" {
		// answers come from the person in the decision UI, never from the agent's CLI
		return die("questions are answered in the decision UI (ppdev ui %s)", id)
	}
	if !valid[sub] {
		return die("usage: q create|list|show|wait|pending|ack|apply|close|cancel")
	}
	c, err := client(a)
	if err != nil {
		return err
	}
	var v any
	switch sub {
	case "create":
		var options any = []any{}
		if f, ok := a.str("options-file"); ok {
			if options, err = readJSONFile(f); err != nil {
				return err
			}
		} else if j, ok := a.str("options-json"); ok {
			if options, err = core.DecodeJSON([]byte(j)); err != nil {
				return err
			}
		} else {
			list := []any{}
			for _, o := range a.list("option") {
				key, rest, _ := strings.Cut(o, "=")
				label, flag, _ := strings.Cut(rest, ":")
				if i := strings.IndexByte(flag, ':'); i >= 0 {
					flag = flag[:i]
				}
				if label == "" {
					label = key
				}
				list = append(list, core.OM("key", key, "label", label, "recommended", flag == "recommended"))
			}
			options = list
		}
		var prompt string
		if f, ok := a.str("prompt-file"); ok {
			data, err := os.ReadFile(f)
			if err != nil {
				return err
			}
			prompt = string(data)
		} else if p, ok := a.str("prompt"); ok {
			prompt = p
		} else {
			return die("--prompt or --prompt-file is required")
		}
		kind, ok := a.str("kind")
		if !ok {
			kind = "single_choice"
		}
		subjects := []any{}
		for _, s := range a.list("subject") {
			subjects = append(subjects, s)
		}
		v, err = c.Post("/api/questions", core.OM("kind", kind, "prompt", prompt, "subjects", subjects, "options", options, "allow_free_text", !a.has("no-free-text"), "created_by", core.OM("host", "cli")))
	case "list":
		v, err = c.Get("/api/questions", 0)
	case "show":
		v, err = c.Get("/api/questions/"+esc(id), 0)
	case "wait":
		t := int64(50000)
		if s, ok := a.str("timeout"); ok {
			t, _ = strconv.ParseInt(s, 10, 64)
		}
		v, err = c.Get(fmt.Sprintf("/api/questions/%s/wait?timeout_ms=%d", esc(id), t), time.Duration(t+10000)*time.Millisecond)
	case "pending":
		v, err = c.Get("/api/pending", 0)
	case "ack":
		v, err = c.Post("/api/questions/"+esc(id)+"/answers/"+esc(aid)+"/ack", core.NewOMap())
	case "apply":
		title, ok := a.str("title")
		if !ok {
			return die("--title is required")
		}
		body, err := readBody(a)
		if err != nil {
			return err
		}
		d := core.OM("title", title, "body", body)
		if ch, ok := a.str("chosen"); ok {
			d.Set("chosen", ch)
		}
		alts := []any{}
		for _, x := range a.list("alternative") {
			alts = append(alts, x)
		}
		d.Set("alternatives", alts)
		req := core.OM("decision", d)
		if r, ok := a.str("expected-rev"); ok {
			n, err := strconv.ParseInt(r, 10, 64)
			if err != nil {
				return die("--expected-rev must be a number")
			}
			req.Set("expected_question_rev", n)
		}
		v, err = c.Post("/api/questions/"+esc(id)+"/answers/"+esc(aid)+"/apply", req)
		if err != nil {
			return err
		}
	case "close":
		reason, ok := a.str("reason")
		if !ok {
			return die("--reason is required")
		}
		v, err = c.Post("/api/questions/"+esc(id)+"/answers/"+esc(aid)+"/close", core.OM("reason", reason))
	case "cancel":
		reason, ok := a.str("reason")
		if !ok {
			return die("--reason is required")
		}
		var replaced any
		if r, ok := a.str("replaced-by"); ok {
			replaced = r
		}
		v, err = c.Post("/api/questions/"+esc(id)+"/cancel", core.OM("reason", reason, "replaced_by", replaced))
	}
	if err != nil {
		return err
	}
	return printOut(out, v)
}

// ---------- records ----------

func cmdRecord(a *args, out io.Writer) error {
	sub, id := a.pos1(1), a.pos1(2)
	if sub != "list" && sub != "get" && sub != "create" && sub != "put" {
		return die("usage: record list|get|create|put")
	}
	fm := core.NewOMap()
	if f, ok := a.str("fm-file"); ok {
		v, err := readJSONFile(f)
		if err != nil {
			return err
		}
		m, ok := v.(*core.OMap)
		if !ok {
			return die("--fm-file must contain a JSON object")
		}
		fm = m
	}
	for _, kv := range a.list("fm") {
		k, v, _ := strings.Cut(kv, "=")
		if k == "" {
			continue
		}
		if v != "" && (strings.HasPrefix(v, "[") || strings.HasPrefix(v, "{")) {
			parsed, err := core.DecodeJSON([]byte(v))
			if err != nil {
				return die("--fm %s: %v", k, err)
			}
			fm.Set(k, parsed)
		} else {
			fm.Set(k, v)
		}
	}
	var (
		expectedRev int64
		expectedSHA string
	)
	if sub == "put" {
		r, ok1 := a.str("expected-rev")
		s, ok2 := a.str("expected-sha256")
		if !ok1 || !ok2 {
			return die("record put requires --expected-rev and --expected-sha256 from record get")
		}
		n, err := strconv.ParseInt(r, 10, 64)
		if err != nil {
			return die("--expected-rev must be a number")
		}
		expectedRev, expectedSHA = n, s
	}
	var kind, title string
	if sub == "create" {
		var ok bool
		if kind, ok = a.str("kind"); !ok {
			return die("--kind")
		}
		if title, ok = a.str("title"); !ok {
			return die("--title")
		}
	}
	c, err := client(a)
	if err != nil {
		return err
	}
	var v any
	switch sub {
	case "list":
		path := "/api/records"
		if k, ok := a.str("kind"); ok {
			path += "?kind=" + url.QueryEscape(k)
		}
		v, err = c.Get(path, 0)
	case "get":
		v, err = c.Get("/api/records/"+esc(id), 0)
	case "create":
		body, rerr := readBody(a)
		if rerr != nil {
			return rerr
		}
		v, err = c.Post("/api/records", core.OM("kind", kind, "title", title, "frontmatter", fm, "body", body))
	case "put":
		var body string
		if a.has("body-file") || a.has("body") {
			if body, err = readBody(a); err != nil {
				return err
			}
		} else {
			cur, err := c.Get("/api/records/"+esc(id), 0)
			if err != nil {
				return err
			}
			body = fstr(asMap(cur), "body")
		}
		v, err = c.Put("/api/records/"+esc(id), core.OM("frontmatter", fm, "body", body, "expected_rev", expectedRev, "expected_sha256", expectedSHA))
	}
	if err != nil {
		return err
	}
	return printOut(out, v)
}

// ---------- verification ----------

func cmdVerify(a *args, out io.Writer) error {
	sub, id := a.pos1(1), a.pos1(2)
	if sub != "run" && sub != "wait" && sub != "get" && sub != "list" {
		return die("usage: verify run|wait|get|list")
	}
	timeout := int64(600000)
	if s, ok := a.str("timeout"); ok {
		timeout, _ = strconv.ParseInt(s, 10, 64)
	}
	var claims []any
	var config any = core.NewOMap()
	var pluginName string
	if sub == "run" {
		for _, s := range a.list("claim") {
			spec, claim, _ := strings.Cut(s, "/")
			if spec == "" || claim == "" || strings.Contains(claim, "/") {
				return die("--claim must be SPEC-ID/CLAIM, got %s", s)
			}
			claims = append(claims, core.OM("spec", spec, "claim", claim))
		}
		if j, ok := a.str("config-json"); ok {
			cfg, err := core.DecodeJSON([]byte(j))
			if err != nil {
				return die("--config-json: %v", err)
			}
			config = cfg
		}
		var ok bool
		if pluginName, ok = a.str("plugin"); !ok {
			return die("--plugin")
		}
	}
	c, err := client(a)
	if err != nil {
		return err
	}
	var v any
	switch sub {
	case "run":
		targets := []any{}
		for _, t := range a.list("target") {
			targets = append(targets, t)
		}
		if claims == nil {
			claims = []any{}
		}
		job, err := c.Post("/api/runs", core.OM("plugin", pluginName, "claims", claims, "targets", targets, "config", config))
		if err != nil {
			return err
		}
		if !a.has("wait") {
			return printOut(out, job)
		}
		v, err = c.Get(fmt.Sprintf("/api/runs/%s/wait?timeout_ms=%d", esc(fstr(asMap(job), "run_id")), timeout), time.Duration(timeout+10000)*time.Millisecond)
		if err != nil {
			return err
		}
	case "wait":
		v, err = c.Get(fmt.Sprintf("/api/runs/%s/wait?timeout_ms=%d", esc(id), timeout), time.Duration(timeout+10000)*time.Millisecond)
	case "get":
		v, err = c.Get("/api/runs/"+esc(id), 0)
	case "list":
		v, err = c.Get("/api/runs", 0)
	}
	if err != nil {
		return err
	}
	if a.has("json") {
		return printOut(out, v)
	}
	if sub == "list" {
		list := []any{}
		for _, r := range asList(v) {
			list = append(list, runSummary(asMap(r)))
		}
		return printOut(out, list)
	}
	return printOut(out, compact(v))
}

// compactLimit is how many entries of a long list compact keeps.
const compactLimit = 5

// compact shortens what grows with the size of the verified project, for agents that
// read the output: the input file list of a run becomes its count, and mismatch and
// covered-test lists keep their first entries plus the number left out.
func compact(v any) any {
	switch t := v.(type) {
	case *core.OMap:
		out := core.NewOMap()
		for _, k := range t.Keys() {
			x, _ := t.Get(k)
			l, isList := x.([]any)
			switch {
			case k == "files" && isList:
				out.Set("files_count", int64(len(l)))
			case (k == "mismatches" || k == "covered") && isList && len(l) > compactLimit:
				out.Set(k, compact(l[:compactLimit]))
				out.Set(k+"_omitted", int64(len(l)-compactLimit))
			default:
				out.Set(k, compact(x))
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = compact(x)
		}
		return out
	}
	return v
}

// runSummary is one line of verify list: the run, its result per claim, and whether it
// still applies to the current files.
func runSummary(r *core.OMap) *core.OMap {
	claims := []any{}
	for _, c := range asList(field(r, "claims")) {
		m := asMap(c)
		claims = append(claims, fstr(asMap(field(m, "spec")), "id")+"/"+fstr(m, "claim")+": "+fstr(m, "result"))
	}
	app := asMap(field(r, "applicability"))
	return core.OM("id", field(r, "id"), "plugin", fstr(asMap(field(r, "plugin")), "name"), "result", field(r, "result"),
		"claims", claims, "finished_at", fstr(asMap(field(r, "environment")), "finished_at"),
		"applicable", field(app, "applicable"), "mismatches", int64(len(asList(field(app, "mismatches")))))
}

func cmdBundle(a *args, out io.Writer) error {
	ids := a.pos[1:]
	if len(ids) == 0 {
		return die("bundle <id...> [--stdout] [--no-expand] [--title t]")
	}
	q := url.Values{}
	q.Set("ids", strings.Join(ids, ","))
	if a.has("stdout") {
		q.Set("out", "0")
	} else {
		q.Set("out", "1")
	}
	if a.has("no-expand") {
		q.Set("expand", "0")
	} else {
		q.Set("expand", "1")
	}
	if t, ok := a.str("title"); ok {
		q.Set("title", t)
	}
	c, err := client(a)
	if err != nil {
		return err
	}
	v, err := c.Get("/api/bundle?"+q.Encode(), 0)
	if err != nil {
		return err
	}
	m := asMap(v)
	if a.has("stdout") {
		return printOut(out, fstr(m, "markdown"))
	}
	return printOut(out, core.OM("inputs", field(m, "inputs"), "path", field(m, "path")))
}
