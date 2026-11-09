// Package e2e copies the prepare-dev binary to a temporary location and drives it like
// an installed tool: init, records, questions, answers, decisions, trace, status, stop
// and restart. It builds the binary from this checkout, or tests the one named by
// PREPARE_DEV_E2E_BINARY (for example a binary taken from a release archive).
package e2e

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	byteorder "encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	skillcopy "github.com/wim-web/prepare-dev/internal/skills"
)

var (
	binary string // the binary under test
	other  string // a different build (another version string), for the identity check
)

func goBuild(out string, ldflags string) error {
	build := exec.Command("go", "build", "-ldflags", ldflags, "-o", out, "./cmd/prepare-dev")
	build.Dir = filepath.Join("..", "..")
	if data, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("build failed: %v\n%s", err, data)
	}
	return nil
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "prepare-dev-bin-")
	if err != nil {
		panic(err)
	}
	binary = os.Getenv("PREPARE_DEV_E2E_BINARY")
	if binary == "" {
		binary = filepath.Join(dir, "prepare-dev")
		if err := goBuild(binary, ""); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	} else if binary, err = filepath.Abs(binary); err != nil {
		panic(err)
	}
	other = filepath.Join(dir, "prepare-dev-other")
	if err := goBuild(other, "-X main.version=0.0.0-e2e-other"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// in a verification run, say which binary these tests checked (scope.bounds)
	if bounds := os.Getenv("PREPARE_DEV_BOUNDS_DIR"); bounds != "" {
		origin := "built from the checkout"
		if os.Getenv("PREPARE_DEV_E2E_BINARY") != "" {
			origin = "PREPARE_DEV_E2E_BINARY"
		}
		data, err := os.ReadFile(binary)
		if err != nil {
			panic(err)
		}
		sum := sha256.Sum256(data)
		out, _ := json.Marshal(map[string]any{"e2e_binary": origin, "e2e_build": hex.EncodeToString(sum[:])})
		if err := os.WriteFile(filepath.Join(bounds, "e2e.json"), out, 0o644); err != nil {
			panic(err)
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

const minimalPath = "/usr/bin:/bin"

// minimalEnv runs the binary without Node.js or Go on PATH. Daemons stay in the test's
// process group, so they end with it even when the test is killed.
func minimalEnv() []string {
	return []string{"PATH=" + minimalPath, "HOME=" + os.Getenv("HOME"), "TMPDIR=" + os.TempDir(), "PREPARE_DEV_DAEMON_SESSION=inherit"}
}

// onPath reports whether an executable named name is in one of the PATH directories.
func onPath(path, name string) bool {
	for _, dir := range filepath.SplitList(path) {
		if st, err := os.Stat(filepath.Join(dir, name)); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return true
		}
	}
	return false
}

type project struct {
	t    *testing.T
	root string
	bin  string
}

func newProject(t *testing.T, bin string) *project {
	t.Helper()
	root := filepath.Join(t.TempDir(), "target project") // a path with a space
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	p := &project{t: t, root: root, bin: bin}
	t.Cleanup(func() { _, _, _ = p.run("stop") })
	return p
}

func (p *project) run(args ...string) (string, string, int) {
	cmd := exec.Command(p.bin, append(args, "--root", p.root)...)
	cmd.Dir = p.root
	cmd.Env = minimalEnv()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		p.t.Fatalf("%v: %v", args, err)
	}
	return out.String(), errb.String(), code
}

func (p *project) ok(args ...string) string {
	p.t.Helper()
	out, errOut, code := p.run(args...)
	if code != 0 {
		p.t.Fatalf("prepare-dev %s: exit %d\n%s%s", strings.Join(args, " "), code, out, errOut)
	}
	return out
}

func (p *project) json(args ...string) map[string]any {
	p.t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(p.ok(args...)), &v); err != nil {
		p.t.Fatalf("prepare-dev %s: not JSON: %v", strings.Join(args, " "), err)
	}
	return v
}

func (p *project) write(name, content string) string {
	p.t.Helper()
	path := filepath.Join(p.root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		p.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		p.t.Fatal(err)
	}
	return path
}

func (p *project) daemonURL() string {
	p.t.Helper()
	data, err := os.ReadFile(filepath.Join(p.root, ".prepare-dev", "daemon.json"))
	if err != nil {
		p.t.Fatal(err)
	}
	var info struct{ URL string }
	if err := json.Unmarshal(data, &info); err != nil {
		p.t.Fatal(err)
	}
	return info.URL
}

