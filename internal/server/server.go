// Package server is the HTTP API, the SSE stream and the decision UI of a daemon.
package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wim-web/prepare-dev/internal/core"
	"github.com/wim-web/prepare-dev/internal/plugin"
	"github.com/wim-web/prepare-dev/internal/runtime"
	"github.com/wim-web/prepare-dev/internal/store"
)

// Options configure a Server.
type Options struct {
	Port int
	Host string
	// UIHTML is the built single-file decision UI; nil serves a fallback page with 503.
	UIHTML []byte
	// CrashAfter makes the process exit right after this apply stage (end-to-end crash tests).
	CrashAfter string
	Tool       *core.ToolIdentity
	OnShutdown func()
}

// DaemonInfo is written to .prepare-dev/daemon.json.
type DaemonInfo struct {
	PID           int                `json:"pid"`
	Port          int                `json:"port"`
	URL           string             `json:"url"`
	Root          string             `json:"root"`
	StartedAt     string             `json:"started_at"`
	Tool          *core.ToolIdentity `json:"tool,omitempty"`
	ShutdownToken string             `json:"shutdown_token,omitempty"`
}

type handler func(w http.ResponseWriter, r *http.Request, p map[string]string) error

type sseClient struct {
	ch   chan []byte
	done chan struct{}
	once sync.Once
}

func (c *sseClient) close() { c.once.Do(func() { close(c.done) }) }

// Server serves one runtime.
type Server struct {
	rt         *runtime.Runtime
	opts       Options
	token      string
	stopping   atomic.Bool
	mux        *http.ServeMux
	info       *DaemonInfo
	httpSrv    *http.Server
	DaemonFile string

	sseMu   sync.Mutex
	clients map[*sseClient]bool
	stopHB  chan struct{}
	offs    []func()
}

// New builds a server and subscribes to the runtime notifications.
func New(rt *runtime.Runtime, opts Options) *Server {
	if opts.Host == "" {
		opts.Host = "127.0.0.1"
	}
	s := &Server{rt: rt, opts: opts, token: core.RandomUUID(), clients: map[*sseClient]bool{}, DaemonFile: filepath.Join(rt.Store.StateDir, "daemon.json"), mux: http.NewServeMux()}
	s.defineRoutes()
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_ = writeJSON(w, 404, core.OM("error", "not_found", "message", fmt.Sprintf("no route %s %s", r.Method, r.URL.EscapedPath())))
	})
	s.offs = append(s.offs, rt.Store.Emitter.On(func(name string, payload any) { s.broadcast(name, payload) }))
	return s
}

// Info is the daemon info after Listen.
func (s *Server) Info() *DaemonInfo { return s.info }

// ---------- routing ----------

var paramRE = regexp.MustCompile(`:([a-zA-Z_]+)`)

// add registers a route on the standard ServeMux; ":name" segments become wildcards
// and reach the handler decoded, in p.
func (s *Server) add(method, pattern string, h handler) {
	var keys []string
	muxPattern := paramRE.ReplaceAllStringFunc(pattern, func(m string) string {
		keys = append(keys, m[1:])
		return "{" + m[1:] + "}"
	})
	if muxPattern == "/" {
		muxPattern = "/{$}"
	}
	s.mux.HandleFunc(method+" "+muxPattern, func(w http.ResponseWriter, r *http.Request) {
		p := map[string]string{}
		for _, k := range keys {
			p[k] = r.PathValue(k)
		}
		if err := h(w, r, p); err != nil {
			s.writeError(w, r, err)
		}
	})
}

func (s *Server) locked(fn func() (int, any, error)) (int, any, error) {
	s.rt.Lock()
	defer s.rt.Unlock()
	return fn()
}

