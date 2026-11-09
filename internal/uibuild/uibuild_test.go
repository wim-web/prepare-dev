package uibuild

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// [B1] The decision UI embedded in the binary (ui/build) is the build of the UI sources
// the U1 tests check.
func TestEmbeddedBuild(t *testing.T) {
	t.Run("[B1] the committed UI build was made from the current UI sources", func(t *testing.T) {
		if err := Check(filepath.Join("..", "..", "ui")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("[B1] a source change after the build, or a changed output, is detected", func(t *testing.T) {
		dir := t.TempDir()
		for name, body := range map[string]string{"index.html": "<div id=root>", "package.json": "{}", "src/main.tsx": "x", "src/App.test.tsx": "t", "build/index.html": "built", "build/THIRD_PARTY_NOTICES.txt": "n"} {
			p := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		digest, err := SourceDigest(dir)
		if err != nil {
			t.Fatal(err)
		}
		sha := func(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
		manifest, _ := json.Marshal(Manifest{Source: digest, Files: map[string]string{"index.html": sha("built"), "THIRD_PARTY_NOTICES.txt": sha("n")}})
		if err := os.WriteFile(filepath.Join(dir, "build", "build.json"), manifest, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := Check(dir); err != nil {
			t.Fatal(err)
		}
		write := func(name, body string) {
			if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		// a test file does not change the build
		write("src/App.test.tsx", "t2")
		if err := Check(dir); err != nil {
			t.Fatalf("a test change should not count: %v", err)
		}
		// an output edited by hand
		write("build/index.html", "edited")
		if err := Check(dir); err == nil || !strings.Contains(err.Error(), "changed after the build") {
			t.Fatalf("an edited output should be detected: %v", err)
		}
		write("build/index.html", "built")
		// a source changed (or added) without a new build
		write("src/extra.tsx", "y")
		if err := Check(dir); err == nil || !strings.Contains(err.Error(), "sources changed") {
			t.Fatalf("a source change should be detected: %v", err)
		}
	})
}
