// Command release builds prepare-dev for macOS and Linux and packs each binary with the
// README and the third-party notices:
//
//	go run ./scripts/release -version 0.2.0
//
// It writes dist/prepare-dev-<version>-<os>-<arch>.tar.gz and dist/artifact.json with the
// sha256 of every archive and binary. The decision UI is the committed build in ui/build;
// rebuild it (cd ui && pnpm build) before releasing a UI change: the release refuses a
// build whose recorded source digest differs from the current UI sources.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/wim-web/prepare-dev/internal/uibuild"
)

type artifact struct {
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	File         string `json:"file"`
	SHA256       string `json:"sha256"`
	BinarySHA256 string `json:"binary_sha256"`
}

func main() {
	version := flag.String("version", "", "version to embed (required)")
	out := flag.String("out", "dist", "output directory")
	targets := flag.String("targets", "darwin/arm64,darwin/amd64,linux/arm64,linux/amd64", "comma-separated os/arch list")
	flag.Parse()
	if *version == "" {
		fail("-version is required")
	}
	if _, err := os.Stat("docs/distribution-readme.md"); err != nil {
		fail("docs/distribution-readme.md is missing (run from the repository root)")
	}
	// the embedded UI must be the build of the current UI sources
	if err := uibuild.Check("ui"); err != nil {
		fail("%v", err)
	}
	notices, err := thirdPartyNotices()
	if err != nil {
		fail("notices: %v", err)
	}
	readme, err := os.ReadFile("docs/distribution-readme.md")
	if err != nil {
		fail("%v", err)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fail("%v", err)
	}
	var arts []artifact
	for _, target := range strings.Split(*targets, ",") {
		goos, goarch, ok := strings.Cut(strings.TrimSpace(target), "/")
		if !ok {
			fail("bad target %q", target)
		}
		tmp, err := os.MkdirTemp("", "prepare-dev-release-")
		if err != nil {
			fail("%v", err)
		}
		bin := filepath.Join(tmp, "prepare-dev")
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w -X main.version="+*version, "-o", bin, "./cmd/prepare-dev")
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fail("build %s: %v", target, err)
		}
		binData, err := os.ReadFile(bin)
		if err != nil {
			fail("%v", err)
		}
		name := fmt.Sprintf("prepare-dev-%s-%s-%s", *version, goos, goarch)
		archive, err := pack(name, map[string][]byte{"prepare-dev": binData, "README.md": readme, "THIRD_PARTY_NOTICES.txt": notices})
		if err != nil {
			fail("%v", err)
		}
		file := filepath.Join(*out, name+".tar.gz")
		if err := os.WriteFile(file, archive, 0o644); err != nil {
			fail("%v", err)
		}
		os.RemoveAll(tmp)
		arts = append(arts, artifact{OS: goos, Arch: goarch, File: filepath.Base(file), SHA256: sum(archive), BinarySHA256: sum(binData)})
		fmt.Printf("%s  %s\n", sum(archive), file)
	}
	manifest, _ := json.MarshalIndent(map[string]any{"version": *version, "artifacts": arts}, "", "  ")
	if err := os.WriteFile(filepath.Join(*out, "artifact.json"), append(manifest, '\n'), 0o644); err != nil {
		fail("%v", err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "release: "+format+"\n", args...)
	os.Exit(1)
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// pack writes a gzipped tar with fixed times and modes, so the same inputs give the
// same archive.
func pack(dir string, files map[string][]byte) ([]byte, error) {
	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	gz.ModTime = time.Unix(0, 0)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	epoch := time.Unix(0, 0)
	if err := tw.WriteHeader(&tar.Header{Name: dir + "/", Mode: 0o755, Typeflag: tar.TypeDir, ModTime: epoch, Format: tar.FormatPAX}); err != nil {
		return nil, err
	}
	for _, n := range names {
		mode := int64(0o644)
		if n == "prepare-dev" {
			mode = 0o755
		}
		hdr := &tar.Header{Name: dir + "/" + n, Mode: mode, Size: int64(len(files[n])), ModTime: epoch, Typeflag: tar.TypeReg, Format: tar.FormatPAX}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(files[n]); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var licenseFile = regexp.MustCompile(`(?i)^(licen[cs]e|copying|notice)`)

// thirdPartyNotices lists the Go standard library and the modules linked into the binary,
// then appends the notices of the UI build.
func thirdPartyNotices() ([]byte, error) {
	var b strings.Builder
	b.WriteString("prepare-dev includes the following third-party software.\n")
	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return nil, err
	}
	if text, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(goroot)), "LICENSE")); err == nil {
		section(&b, "Go standard library and runtime", string(text))
	}
	out, err := exec.Command("go", "list", "-deps", "-f", "{{with .Module}}{{if not .Main}}{{.Path}}@{{.Version}} {{.Dir}}{{end}}{{end}}", "./cmd/prepare-dev").Output()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var mods []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" && !seen[line] {
			seen[line] = true
			mods = append(mods, line)
		}
	}
	sort.Strings(mods)
	for _, m := range mods {
		id, dir, _ := strings.Cut(m, " ")
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		var texts []string
		for _, e := range entries {
			if !e.IsDir() && licenseFile.MatchString(e.Name()) {
				data, err := os.ReadFile(filepath.Join(dir, e.Name()))
				if err != nil {
					return nil, err
				}
				texts = append(texts, string(data))
			}
		}
		if len(texts) == 0 {
			return nil, fmt.Errorf("no license file for %s", id)
		}
		section(&b, id, strings.Join(texts, "\n"))
	}
	ui, err := os.ReadFile("ui/build/THIRD_PARTY_NOTICES.txt")
	if err != nil {
		return nil, err
	}
	b.WriteString("\n")
	b.WriteString(strings.Repeat("=", 72) + "\nDecision UI (bundled into the binary)\n" + strings.Repeat("=", 72) + "\n")
	b.Write(ui)
	return []byte(b.String()), nil
}

func section(b *strings.Builder, title, text string) {
	b.WriteString("\n" + strings.Repeat("-", 72) + "\n" + title + "\n" + strings.Repeat("-", 72) + "\n")
	b.WriteString(strings.TrimRight(text, "\n") + "\n")
}