func (s *Server) defineRoutes() {
	rt := s.rt
	q := rt.Questions

	s.add("GET", "/api/identity", func(w http.ResponseWriter, r *http.Request, p map[string]string) error {
		started := ""
		if s.info != nil {
			started = s.info.StartedAt
		}
		var tool any
		if s.opts.Tool != nil {
			tool = s.opts.Tool
		}
		return writeJSON(w, 200, core.OM("pid", int64(os.Getpid()), "root", rt.Root(), "started_at", started, "tool", tool))
	})
	s.add("POST", "/api/shutdown", func(w http.ResponseWriter, r *http.Request, p map[string]string) error {
		if r.Header.Get("Authorization") != "Bearer "+s.token {
			return core.Errf("forbidden", "invalid shutdown token")
		}
		if s.opts.OnShutdown == nil {
			return core.Errf("unsupported", "shutdown is not managed by this server")
		}
		if rt.Runs.Running() {
			return core.Errf("conflict", "verification is running; wait for it to finish before stopping")
		}
		s.stopping.Store(true)
		if err := writeJSON(w, 200, core.OM("stopping", true)); err != nil {
			return err
		}
		go func() {
			time.Sleep(10 * time.Millisecond)
			s.opts.OnShutdown()
		}()
		return nil
	})

	s.add("GET", "/api/status", s.handle(func(r *http.Request, p map[string]string) (int, any, error) {
		v, err := rt.Status.View()
		return 200, v, err
	}))

	s.add("GET", "/api/records", s.handle(func(r *http.Request, p map[string]string) (int, any, error) {
		list, err := rt.Store.ListRecords(r.URL.Query().Get("kind"))
		if err != nil {
			return 0, nil, err
		}
		out := []any{}
		for _, rec := range list {
			if rec.Kind == "decision" && !rt.Trace.IsPublic(rec) {
				continue
			}
			title, _ := rec.Frontmatter.Get("title")
			status, _ := rec.Frontmatter.Get("status")
			out = append(out, core.OM("id", rec.ID, "kind", rec.Kind, "title", title, "status", status, "rev", rec.Rev, "sha256", rec.SHA256, "path", rec.Path))
		}
		return 200, out, nil
	}))

	s.add("GET", "/api/records/:id", s.handle(func(r *http.Request, p map[string]string) (int, any, error) {
		rec, err := rt.Store.ReadRecord(p["id"])
		if err != nil {
			return 0, nil, err
		}
		if rec.Kind == "decision" && !rt.Trace.IsPublic(rec) {
			return 0, nil, core.Errf("not_found", "decision "+p["id"]+" is provisional")
		}
		return 200, rec, nil
	}))

	s.add("POST", "/api/records", s.handleBody(func(r *http.Request, p map[string]string, b *body) (int, any, error) {
		kind := b.str("kind")
		title := b.str("title")
		fm := b.recordDef("frontmatter")
		text := b.strDef("body", "")
		if err := b.err(); err != nil {
			return 0, nil, err
		}
		if err := forbidAnswerDecision(fm, kind); err != nil {
			return 0, nil, err
		}
		if !core.IsRecordKind(kind) {
			return 0, nil, core.Errf("invalid", "invalid record kind "+kind, "kind", kind)
		}
		fm = fm.Clone()
		fm.Set("title", title)
		rec, err := rt.Store.CreateRecord(kind, fm, text)
		return 201, rec, err
	}))

	s.add("PUT", "/api/records/:id", s.handleBody(func(r *http.Request, p map[string]string, b *body) (int, any, error) {
		fm := b.recordDef("frontmatter")
		text := b.optStr("body")
		rev := b.int("expected_rev")
		sha := b.str("expected_sha256")
		if err := b.err(); err != nil {
			return 0, nil, err
		}
		cur, err := rt.Store.ReadRecord(p["id"])
		if err != nil {
			return 0, nil, err
		}
		if err := forbidAnswerDecision(cur.Frontmatter.Merge(fm), cur.Kind); err != nil {
			return 0, nil, err
		}
		if tx, ok := cur.Frontmatter.Get("txid"); ok && tx != nil && tx != "" {
			return 0, nil, core.Errf("forbidden", "decisions created from answers are confirmed through question_apply only")
		}
		bodyText := cur.Body
		if text != nil {
			bodyText = *text
		}
		rec, err := rt.Store.WriteRecord(p["id"], fm, bodyText, &store.ExpectedVersion{Rev: rev, SHA256: sha})
		return 200, rec, err
	}))

	s.add("GET", "/api/questions", s.handle(func(r *http.Request, p map[string]string) (int, any, error) {
		list, err := q.List()
		if err != nil {
			return 0, nil, err
		}
		out := []*core.Question{}
		needs := r.URL.Query().Get("needs_answer") == "1"
		for _, x := range list {
			if needs {
				if x.State == "cancelled" {
					continue
				}
				active := false
				for _, a := range x.Answers {
					if a.Closed == nil && !a.Stale {
						active = true
					}
				}
				if active {
					continue
				}
			}
			out = append(out, x)
		}
		return 200, out, nil
	}))

	s.add("GET", "/api/pending", s.handle(func(r *http.Request, p map[string]string) (int, any, error) {
		v, err := q.Pending()
		return 200, v, err
	}))

	s.add("POST", "/api/questions", s.handleBody(func(r *http.Request, p map[string]string, b *body) (int, any, error) {
		b.strict("kind", "prompt", "subjects", "options", "allow_free_text", "created_by")
		in := runtime.CreateQuestionInput{
			Kind:     b.enum("kind", core.QuestionKinds...),
			Prompt:   b.str("prompt"),
			Subjects: b.strings("subjects"),
			Options:  b.options("options"),
		}
		allow := b.boolDef("allow_free_text", true)
		in.AllowFreeText = &allow
		in.CreatedBy = b.createdBy("created_by")
		if err := b.err(); err != nil {
			return 0, nil, err
		}
		created, err := q.Create(in)
		if err != nil {
			return 0, nil, err
		}
		return 201, core.OM("question", created, "url", s.QuestionURL(created.ID)), nil
	}))

	s.add("GET", "/api/questions/:id", s.handle(func(r *http.Request, p map[string]string) (int, any, error) {
		v, err := q.Read(p["id"])
		return 200, v, err
	}))

	s.add("POST", "/api/questions/:id/answers", s.handleBody(func(r *http.Request, p map[string]string, b *body) (int, any, error) {
		// answers are the person's, given in the decision UI: a browser request from this
		// daemon's own page always carries its Origin (checked in serveHTTP); the CLI has
		// no answer command
		if r.Header.Get("Origin") == "" {
			return 0, nil, core.Errf("forbidden", "answers are accepted from the decision UI only")
		}
		choice := b.choice("choice")
		text := b.nullableStr("text")
		if err := b.err(); err != nil {
			return 0, nil, err
		}
		_, a, err := q.Answer(p["id"], runtime.AnswerInput{Choice: choice, Text: text})
		if err != nil {
			return 0, nil, err
		}
		pub, err := q.Read(p["id"])
		if err != nil {
			return 0, nil, err
		}
		return 201, core.OM("question", pub, "answer", a), nil
	}))

	s.add("GET", "/api/questions/:id/wait", func(w http.ResponseWriter, r *http.Request, p map[string]string) error {
		timeout := clampTimeout(r.URL.Query().Get("timeout_ms"), rt.Store.Config.Wait.DefaultTimeoutMS)
		res, err := rt.WaitQuestion(r.Context(), p["id"], timeout)
		if err != nil {
			return err
		}
		return writeJSON(w, 200, res)
	})

	s.add("POST", "/api/questions/:id/answers/:aid/ack", s.handle(func(r *http.Request, p map[string]string) (int, any, error) {
		v, err := q.Ack(p["id"], p["aid"])
		return 200, v, err
	}))

	s.add("POST", "/api/questions/:id/answers/:aid/apply", s.handleBody(func(r *http.Request, p map[string]string, b *body) (int, any, error) {
		d := b.decision("decision")
		expected := b.optInt("expected_question_rev")
		if err := b.err(); err != nil {
			return 0, nil, err
		}
		var hooks *runtime.ApplyHooks
		crashAfter := s.opts.CrashAfter
		if crashAfter == "" {
			crashAfter = os.Getenv("PREPARE_DEV_CRASH_AFTER")
		}
		if crashAfter != "" {
			hooks = &runtime.ApplyHooks{AfterStage: func(stage string) error {
				if stage == crashAfter {
					// simulate the daemon dying right after this persistence stage
					fmt.Fprintf(os.Stderr, "[prepare-dev] simulated crash after stage %s\n", stage)
					os.Exit(42)
				}
				return nil
			}}
		}
		v, err := q.Apply(p["id"], p["aid"], d, expected, hooks)
		return 200, v, err
	}))

	s.add("POST", "/api/questions/:id/cancel", s.handleBody(func(r *http.Request, p map[string]string, b *body) (int, any, error) {
		reason := b.str("reason")
		replaced := b.nullableStr("replaced_by")
		if err := b.err(); err != nil {
			return 0, nil, err
		}
		v, err := q.Cancel(p["id"], reason, replaced)
		return 200, v, err
	}))

	s.add("POST", "/api/questions/:id/answers/:aid/close", s.handleBody(func(r *http.Request, p map[string]string, b *body) (int, any, error) {
		reason := b.str("reason")
		if err := b.err(); err != nil {
			return 0, nil, err
		}
		v, err := q.Close(p["id"], p["aid"], reason)
		return 200, v, err
	}))

	s.add("GET", "/api/runs", s.handle(func(r *http.Request, p map[string]string) (int, any, error) {
		list, err := rt.Runs.List()
		if err != nil {
			return 0, nil, err
		}
		out := []any{}
		for _, run := range list {
			v, err := withField(run, "applicability", rt.Runs.Applicability(run))
			if err != nil {
				return 0, nil, err
			}
			out = append(out, v)
		}
		return 200, out, nil
	}))

	s.add("POST", "/api/runs", s.handleBody(func(r *http.Request, p map[string]string, b *body) (int, any, error) {
		in := runtime.StartRunInput{Plugin: b.str("plugin"), Claims: b.claims("claims"), Targets: b.stringsDef("targets"), Config: b.recordDef("config")}
		if err := b.err(); err != nil {
			return 0, nil, err
		}
		v, err := rt.Runs.Start(in)
		return 202, v, err
	}))

	s.add("GET", "/api/runs/:id", s.handle(func(r *http.Request, p map[string]string) (int, any, error) {
		run, err := rt.Runs.Get(p["id"])
		if err != nil {
			return 0, nil, err
		}
		v, err := withField(run, "applicability", rt.Runs.Applicability(run))
		return 200, v, err
	}))

	s.add("GET", "/api/runs/:id/wait", func(w http.ResponseWriter, r *http.Request, p map[string]string) error {
		res, err := rt.Runs.Wait(p["id"], clampTimeout(r.URL.Query().Get("timeout_ms"), 50000))
		if err != nil {
			return err
		}
		var app any
		if res.Record != nil {
			rt.Lock()
			app = rt.Runs.Applicability(res.Record)
			rt.Unlock()
		}
		v, err := withField(res, "applicability", app)
		if err != nil {
			return err
		}
		return writeJSON(w, 200, v)
	})

	s.add("GET", "/api/trace/:id", s.handle(func(r *http.Request, p map[string]string) (int, any, error) {
		v, err := rt.Trace.For(p["id"])
		return 200, v, err
	}))

	s.add("GET", "/api/bundle", s.handle(func(r *http.Request, p map[string]string) (int, any, error) {
		qv := r.URL.Query()
		var ids []string
		for _, x := range strings.Split(qv.Get("ids"), ",") {
			if x = strings.TrimSpace(x); x != "" {
				ids = append(ids, x)
			}
		}
		v, err := rt.Trace.BuildBundle(ids, runtime.BundleOptions{Out: qv.Get("out") == "1", Expand: qv.Get("expand") != "0", Title: qv.Get("title")})
		return 200, v, err
	}))

	s.add("GET", "/api/plugins", s.handle(func(r *http.Request, p map[string]string) (int, any, error) {
		return 200, rt.Runs.PluginNames(), nil
	}))

	s.add("GET", "/events", func(w http.ResponseWriter, r *http.Request, p map[string]string) error {
		s.serveSSE(w, r)
		return nil
	})
	s.add("GET", "/", func(w http.ResponseWriter, r *http.Request, p map[string]string) error { return s.ui(w) })
	s.add("GET", "/q/:id", func(w http.ResponseWriter, r *http.Request, p map[string]string) error { return s.ui(w) })
}

