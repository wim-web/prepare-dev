package plugin

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Exec stops the whole process group when a tool runs past its timeout: a child the
// tool left behind neither keeps running nor keeps the output pipe (and the run) open.
func TestExecTimeoutStopsTheProcessGroup(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "child-still-running")
	start := time.Now()
	res, err := Exec("sh", []string{"-c", "(sleep 2 && touch " + marker + ") & sleep 30"}, ExecOptions{Dir: dir, Timeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || res.Signal == "" || res.Code != -1 {
		t.Fatalf("result after the timeout: %+v", res)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("Exec returned after %v: the child kept the output open", d)
	}
	time.Sleep(2500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the child of the tool kept running after the timeout")
	}
}