func (p *project) pid() int {
	p.t.Helper()
	data, err := os.ReadFile(filepath.Join(p.root, ".prepare-dev", "daemon.json"))
	if err != nil {
		p.t.Fatal(err)
	}
	var info struct{ PID int }
	if err := json.Unmarshal(data, &info); err != nil {
		p.t.Fatal(err)
	}
	return info.PID
}

func httpDo(t *testing.T, method, url, body string, header map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("content-type", "application/json")
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var b bytes.Buffer
	_, _ = b.ReadFrom(res.Body)
	return res.StatusCode, b.String()
}

// rawGet sends a request line as is (net/http refuses to build malformed URLs).
func rawGet(t *testing.T, base, path string) int {
	t.Helper()
	host := strings.TrimPrefix(base, "http://")
	conn, err := net.Dial("tcp", host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", path, host)
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var proto string
	var code int
	fmt.Sscanf(line, "%s %d", &proto, &code)
	return code
}

// relocated copies the built binary to a fresh directory, like an installation.
func relocated(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "install dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "prepare-dev")
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return dst
}

// [P1] The relocated binary runs without Node.js, the source checkout or Go on PATH,
// and carries the question, answer, decision and trace flow end to end.
func testP1Flow(t *testing.T) {
	for _, tool := range []string{"go", "node", "npm", "pnpm"} {
		if onPath(minimalPath, tool) {
			t.Fatalf("%s is on the PATH the binary runs with (%s)", tool, minimalPath)
		}
	}
	bin := relocated(t)
	p := newProject(t, bin)

	id := p.json("version", "--json")
	realBin, _ := filepath.EvalSymlinks(bin)
	if build, _ := id["build"].(string); len(build) != 64 || id["path"] != realBin || id["version"] == "" {
		t.Fatalf("identity: %v", id)
	}
	if out := p.ok("help"); !strings.Contains(out, "--prompt-file") {
		t.Fatalf("help does not mention --prompt-file")
	}
	init := p.json("init")
	if !strings.HasPrefix(init["ui"].(string), "http://127.0.0.1:") {
		t.Fatalf("init: %v", init)
	}
	url := p.daemonURL()
	code, html := httpDo(t, "GET", url+"/", "", nil)
	if code != 200 || !strings.Contains(html, "<div id=\"root\">") && !strings.Contains(html, "id=\"root\"") || strings.Contains(html, "@json-render") {
		t.Fatalf("UI page: %d", code)
	}

	// records: create, then put with the version read before
	p.write("req.md", "受付人数は4人まで\n")
	rec := p.json("record", "create", "--kind", "requirement", "--title", "予約の受付人数", "--body-file", filepath.Join(p.root, "req.md"))
	if rec["id"] != "REQ-0001" {
		t.Fatalf("record create: %v", rec)
	}
	if _, errOut, code := p.run("record", "put", "REQ-0001", "--fm", "status=proposed"); code == 0 || !strings.Contains(errOut, "--expected-rev") {
		t.Fatalf("record put without expected version should fail: %s", errOut)
	}
	if _, errOut, code := p.run("record", "put", "REQ-0001", "--expected-rev", "1", "--expected-sha256", "deadbeef", "--fm", "status=proposed"); code == 0 || !strings.Contains(errOut, "conflict") {
		t.Fatalf("stale record put should conflict: %s", errOut)
	}
	put := p.json("record", "put", "REQ-0001", "--expected-rev", "1", "--expected-sha256", rec["sha256"].(string), "--fm", "status=proposed")
	if put["rev"].(float64) != 2 || put["body"] != "\n受付人数は4人まで\n" && put["body"] != "受付人数は4人まで\n" {
		t.Fatalf("record put: rev %v body %q", put["rev"], put["body"])
	}

	// a question with a Markdown table and options from files
	p.write("q.md", "# 上限を決める\n\n| 案 | 内容 |\n| --- | --- |\n| 4人 | 小さい |\n")
	p.write("opts.json", `[{"key":"four","label":"4人","summary":"4人まで受け付ける","recommended":true,"reason":"席数に合う"},{"key":"six","label":"6人"}]`)
	q := p.json("q", "create", "--subject", "REQ-0001", "--prompt-file", filepath.Join(p.root, "q.md"), "--options-file", filepath.Join(p.root, "opts.json"))
	if q["url"] != url+"/q/Q-0001" {
		t.Fatalf("q create url: %v", q["url"])
	}
	if code, _ := httpDo(t, "GET", url+"/api/questions/Q-0001/ui", "", nil); code != 404 {
		t.Fatalf("legacy ui endpoint: %d", code)
	}

	// the browser answers through the API (same origin), other origins are refused
	if code, _ := httpDo(t, "POST", url+"/api/questions/Q-0001/answers", `{"choice":["four"],"text":"補足"}`, map[string]string{"Origin": "http://evil.example"}); code != 403 {
		t.Fatalf("cross-origin answer: %d", code)
	}
	if code, body := httpDo(t, "POST", url+"/api/questions/Q-0001/answers", `{"choice":["four"],"text":"補足"}`, map[string]string{"Origin": url}); code != 201 {
		t.Fatalf("answer: %d %s", code, body)
	}

	wait := p.json("q", "wait", "Q-0001", "--timeout", "1000")
	if wait["status"] != "answered" {
		t.Fatalf("wait: %v", wait["status"])
	}
	ack := p.json("q", "ack", "Q-0001", "ANS-0001-1")
	if ack["received_at"] == nil {
		t.Fatalf("ack: %v", ack)
	}
	p.write("d.md", "4人に決めた\n")
	applied := p.json("q", "apply", "Q-0001", "ANS-0001-1", "--title", "上限は4人", "--body-file", filepath.Join(p.root, "d.md"), "--chosen", "four", "--alternative", "six")
	if applied["status"] != "applied" || applied["decision_id"] != "DEC-0001" {
		t.Fatalf("apply: %v", applied)
	}
	again := p.json("q", "apply", "Q-0001", "ANS-0001-1", "--title", "x", "--body", "x")
	if again["status"] != "already_applied" || again["decision_id"] != "DEC-0001" {
		t.Fatalf("apply retry: %v", again)
	}

	trace := p.json("trace", "REQ-0001")
	decisions := trace["decisions"].([]any)
	if len(decisions) != 1 || decisions[0].(map[string]any)["id"] != "DEC-0001" {
		t.Fatalf("trace decisions: %v", decisions)
	}
	status := p.ok("status")
	for _, want := range []string{"pending answers: 0", "inflight: 0", "stale decisions: none", "questions waiting for an answer: 0"} {
		if !strings.Contains(status, want) {
			t.Fatalf("status lacks %q:\n%s", want, status)
		}
	}

	// malformed paths are rejected without taking the daemon down
	// net/http answers 400 before routing; the daemon keeps serving
	if code := rawGet(t, url, "/q/%E0%A4%A"); code != 400 {
		t.Fatalf("UI page with a malformed escape: %d", code)
	}
	if code := rawGet(t, url, "/api/questions/%E0%A4%A"); code != 400 {
		t.Fatalf("API with a malformed escape: %d", code)
	}
	if code, _ := httpDo(t, "GET", url+"/api/questions/..%2Fruns%2FRUN-0001", "", nil); code != 404 {
		t.Fatalf("question id outside the questions dir: %d", code)
	}
	if code, _ := httpDo(t, "GET", url+"/api/status", "", nil); code != 200 {
		t.Fatalf("daemon down after malformed requests: %d", code)
	}
	if out := strings.TrimSpace(p.ok("ui", "Q-0001", "--no-open")); out != url+"/q/Q-0001" {
		t.Fatalf("ui: %q", out)
	}
	if out := p.ok("bundle", "REQ-0001", "--stdout"); !strings.Contains(out, "REQ-0001") || !strings.Contains(out, "DEC-0001") {
		t.Fatalf("bundle: %s", out)
	}
	if out := p.ok("stop"); strings.TrimSpace(out) != "daemon stopped" {
		t.Fatalf("stop: %q", out)
	}
	log, _ := os.ReadFile(filepath.Join(p.root, ".prepare-dev", "daemon.log"))
	if strings.Contains(string(log), "shutdown_token") {
		t.Fatalf("the shutdown token is written to daemon.log")
	}
}

// [P2] init copies the skills as real directories, writes no MCP configuration, is
// idempotent, and keeps the user's edits.
func testP2Skills(t *testing.T) {
	p := newProject(t, relocated(t))
	first := p.json("init")
	skills := first["skills"].(map[string]any)
	if len(skills["copied"].([]any)) != 14 || len(skills["conflicts"].([]any)) != 0 {
		t.Fatalf("first init: %v", skills)
	}
	skill := filepath.Join(p.root, ".agents", "skills", "prepare-dev", "SKILL.md")
	st, err := os.Lstat(filepath.Dir(skill))
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("skill is not a real directory: %v", err)
	}
	for _, f := range []string{".mcp.json", filepath.Join(".codex", "config.toml")} {
		if _, err := os.Stat(filepath.Join(p.root, f)); err == nil {
			t.Fatalf("init wrote %s", f)
		}
	}
	if data, err := os.ReadFile(filepath.Join(p.root, ".prepare-dev", ".gitignore")); err != nil || !strings.Contains(string(data), "lease") || !strings.Contains(string(data), "daemon.json") {
		t.Fatalf("init should keep the daemon's files out of git: %v %s", err, data)
	}
	second := p.json("init")["skills"].(map[string]any)
	if len(second["copied"].([]any)) != 0 || len(second["conflicts"].([]any)) != 0 || len(second["unchanged"].([]any)) != 14 {
		t.Fatalf("second init: %v", second)
	}
	if err := os.WriteFile(skill, []byte("edited by the user\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	third := p.json("init")["skills"].(map[string]any)
	if len(third["conflicts"].([]any)) != 1 {
		t.Fatalf("edited skill should be reported: %v", third)
	}
	if data, _ := os.ReadFile(skill); string(data) != "edited by the user\n" {
		t.Fatalf("user edit overwritten")
	}

	// a skill an older version installed and nobody edited since is updated: make the
	// manifest say prepare-dev installed exactly what is there now
	manifestPath := filepath.Join(p.root, ".prepare-dev", "skills.json")
	var manifest struct {
		Installed map[string]string `json:"installed"`
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil || json.Unmarshal(data, &manifest) != nil || len(manifest.Installed) != 14 {
		t.Fatalf("skills.json: %v %s", err, data)
	}
	manifest.Installed[".agents/skills/prepare-dev"] = skillcopy.Digest(os.DirFS(filepath.Join(p.root, ".agents", "skills")), "prepare-dev")
	data, _ = json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	fourth := p.json("init")["skills"].(map[string]any)
	if len(fourth["updated"].([]any)) != 1 || len(fourth["conflicts"].([]any)) != 0 {
		t.Fatalf("unedited old skill should be updated: %v", fourth)
	}
	if data, _ := os.ReadFile(skill); string(data) == "edited by the user\n" {
		t.Fatalf("skill not updated")
	}

	// an edited skill is replaced only on request, after a backup
	if err := os.WriteFile(skill, []byte("edited again\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fifth := p.json("init", "--replace-skills")["skills"].(map[string]any)
	replaced := fifth["replaced"].([]any)
	if len(replaced) != 1 {
		t.Fatalf("--replace-skills: %v", fifth)
	}
	backup := replaced[0].(map[string]any)["backup"].(string)
	if data, _ := os.ReadFile(filepath.Join(backup, "SKILL.md")); string(data) != "edited again\n" {
		t.Fatalf("backup lacks the edit: %s", backup)
	}
	if data, _ := os.ReadFile(skill); string(data) == "edited again\n" {
		t.Fatalf("skill not replaced")
	}
}

// [P3] One writer per root: concurrent starts share one daemon, a second serve is
// refused before it opens anything, a daemon.json left by a killed daemon is replaced,
// stop and restart work, a copy of the same build connects and another build is refused.
func testP3Daemon(t *testing.T) {
	bin := relocated(t)
	p := newProject(t, bin)
	p.ok("init", "--no-skills")
	p.ok("stop")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, errOut, code := p.run("status", "--json")
			if code != 0 {
				t.Errorf("concurrent status: %s", errOut)
				return
			}
			var v map[string]any
			if json.Unmarshal([]byte(out), &v) != nil {
				t.Errorf("status not JSON")
			}
		}()
	}
	wg.Wait()
	first := p.pid()
	for i := 0; i < 3; i++ {
		if _, _, code := p.run("status", "--json"); code != 0 {
			t.Fatal("status after the concurrent starts")
		}
		if p.pid() != first {
			t.Fatalf("the daemon changed from %d to %d", first, p.pid())
		}
	}

	// a second serve on the same root fails on the lease and leaves the journal alone
	journal := filepath.Join(p.root, "prepare", "events.jsonl")
	before, _ := os.ReadFile(journal)
	cmd := exec.Command(bin, "serve", "--root", p.root)
	cmd.Env = minimalEnv()
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "holds the lease") {
		t.Fatalf("second serve: %v %s", err, out)
	}
	if after, _ := os.ReadFile(journal); !bytes.Equal(before, after) {
		t.Fatalf("the refused serve wrote to the journal")
	}

	if code, _ := httpDo(t, "POST", p.daemonURL()+"/api/shutdown", "", nil); code != 403 {
		t.Fatalf("shutdown without token: %d", code)
	}
	p.ok("stop")
	p.ok("status")
	second := p.pid()
	if second == first {
		t.Fatalf("restart kept the pid %d", first)
	}

	// a daemon killed without cleanup leaves daemon.json; its lease is gone with it
	proc, _ := os.FindProcess(second)
	_ = proc.Kill()
	_, _ = proc.Wait()
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(filepath.Join(p.root, ".prepare-dev", "daemon.json")); err != nil {
			t.Fatal("daemon.json should outlive a killed daemon")
		}
		if _, _, code := p.run("status", "--no-start"); code != 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	p.ok("status")
	if third := p.pid(); third == second {
		t.Fatalf("no new daemon after the kill")
	}

	// a copy of the same build elsewhere is the same tool
	moved := &project{t: t, root: p.root, bin: relocated(t)}
	before3 := p.pid()
	moved.ok("status")
	if p.pid() != before3 {
		t.Fatalf("a copy of the same build started another daemon")
	}
	// another build is refused, with the running daemon's identity and how to stop it
	foreign := &project{t: t, root: p.root, bin: other}
	if _, errOut, code := foreign.run("status"); code == 0 || !strings.Contains(errOut, "another build") || !strings.Contains(errOut, "prepare-dev stop") || !strings.Contains(errOut, fmt.Sprintf("pid %d", before3)) {
		t.Fatalf("another build should be refused: %s", errOut)
	}

	// the same directory through a symbolic link is the same root and the same daemon
	link := filepath.Join(t.TempDir(), "link to project")
	if err := os.Symlink(p.root, link); err != nil {
		t.Fatal(err)
	}
	linked := &project{t: t, root: link, bin: bin}
	linked.ok("status")
	if p.pid() != before3 {
		t.Fatalf("the root through a link started another daemon")
	}

	// a daemon whose lease file is removed (git clean, git stash -u) stops writing and exits,
	// so the daemon the next command starts is the only writer
	url := p.daemonURL()
	for _, f := range []string{"lease", "daemon.json"} {
		if err := os.Remove(filepath.Join(p.root, ".prepare-dev", f)); err != nil {
			t.Fatal(err)
		}
	}
	if code, _ := httpDo(t, "GET", url+"/api/status", "", nil); code != 503 {
		t.Fatalf("the daemon without its lease still serves: %d", code)
	}
	gone := false
	for i := 0; i < 50 && !gone; i++ {
		gone = syscall.Kill(before3, 0) != nil
		time.Sleep(100 * time.Millisecond)
	}
	if !gone {
		t.Fatalf("the daemon without its lease is still running")
	}
	p.ok("status")
	if p.pid() == before3 {
		t.Fatal("no new daemon after the lease was removed")
	}
}

// [P3] A daemon.json left by the TypeScript version, whose lease port is still taken, is
// not taken over: the two versions' leases do not exclude each other.
func testP3TypeScriptDaemon(t *testing.T) {
	p := newProject(t, relocated(t))
	p.ok("init", "--no-skills")
	p.ok("stop")
	real, err := filepath.EvalSymlinks(p.root)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(real))
	port := 20000 + int(byteorder.BigEndian.Uint32(sum[:4])%20000)
	lease, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Skipf("the TypeScript lease port %d is in use here: %v", port, err)
	}
	dead, _ := net.Listen("tcp", "127.0.0.1:0")
	deadPort := dead.Addr().(*net.TCPAddr).Port
	dead.Close()
	info := fmt.Sprintf(`{"pid": %d, "port": %d, "url": "http://127.0.0.1:%d", "root": %q, "started_at": "2026-11-09T00:00:00.000Z", "tool": {"version": "0.1.0", "build": "x", "root": "/opt/prepare-dev", "mode": "package"}, "shutdown_token": "t"}`, os.Getpid(), deadPort, deadPort, real)
	p.write(filepath.Join(".prepare-dev", "daemon.json"), info)
	if _, errOut, code := p.run("status"); code == 0 || !strings.Contains(errOut, "TypeScript") {
		lease.Close()
		t.Fatalf("a TypeScript daemon that may still run should be refused: %s", errOut)
	}
	if _, errOut, code := p.run("serve"); code == 0 || !strings.Contains(errOut, "TypeScript") {
		lease.Close()
		t.Fatalf("serve should refuse too: %s", errOut)
	}
	lease.Close() // the TypeScript daemon is gone
	p.ok("status")
}

