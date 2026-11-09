// Package daemon locates, starts, identifies and stops the per-project daemon, and
// talks to it over HTTP.
package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wim-web/prepare-dev/internal/core"
)

// Version is the tool version; release builds set it with -ldflags.
var Version = "0.3.0-dev"

// Info is the content of .prepare-dev/daemon.json.
type Info struct {
	PID           int                `json:"pid"`
	Port          int                `json:"port"`
	URL           string             `json:"url"`
	Root          string             `json:"root"`
	StartedAt     string             `json:"started_at"`
	Tool          *core.ToolIdentity `json:"tool,omitempty"`
	ShutdownToken string             `json:"shutdown_token,omitempty"`
}

// CanonicalRoot resolves symlinks; the path must exist.
func CanonicalRoot(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// ---------- identity ----------

var (
	identityOnce sync.Once
	identity     core.ToolIdentity
	identityErr  error
)

// Identity tells which build this process is: the version and the sha256 of the
// executable. The skills and UI are embedded, so where the file lies does not
// change what it does; the path is shown for diagnosis only.
func Identity() (core.ToolIdentity, error) {
	identityOnce.Do(func() {
		exe, err := Executable()
		if err != nil {
			identityErr = err
			return
		}
		sum, err := core.SHA256File(exe)
		if err != nil {
			identityErr = err
			return
		}
		identity = core.ToolIdentity{Version: Version, Build: sum, Path: exe, Root: filepath.Dir(exe), Mode: "package"}
	})
	return identity, identityErr
}

// Executable is the binary to start daemons with.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return exe, nil
}

// ---------- lease ----------

// LeaseFile is the file whose exclusive lock makes a daemon the only writer of a
// project. The lock follows the directory itself, whatever path spelling reached it,
// and the OS releases it when the process ends.
func LeaseFile(root string) string { return filepath.Join(root, ".prepare-dev", "lease") }

// Lease is the project's lease held by a daemon.
type Lease struct {
	path string
	f    *os.File
}

// AcquireLease takes the project's lease or fails if another process holds it. A CLI
// checking the lease holds it for a moment, so a busy lease is retried for a second.
func AcquireLease(root string) (*Lease, error) {
	path := LeaseFile(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(time.Second)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil || !(errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)) || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		f.Close()
		holder := ""
		if pid := LeaseHolder(root); pid > 0 {
			holder = fmt.Sprintf(" (pid %d)", pid)
		}
		return nil, fmt.Errorf("another daemon holds the lease of %s%s", root, holder)
	}
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	_ = f.Sync()
	return &Lease{path: path, f: f}, nil
}

// Check fails when the lease file was removed or replaced (git clean, git stash -u):
// another daemon could then take a new lease, so this one must stop writing.
func (l *Lease) Check() error {
	held, err1 := l.f.Stat()
	cur, err2 := os.Stat(l.path)
	if err1 != nil || err2 != nil || !os.SameFile(held, cur) {
		return fmt.Errorf("the lease file %s was removed or replaced; this daemon stops writing (start it again)", l.path)
	}
	return nil
}

// Release unlocks the lease.
func (l *Lease) Release() {
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	l.f.Close()
}

// LeaseHolder is the pid written in the lease file, or 0.
func LeaseHolder(root string) int {
	data, err := os.ReadFile(LeaseFile(root))
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid
}