func (s *Server) handle(fn func(r *http.Request, p map[string]string) (int, any, error)) handler {
	return func(w http.ResponseWriter, r *http.Request, p map[string]string) error {
		status, v, err := s.locked(func() (int, any, error) { return fn(r, p) })
		if err != nil {
			return err
		}
		return writeJSON(w, status, v)
	}
}

func (s *Server) handleBody(fn func(r *http.Request, p map[string]string, b *body) (int, any, error)) handler {
	return func(w http.ResponseWriter, r *http.Request, p map[string]string) error {
		raw, err := s.readBody(r)
		if err != nil {
			return err
		}
		b := &body{m: raw}
		status, v, err := s.locked(func() (int, any, error) { return fn(r, p, b) })
		if err != nil {
			return err
		}
		return writeJSON(w, status, v)
	}
}

func clampTimeout(raw string, def int64) time.Duration {
	ms := def
	if raw != "" {
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			ms = int64(f)
		}
	}
	if ms < 0 {
		ms = 0
	}
	if ms > 3_600_000 {
		ms = 3_600_000
	}
	return time.Duration(ms) * time.Millisecond
}

// QuestionURL is the UI address of a question.
func (s *Server) QuestionURL(id string) string {
	base := "http://127.0.0.1"
	if s.info != nil {
		base = s.info.URL
	}
	return base + "/q/" + id
}

