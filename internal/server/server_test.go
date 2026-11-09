package server

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wim-web/prepare-dev/internal/core"
	"github.com/wim-web/prepare-dev/internal/runtime"
)

func start(t *testing.T, onShutdown func()) (*runtime.Runtime, *Server, *DaemonInfo) {
	t.Helper()
	rt, err := runtime.Open(t.TempDir(), runtime.Options{})
	if err != nil {
		t.Fatal(err)
	}
	srv := New(rt, Options{OnShutdown: onShutdown})
	info, err := srv.Listen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return rt, srv, info
}

func request(t *testing.T, method, url, body string, header map[string]string) int {
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
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return res.StatusCode
}

func TestHTTP(t *testing.T) {
	t.Run("[U2] the old UI endpoint is gone, a question with UI data is refused, and other origins are refused", func(t *testing.T) {
		rt, _, info := start(t, nil)
		rt.Lock()
		req, err := rt.Store.CreateRecord("requirement", core.OM("title", "R"), "r\n")
		if err != nil {
			t.Fatal(err)
		}
		q, err := rt.Questions.Create(runtime.CreateQuestionInput{Kind: "free_text", Prompt: "Q", Subjects: []string{req.ID}})
		rt.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if code := request(t, "GET", info.URL+"/api/questions/"+q.ID+"/ui", "", nil); code != 404 {
			t.Fatalf("old ui endpoint: %d", code)
		}
		body := fmt.Sprintf(`{"kind":"free_text","prompt":"Q","subjects":[%q],"ui":{}}`, req.ID)
		if code := request(t, "POST", info.URL+"/api/questions", body, nil); code != 400 {
			t.Fatalf("question with ui: %d", code)
		}
		if code := request(t, "GET", info.URL+"/api/status", "", map[string]string{"Origin": "https://example.invalid"}); code != 403 {
			t.Fatalf("other origin: %d", code)
		}
		if code := request(t, "GET", info.URL+"/api/status", "", map[string]string{"Origin": info.URL}); code != 200 {
			t.Fatalf("same origin: %d", code)
		}
	})

	t.Run("[A1] answers are accepted only from the decision UI: a request without the page's Origin is refused", func(t *testing.T) {
		rt, _, info := start(t, nil)
		rt.Lock()
		req, err := rt.Store.CreateRecord("requirement", core.OM("title", "R"), "r\n")
		if err != nil {
			t.Fatal(err)
		}
		q, err := rt.Questions.Create(runtime.CreateQuestionInput{Kind: "free_text", Prompt: "Q", Subjects: []string{req.ID}})
		rt.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		answer := `{"choice":[],"text":"決めた"}`
		if code := request(t, "POST", info.URL+"/api/questions/"+q.ID+"/answers", answer, nil); code != 403 {
			t.Fatalf("answer without Origin: %d", code)
		}
		if code := request(t, "POST", info.URL+"/api/questions/"+q.ID+"/answers", answer, map[string]string{"Origin": "http://localhost:1"}); code != 403 {
			t.Fatalf("answer from another origin: %d", code)
		}
		if code := request(t, "POST", info.URL+"/api/questions/"+q.ID+"/answers", answer, map[string]string{"Origin": info.URL}); code != 201 {
			t.Fatalf("answer from the UI: %d", code)
		}
	})

	t.Run("[P3] refuses new work and an already-open request body after accepting shutdown", func(t *testing.T) {
		rt, _, info := start(t, func() {})
		host := strings.TrimPrefix(info.URL, "http://")
		conn, err := net.Dial("tcp", host)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		full := `{"plugin":"vitest","claims":[]}`
		fmt.Fprintf(conn, "POST /api/runs HTTP/1.1\r\nHost: %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n", host, len(full))
		fmt.Fprint(conn, `{"plugin":`)
		time.Sleep(50 * time.Millisecond)
		if code := request(t, "GET", info.URL+"/api/identity", "", nil); code != 200 {
			t.Fatalf("identity: %d", code)
		}
		if code := request(t, "POST", info.URL+"/api/shutdown", "", map[string]string{"Authorization": "Bearer " + info.ShutdownToken}); code != 200 {
			t.Fatalf("shutdown: %d", code)
		}
		fmt.Fprint(conn, full[len(`{"plugin":`):])
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(line, " 409 ") {
			t.Fatalf("open request after shutdown: %s", line)
		}
		if code := request(t, "POST", info.URL+"/api/runs", full, nil); code != 409 {
			t.Fatalf("new request after shutdown: %d", code)
		}
		if jobs := rt.Runs.Jobs(); len(jobs) != 0 {
			t.Fatalf("jobs started: %v", jobs)
		}
	})

	t.Run("shutdown needs the token", func(t *testing.T) {
		_, _, info := start(t, func() {})
		if code := request(t, "POST", info.URL+"/api/shutdown", "", nil); code != 403 {
			t.Fatalf("shutdown without token: %d", code)
		}
	})
}