// LeaseHeld reports whether some process holds the project's lease. It takes the lock
// for a moment when it is free; AcquireLease retries for that.
func LeaseHeld(root string) bool {
	f, err := os.Open(LeaseFile(root))
	if err != nil {
		return false
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// ---------- the TypeScript version ----------

// tsLeasePort is the port the TypeScript version holds as its lease for root.
func tsLeasePort(root string) int {
	sum := sha256.Sum256([]byte(root))
	return 20000 + int(binary.BigEndian.Uint32(sum[:4])%20000)
}

// typeScriptDaemon reports whether daemon.json was written by the TypeScript version
// (its tool has no path) and that version's lease port for root is taken, that is, a
// TypeScript daemon may still be running there.
func typeScriptDaemon(root string, info *Info) bool {
	if info == nil || (info.Tool != nil && info.Tool.Path != "") {
		return false
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", tsLeasePort(root)))
	if err != nil {
		return true
	}
	ln.Close()
	return false
}

func typeScriptError(root string, info *Info) error {
	return fmt.Errorf("daemon.json of %s was written by the TypeScript version of prepare-dev and its lease port %d is in use: stop that daemon (pid %d) with the TypeScript version before using this one", root, tsLeasePort(root), info.PID)
}

// CheckForeignDaemon fails when a TypeScript daemon may hold root; serve calls it
// before taking the lease, since the two versions' leases do not exclude each other.
func CheckForeignDaemon(root string) error {
	if info := readInfoFile(root); info != nil && typeScriptDaemon(root, info) {
		return typeScriptError(root, info)
	}
	return nil
}

// ---------- daemon.json and probing ----------

func infoPath(root string) string { return filepath.Join(root, ".prepare-dev", "daemon.json") }

// sameDir compares directories by identity, not by spelling.
func sameDir(a, b string) bool {
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}

// ReadInfo reads daemon.json of root, or nil if it is missing, invalid or for another directory.
func ReadInfo(root string) *Info {
	info := readInfoFile(root)
	if info == nil || !sameDir(info.Root, root) || info.URL != fmt.Sprintf("http://127.0.0.1:%d", info.Port) {
		return nil
	}
	return info
}

func readInfoFile(root string) *Info {
	data, err := os.ReadFile(infoPath(root))
	if err != nil {
		return nil
	}
	v, err := core.DecodeJSON(data)
	if err != nil {
		return nil
	}
	m, ok := v.(*core.OMap)
	if !ok {
		return nil
	}
	info := &Info{}
	pid, ok1 := m.Get("pid")
	port, ok2 := m.Get("port")
	if !ok1 || !ok2 {
		return nil
	}
	p, okp := core.AsInt(pid)
	po, okpo := core.AsInt(port)
	if !okp || !okpo || p <= 0 || po < 1 || po > 65535 {
		return nil
	}
	info.PID, info.Port = int(p), int(po)
	var ok3, ok4, ok5 bool
	info.URL, ok3 = m.Str("url")
	info.Root, ok4 = m.Str("root")
	info.StartedAt, ok5 = m.Str("started_at")
	if !ok3 || !ok4 || !ok5 {
		return nil
	}
	info.ShutdownToken, _ = m.Str("shutdown_token")
	if t, ok := m.Map("tool"); ok {
		info.Tool = toolOf(t)
	}
	return info
}

func toolOf(m *core.OMap) *core.ToolIdentity {
	t := &core.ToolIdentity{}
	t.Version, _ = m.Str("version")
	t.Build, _ = m.Str("build")
	t.Path, _ = m.Str("path")
	t.Root, _ = m.Str("root")
	t.Mode, _ = m.Str("mode")
	return t
}

func describe(t *core.ToolIdentity) string {
	if t == nil {
		return "unknown build"
	}
	b := t.Build
	if len(b) > 12 {
		b = b[:12]
	}
	where := t.Path
	if where == "" {
		where = t.Root + " (TypeScript version)"
	}
	return fmt.Sprintf("version %s, build %s, %s", t.Version, b, where)
}

type live struct {
	PID       int
	Root      string
	StartedAt string
	Tool      *core.ToolIdentity
}

var httpClient = &http.Client{}

func probe(info *Info) *live {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", info.URL+"/api/identity", nil)
	res, err := httpClient.Do(req)
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil
	}
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil
	}
	v, err := core.DecodeJSON(data)
	if err != nil {
		return nil
	}
	m, ok := v.(*core.OMap)
	if !ok {
		return nil
	}
	l := &live{}
	if pid, ok := m.Get("pid"); ok {
		if n, ok := core.AsInt(pid); ok {
			l.PID = int(n)
		}
	}
	l.Root, _ = m.Str("root")
	l.StartedAt, _ = m.Str("started_at")
	if t, ok := m.Map("tool"); ok {
		l.Tool = toolOf(t)
	}
	if l.PID != info.PID || l.Root != info.Root || l.StartedAt != info.StartedAt {
		return nil
	}
	return l
}

// Ensure connects to the project's daemon, starting it detached when start is true.
//
//   - daemon.json answers with the same version and build: connect.
//   - it answers with another build: refuse, showing that daemon and how to stop it.
//   - it does not answer and the lease is free: the daemon is gone; remove daemon.json
//     (unless a TypeScript daemon may still hold the root) and start one.
//   - the lease is held but nothing answers yet: a daemon is starting; wait until the
//     timeout, then fail with the pid written in the lease file.
func Ensure(root string, start bool, timeout time.Duration) (*Info, error) {
	root, err := CanonicalRoot(root)
	if err != nil {
		return nil, err
	}
	id, err := Identity()
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	logPath := filepath.Join(root, ".prepare-dev", "daemon.log")
	deadline := time.Now().Add(timeout)
	var exited <-chan error // the daemon this call started, when it ends
	var logFrom int64
	for {
		info := ReadInfo(root)
		if info != nil {
			if l := probe(info); l != nil {
				if l.Tool == nil || l.Tool.Build != id.Build || l.Tool.Version != id.Version {
					return nil, fmt.Errorf("the running daemon is another build (%s; pid %d) and this is %s; run prepare-dev stop --root %s, then retry", describe(l.Tool), info.PID, describe(&id), core.JSONString(root))
				}
				return info, nil
			}
		}
		held := LeaseHeld(root)
		if info != nil && !held {
			if typeScriptDaemon(root, info) {
				return nil, typeScriptError(root, info)
			}
			_ = os.Remove(infoPath(root)) // its daemon is gone
			info = nil
		}
		if info == nil && !held {
			if !start {
				return nil, fmt.Errorf("no daemon running for %s (run: prepare-dev serve)", root)
			}
			if exited == nil {
				if st, err := os.Stat(logPath); err == nil {
					logFrom = st.Size()
				}
				if exited, err = spawn(root, logPath); err != nil {
					return nil, err
				}
			} else {
				select {
				case <-exited:
					// it ended without serving and nothing else holds the lease: say why
					return nil, fmt.Errorf("daemon did not start: %s", lastLogLine(logPath, logFrom))
				default:
				}
			}
		}
		if time.Now().After(deadline) {
			if held {
				return nil, fmt.Errorf("a daemon holds the lease of %s but does not answer (pid %d, from %s); stop that process, then retry", root, LeaseHolder(root), LeaseFile(root))
			}
			return nil, fmt.Errorf("daemon did not start (see %s)", logPath)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// lastLogLine is the last non-empty line written to the log after offset from.
func lastLogLine(path string, from int64) string {
	data, err := os.ReadFile(path)
	if err != nil || int64(len(data)) < from {
		return "see " + path
	}
	lines := strings.Split(strings.TrimSpace(string(data[from:])), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return strings.TrimPrefix(last, "error: ")
	}
	return "see " + path
}

// spawn starts the daemon for root; the channel receives when that process ends.
func spawn(root, logPath string) (<-chan error, error) {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, err
	}
	logf, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	defer logf.Close()
	exe, err := Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, "serve", "--root", root)
	cmd.Dir = root
	cmd.Stdout = logf
	cmd.Stderr = logf
	// a new session survives the terminal that started the CLI. Tests set
	// PREPARE_DEV_DAEMON_SESSION=inherit so their daemons end with the test's
	// process group.
	if os.Getenv("PREPARE_DEV_DAEMON_SESSION") != "inherit" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return done, nil
}

// Stop asks the project's daemon to stop. It returns false when none is running and
// removes a daemon.json left by a daemon that is gone.
func Stop(root string) (bool, error) {
	root, err := CanonicalRoot(root)
	if err != nil {
		return false, err
	}
	info := ReadInfo(root)
	held := LeaseHeld(root)
	if info == nil || probe(info) == nil {
		switch {
		case held:
			return false, fmt.Errorf("a daemon holds the lease of %s but does not answer (pid %d, from %s); stop that process", root, LeaseHolder(root), LeaseFile(root))
		case info != nil && typeScriptDaemon(root, info):
			return false, typeScriptError(root, info)
		case info != nil:
			_ = os.Remove(infoPath(root))
		}
		return false, nil
	}
	if info.ShutdownToken == "" {
		return false, fmt.Errorf("daemon.json has no shutdown token; stop the process %d", info.PID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", info.URL+"/api/shutdown", nil)
	req.Header.Set("Authorization", "Bearer "+info.ShutdownToken)
	res, err := httpClient.Do(req)
	if err != nil {
		return false, err
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return false, fmt.Errorf("daemon refused to stop: %s", body)
	}
	for i := 0; i < 50; i++ {
		if probe(info) == nil && !LeaseHeld(root) {
			return true, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false, fmt.Errorf("daemon did not stop in 5 seconds")
}

// ---------- API client ----------

// APIError is a non-2xx response.
type APIError struct {
	Status  int
	Code    string
	Message string
	Details any
}

func (e *APIError) Error() string { return e.Message }

// Client calls a daemon's HTTP API.
type Client struct {
	BaseURL string
}

// Connect ensures the daemon and returns a client for it.
func Connect(root string, start bool) (*Client, error) {
	info, err := Ensure(root, start, 0)
	if err != nil {
		return nil, err
	}
	return &Client{BaseURL: info.URL}, nil
}

// Request sends a JSON request and decodes the JSON response into ordered values.
func (c *Client) Request(method, path string, body any, timeout time.Duration) (any, error) {
	if timeout <= 0 {
		timeout = time.Hour
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		data, err := core.MarshalJSONNoEscape(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	var v any
	if len(bytes.TrimSpace(data)) > 0 {
		v, err = core.DecodeJSON(data)
		if err != nil {
			return nil, fmt.Errorf("invalid response from daemon: %w", err)
		}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		e := &APIError{Status: res.StatusCode, Code: "error", Message: http.StatusText(res.StatusCode)}
		if m, ok := v.(*core.OMap); ok {
			if s, ok := m.Str("error"); ok {
				e.Code = s
			}
			if s, ok := m.Str("message"); ok {
				e.Message = s
			}
			e.Details, _ = m.Get("details")
		}
		return nil, e
	}
	return v, nil
}

func (c *Client) Get(path string, timeout time.Duration) (any, error) {
	return c.Request("GET", path, nil, timeout)
}

func (c *Client) Post(path string, body any) (any, error) {
	if body == nil {
		body = core.NewOMap()
	}
	return c.Request("POST", path, body, 0)
}

func (c *Client) Put(path string, body any) (any, error) { return c.Request("PUT", path, body, 0) }