// [P1] The relocated binary verifies a Lean theorem with the real lake, and trace links
// the requirement, the spec and the run.
func testP1Lean(t *testing.T) {
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(home, ".elan", "bin", "lake")); err != nil {
		if _, err := exec.LookPath("lake"); err != nil {
			t.Skip("lake is not installed")
		}
	}
	toolchain, err := os.ReadFile(filepath.Join("..", "..", "model", "lean-toolchain"))
	if err != nil {
		t.Fatal(err)
	}
	p := newProject(t, relocated(t))
	p.ok("init", "--no-skills")
	p.write("req.md", "受付人数は定員以下\n")
	p.json("record", "create", "--kind", "requirement", "--title", "受付人数", "--body-file", filepath.Join(p.root, "req.md"))
	p.write("spec.json", `{"status":"accepted","links":{"specifies":["REQ-0001"]},"claims":[{"id":"C1","statement":"受付人数は定員以下","requirement":"REQ-0001","native":[{"plugin":"lean4","ref":"Reservation.bounded"}]}]}`)
	p.json("record", "create", "--kind", "spec", "--title", "受付人数の上限", "--body", "定員以下", "--fm-file", filepath.Join(p.root, "spec.json"))
	p.write("model/lean-toolchain", string(toolchain))
	p.write("model/lakefile.toml", "name = \"reservation_model\"\ndefaultTargets = [\"Reservation\"]\n[[lean_lib]]\nname = \"Reservation\"\n")
	p.write("model/Reservation.lean", "namespace Reservation\ndef accepted (requested capacity : Nat) := min requested capacity\ntheorem bounded (requested capacity : Nat) : accepted requested capacity ≤ capacity := Nat.min_le_right _ _\nend Reservation\n")
	if out := p.ok("guidance", "lean4"); !strings.Contains(out, "#print axioms") {
		t.Fatal("guidance lean4")
	}
	res := p.json("verify", "run", "--plugin", "lean4", "--claim", "SPEC-0001/C1", "--target", "model/Reservation.lean", "--config-json", `{"project_dir":"model"}`, "--wait", "--timeout", "600000")
	rec, _ := res["record"].(map[string]any)
	app, _ := res["applicability"].(map[string]any)
	if rec == nil || rec["result"] != "verified" || rec["method"] != "proof" || app == nil || app["applicable"] != true {
		data, _ := json.MarshalIndent(res, "", "  ")
		t.Fatalf("lean verification:\n%s", data)
	}
	claim := rec["claims"].([]any)[0].(map[string]any)
	if stmt, _ := claim["native"].(map[string]any)["statement"].(string); !strings.Contains(stmt, "accepted requested capacity ≤ capacity") {
		t.Fatalf("statement: %v", claim["native"])
	}
	trace := p.json("trace", "REQ-0001")
	specs := trace["specs"].([]any)
	current := specs[0].(map[string]any)["claims"].([]any)[0].(map[string]any)["current"].(map[string]any)
	if current["run_id"] != "RUN-0001" {
		t.Fatalf("trace current evidence: %v", current)
	}
}