func forbidAnswerDecision(fm *core.OMap, kind string) error {
	if kind != "decision" {
		return nil
	}
	for _, k := range []string{"answer", "txid", "question"} {
		if v, ok := fm.Get(k); ok && truthy(v) {
			return core.Errf("forbidden", "decisions derived from an answer are confirmed through question_apply only")
		}
	}
	return nil
}

func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return t != ""
	case bool:
		return t
	case int64:
		return t != 0
	case float64:
		return t != 0
	}
	return true
}

func withField(v any, key string, val any) (*core.OMap, error) {
	m, err := core.ToOMapValue(v)
	if err != nil {
		return nil, err
	}
	om, ok := m.(*core.OMap)
	if !ok {
		return nil, fmt.Errorf("not an object")
	}
	conv, err := core.ToOMapValue(val)
	if err != nil {
		return nil, err
	}
	om.Set(key, conv)
	return om, nil
}

// ---------- SSE ----------

func (s *Server) serveSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		_ = writeJSON(w, 500, core.OM("error", "internal", "message", "streaming unsupported"))
		return
	}
	h := w.Header()
	h.Set("content-type", "text/event-stream")
	h.Set("cache-control", "no-cache")
	h.Set("connection", "keep-alive")
	w.WriteHeader(200)
	hello, _ := core.MarshalJSONNoEscape(core.OM("ts", core.NowISO()))
	fmt.Fprintf(w, "event: hello\ndata: %s\n\n", hello)
	flusher.Flush()
	c := &sseClient{ch: make(chan []byte, 256), done: make(chan struct{})}
	s.sseMu.Lock()
	s.clients[c] = true
	s.sseMu.Unlock()
	defer func() {
		s.sseMu.Lock()
		delete(s.clients, c)
		s.sseMu.Unlock()
	}()
	for {
		select {
		case msg := <-c.ch:
			if _, err := w.Write(msg); err != nil {
				return
			}
			flusher.Flush()
		case <-c.done:
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) broadcast(name string, payload any) {
	data, err := core.MarshalJSONNoEscape(payload)
	if err != nil {
		return
	}
	msg := []byte("event: " + name + "\ndata: " + string(data) + "\n\n")
	s.sseMu.Lock()
	defer s.sseMu.Unlock()
	for c := range s.clients {
		select {
		case c.ch <- msg:
		default:
			c.close() // too slow; the client reconnects and refetches
		}
	}
}

// ---------- UI ----------

const fallbackHTML = `<!doctype html><html lang="ja"><meta charset="utf-8"><title>prepare-dev</title><body><h1>判断用UIを読み込めません</h1><p>配布物を入れ直してください。開発環境では画面をビルドしてから prepare-dev を作り直してください。</p></body></html>`

func (s *Server) ui(w http.ResponseWriter) error {
	if len(s.opts.UIHTML) > 0 {
		w.Header().Set("content-type", "text/html; charset=utf-8")
		w.WriteHeader(200)
		_, err := w.Write(s.opts.UIHTML)
		return err
	}
	w.Header().Set("content-type", "text/html; charset=utf-8")
	w.WriteHeader(503)
	_, err := io.WriteString(w, fallbackHTML)
	return err
}

// ---------- lifecycle ----------

// Listen binds 127.0.0.1 (an ephemeral port unless Port is set), writes daemon.json and
// starts serving.
func (s *Server) Listen() (*DaemonInfo, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort(s.opts.Host, strconv.Itoa(s.opts.Port)))
	if err != nil {
		return nil, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	s.info = &DaemonInfo{
		PID: os.Getpid(), Port: port, URL: fmt.Sprintf("http://%s:%d", s.opts.Host, port), Root: s.rt.Root(),
		StartedAt: core.NowISO(), Tool: s.opts.Tool, ShutdownToken: s.token,
	}
	data, err := core.PrettyJSON(s.info)
	if err != nil {
		ln.Close()
		return nil, err
	}
	if err := store.WriteFileAtomic(s.DaemonFile, append(data, '\n')); err != nil {
		ln.Close()
		return nil, err
	}
	s.httpSrv = &http.Server{Handler: http.HandlerFunc(s.serveHTTP), ReadHeaderTimeout: 30 * time.Second}
	s.stopHB = make(chan struct{})
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				s.broadcast("ping", core.OM("ts", core.NowISO()))
			case <-s.stopHB:
				return
			}
		}
	}()
	go func() { _ = s.httpSrv.Serve(ln) }()
	return s.info, nil
}