// SPEC-0004 P1–P3: the binary as an installed tool.
func TestDistribution(t *testing.T) {
	t.Run("[P1] the relocated binary runs the question, answer, decision and trace flow without Node.js, Go or the checkout", testP1Flow)
	t.Run("[P2] init copies the skills, writes no MCP configuration, is idempotent and keeps edits", testP2Skills)
	t.Run("[P3] one writer per root; stop, restart, a killed daemon is replaced, another build is refused, a daemon without its lease stops", testP3Daemon)
	t.Run("[P3] a daemon.json of the TypeScript version whose lease port is taken is not taken over", testP3TypeScriptDaemon)
	t.Run("[P3] prepare/ merged from two working copies is refused with what to do", testP3MergedJournal)
	t.Run("[P3] a daemon whose journal Git replaced writes nothing and stops", testP3ReplacedJournal)
	t.Run("[A1] the CLI has no answer command and there is no MCP entry; the API takes answers from the decision UI only", testA1Answers)
	t.Run("[P1] the relocated binary verifies a Lean theorem with the real lake and trace links the evidence", testP1Lean)
}

// [C3] A daemon process that dies during apply recovers on the next start. Before the
// commit the transaction is aborted, and the answer is stale because the subject was
// edited while the daemon was down; after the commit the decision stands and a retry
// returns it. (The port of the TypeScript version's scripts/e2e-crash.sh.)
func TestCrashRecovery(t *testing.T) {
	bin := relocated(t)
	for _, tc := range []struct{ stage, expect string }{{"question", "abort"}, {"commit", "committed"}} {
		t.Run(fmt.Sprintf("[C3] killed after stage %s -> %s", tc.stage, tc.expect), func(t *testing.T) {
			p := newProject(t, bin)
			p.ok("init", "--no-skills")
			p.ok("stop")
			cmd := exec.Command(bin, "serve", "--root", p.root, "--crash-after", tc.stage)
			cmd.Env = minimalEnv()
			var log bytes.Buffer
			var mu sync.Mutex
			cmd.Stdout = &lockedWriter{w: &log, mu: &mu}
			cmd.Stderr = cmd.Stdout
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			info := filepath.Join(p.root, ".prepare-dev", "daemon.json")
			for i := 0; i < 200; i++ {
				if _, err := os.Stat(info); err == nil {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			p.write("req.md", "v1\n")
			p.json("record", "create", "--kind", "requirement", "--title", "R", "--body-file", filepath.Join(p.root, "req.md"))
			p.write("opts.json", `[{"key":"a","label":"A"}]`)
			p.json("q", "create", "--subject", "REQ-0001", "--prompt", "?", "--options-file", filepath.Join(p.root, "opts.json"))
			url := p.daemonURL()
			if code, body := httpDo(t, "POST", url+"/api/questions/Q-0001/answers", `{"choice":["a"],"text":null}`, map[string]string{"Origin": url}); code != 201 {
				t.Fatalf("answer: %d %s", code, body)
			}
			_, _, _ = p.run("q", "apply", "Q-0001", "ANS-0001-1", "--title", "A", "--body", "b") // the daemon exits here
			select {
			case <-done:
			case <-time.After(15 * time.Second):
				t.Fatal("the daemon did not exit")
			}
			mu.Lock()
			out := log.String()
			mu.Unlock()
			if !strings.Contains(out, "simulated crash after stage "+tc.stage) {
				t.Fatalf("no crash at stage %s:\n%s", tc.stage, out)
			}
			// edit the subject while no daemon runs; daemon.json is still there
			req := filepath.Join(p.root, "prepare", "requirements", "REQ-0001.md")
			f, err := os.OpenFile(req, os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = f.WriteString("\n停止中に編集\n")
			f.Close()

			status := p.json("status", "--json")
			retryOut, retryErr, _ := p.run("q", "apply", "Q-0001", "ANS-0001-1", "--title", "A", "--body", "b")
			events, _ := os.ReadFile(filepath.Join(p.root, "prepare", "events.jsonl"))
			_, decErr := os.Stat(filepath.Join(p.root, "prepare", "decisions", "DEC-0001.md"))
			pending := status["pending"].([]any)
			switch tc.expect {
			case "abort":
				if !strings.Contains(string(events), `"type":"apply.abort"`) || decErr == nil {
					t.Fatalf("expected an abort without a decision file")
				}
				if len(pending) != 1 || pending[0].(map[string]any)["answer_id"] != "ANS-0001-1" || pending[0].(map[string]any)["stale"] != true {
					t.Fatalf("the answer should be pending and stale: %v", pending)
				}
				if !strings.Contains(retryErr, "stale") {
					t.Fatalf("a retry after the abort should be refused as stale: %s%s", retryOut, retryErr)
				}
			case "committed":
				if !strings.Contains(string(events), `"type":"apply.commit"`) || strings.Contains(string(events), `"type":"apply.abort"`) || decErr != nil {
					t.Fatalf("expected the committed decision to stand")
				}
				if !strings.Contains(retryOut, `"status": "already_applied"`) || !strings.Contains(retryOut, `"decision_id": "DEC-0001"`) {
					t.Fatalf("a retry should return the decision: %s%s", retryOut, retryErr)
				}
				if len(pending) != 0 {
					t.Fatalf("the committed answer is still pending: %v", pending)
				}
			}
		})
	}
}

type lockedWriter struct {
	w  *bytes.Buffer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// [A1] Answers come from the person in the decision UI: the CLI has no answer command and
// the API refuses a request without the page's Origin.
func testA1Answers(t *testing.T) {
	p := newProject(t, relocated(t))
	p.ok("init", "--no-skills")
	p.write("req.md", "r\n")
	p.json("record", "create", "--kind", "requirement", "--title", "R", "--body-file", filepath.Join(p.root, "req.md"))
	p.write("opts.json", `[{"key":"a","label":"A"}]`)
	p.json("q", "create", "--subject", "REQ-0001", "--prompt", "?", "--options-file", filepath.Join(p.root, "opts.json"))
	if _, errOut, code := p.run("q", "answer", "Q-0001", "--choice", "a"); code == 0 || !strings.Contains(errOut, "decision UI") {
		t.Fatalf("q answer should be refused: %s", errOut)
	}
	if _, errOut, code := p.run("mcp"); code == 0 || !strings.Contains(errOut, "usage") {
		t.Fatalf("there should be no MCP entry: %s", errOut)
	}
	url := p.daemonURL()
	if code, _ := httpDo(t, "POST", url+"/api/questions/Q-0001/answers", `{"choice":["a"],"text":null}`, nil); code != 403 {
		t.Fatalf("answer without Origin: %d", code)
	}
	if code, body := httpDo(t, "POST", url+"/api/questions/Q-0001/answers", `{"choice":["a"],"text":null}`, map[string]string{"Origin": url}); code != 201 {
		t.Fatalf("answer from the UI: %d %s", code, body)
	}
}

// [P3] prepare/ merged from two working copies: with conflict markers in the journal the
// daemon does not start and the CLI says why at once; with both sides' lines kept it
// starts, writes nothing and status shows the problem.
func testP3MergedJournal(t *testing.T) {
	p := newProject(t, relocated(t))
	p.ok("init", "--no-skills")
	p.write("req.md", "r\n")
	p.json("record", "create", "--kind", "requirement", "--title", "R", "--body-file", filepath.Join(p.root, "req.md"))
	p.ok("stop")
	journal := filepath.Join(p.root, "prepare", "events.jsonl")
	clean, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	p.write(filepath.Join("prepare", "events.jsonl"), "<<<<<<< HEAD\n"+string(clean)+"=======\n"+string(clean)+">>>>>>> other\n")
	began := time.Now()
	_, errOut, code := p.run("status")
	if code == 0 || !strings.Contains(errOut, "merge conflict marker") || !strings.Contains(errOut, "Branches and worktrees") {
		t.Fatalf("status with conflict markers: %s", errOut)
	}
	if time.Since(began) > 10*time.Second {
		t.Fatalf("the CLI took %s to say why the daemon did not start", time.Since(began))
	}
	p.write(filepath.Join("prepare", "events.jsonl"), string(clean)+string(clean)) // both sides' lines kept
	out := p.ok("status")
	if !strings.Contains(out, "created twice") || !strings.Contains(out, "resolve the merge of prepare/") {
		t.Fatalf("status should show the conflicting journal and what to do:\n%s", out)
	}
	p.write("req2.md", "r2\n")
	if _, errOut, code := p.run("record", "create", "--kind", "requirement", "--title", "R2", "--body-file", filepath.Join(p.root, "req2.md")); code == 0 || !strings.Contains(errOut, "conflicting entries") {
		t.Fatalf("writes should be refused: %s", errOut)
	}
}

// [P3] Git replaces prepare/events.jsonl on a branch switch, stash, reset or pull. A
// daemon still running notices before its next operation, writes nothing and exits, and
// the next command opens the journal as it is now.
func testP3ReplacedJournal(t *testing.T) {
	p := newProject(t, relocated(t))
	p.ok("init", "--no-skills")
	p.write("req.md", "r\n")
	p.json("record", "create", "--kind", "requirement", "--title", "R", "--body-file", filepath.Join(p.root, "req.md"))
	first := p.pid()
	journal := filepath.Join(p.root, "prepare", "events.jsonl")
	data, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	// what a checkout does: remove the file and write the other branch's version
	if err := os.Remove(journal); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journal, data, 0o644); err != nil {
		t.Fatal(err)
	}
	p.write("req2.md", "r2\n")
	if _, errOut, code := p.run("record", "create", "--kind", "requirement", "--title", "R2", "--body-file", filepath.Join(p.root, "req2.md")); code == 0 || !strings.Contains(errOut, "changed under this daemon") {
		t.Fatalf("a write after the journal was replaced should be refused: %s", errOut)
	}
	if after, _ := os.ReadFile(journal); string(after) != string(data) {
		t.Fatal("the daemon wrote to the replaced journal")
	}
	gone := false
	for i := 0; i < 50 && !gone; i++ {
		gone = syscall.Kill(first, 0) != nil
		time.Sleep(100 * time.Millisecond)
	}
	if !gone {
		t.Fatal("the daemon with a replaced journal is still running")
	}
	rec := p.json("record", "create", "--kind", "requirement", "--title", "R2", "--body-file", filepath.Join(p.root, "req2.md"))
	if rec["id"] != "REQ-0002" || p.pid() == first {
		t.Fatalf("the next command should start a daemon on the current journal: %v", rec)
	}
}