// Close stops serving, ends SSE streams and removes daemon.json if it is ours.
func (s *Server) Close() {
	s.stopping.Store(true)
	if s.stopHB != nil {
		select {
		case <-s.stopHB:
		default:
			close(s.stopHB)
		}
	}
	for _, off := range s.offs {
		off()
	}
	s.sseMu.Lock()
	for c := range s.clients {
		c.close()
	}
	s.sseMu.Unlock()
	if data, err := os.ReadFile(s.DaemonFile); err == nil {
		var cur DaemonInfo
		if v, err := core.DecodeJSON(data); err == nil {
			if m, ok := v.(*core.OMap); ok {
				if pid, ok := m.Get("pid"); ok {
					if n, ok := core.AsInt(pid); ok {
						cur.PID = int(n)
					}
				}
			}
		}
		if cur.PID == os.Getpid() {
			_ = os.Remove(s.DaemonFile)
		}
	}
	if s.httpSrv != nil {
		_ = s.httpSrv.Close()
	}
}

func corsHeaders(h http.Header) {
	h.Set("access-control-allow-methods", "GET,POST,PUT,OPTIONS")
	h.Set("access-control-allow-headers", "content-type")
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	port := 0
	if s.info != nil {
		port = s.info.Port
	}
	host := r.Host
	allowed := host == fmt.Sprintf("127.0.0.1:%d", port) || host == fmt.Sprintf("localhost:%d", port)
	if origin := r.Header.Get("Origin"); !allowed || (origin != "" && origin != "http://"+host) {
		_ = writeJSON(w, 403, core.OM("error", "forbidden", "message", "request must originate from this local UI"))
		return
	}
	path := r.URL.EscapedPath()
	if s.stopping.Load() && path != "/api/identity" {
		_ = writeJSON(w, 409, core.OM("error", "conflict", "message", "daemon is stopping; retry after it has stopped"))
		return
	}
	if r.Method == http.MethodOptions {
		corsHeaders(w.Header())
		w.WriteHeader(204)
		return
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) readBody(r *http.Request) (*core.OMap, error) {
	data, err := io.ReadAll(io.LimitReader(bufio.NewReader(r.Body), 4*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 4*1024*1024 {
		return nil, core.Errf("invalid", "request body too large")
	}
	if s.stopping.Load() {
		return nil, core.Errf("conflict", "daemon is stopping; retry after it has stopped")
	}
	if strings.TrimSpace(string(data)) == "" {
		return core.NewOMap(), nil
	}
	v, err := core.DecodeJSON(data)
	if err != nil {
		return nil, core.Errf("invalid", "request body is not valid JSON")
	}
	m, ok := v.(*core.OMap)
	if !ok {
		return nil, &core.ValidationError{Issues: []core.Issue{{Path: "", Message: "expected object"}}}
	}
	return m, nil
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var re *core.RuntimeError
	var ve *core.ValidationError
	switch {
	case errors.As(err, &re):
		details := re.Details
		if details == nil {
			details = core.NewOMap()
		}
		_ = writeJSON(w, re.HTTPStatus(), core.OM("error", re.Code, "message", re.Message, "details", details))
	case errors.As(err, &ve):
		_ = writeJSON(w, 400, core.OM("error", "invalid", "message", "request body is invalid", "details", core.OM("issues", ve.Issues)))
	case errors.Is(err, context.Canceled):
	default:
		// stderr is .prepare-dev/daemon.log for a detached daemon
		fmt.Fprintf(os.Stderr, "[prepare-dev] %s %s %s: 500 %v\n", core.NowISO(), r.Method, r.URL.EscapedPath(), err)
		_ = writeJSON(w, 500, core.OM("error", "internal", "message", err.Error()))
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) error {
	data, err := core.MarshalJSONNoEscape(v)
	if err != nil {
		return err
	}
	h := w.Header()
	h.Set("content-type", "application/json; charset=utf-8")
	corsHeaders(h)
	w.WriteHeader(status)
	_, err = w.Write(data)
	return err
}

var _ = plugin.ClaimRequest{}
