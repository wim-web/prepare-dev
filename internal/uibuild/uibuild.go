// Package uibuild checks that the committed decision UI build (ui/build) was made from
// the current UI sources and was not changed afterwards. The build writes
// build/build.json with the digest that SourceDigest computes (ui/vite.config.ts
// computes the same) and the sha256 of each output file.
package uibuild

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var reTest = regexp.MustCompile(`\.test\.[cm]?[jt]sx?$`)

// SourceFiles are the files the UI build is made from, relative to the UI directory
// with "/" separators, sorted: the top-level files that exist (index.html, package.json,
// pnpm-lock.yaml, tsconfig.json, vite.config.ts) and every file under src/ except tests
// and hidden files. ui/vite.config.ts lists the same; a new build input outside src/
// must be added to both.
func SourceFiles(dir string) ([]string, error) {
	var files []string
	for _, f := range []string{"index.html", "package.json", "pnpm-lock.yaml", "tsconfig.json", "vite.config.ts"} {
		if st, err := os.Stat(filepath.Join(dir, f)); err == nil && st.Mode().IsRegular() {
			files = append(files, f)
		}
	}
	err := filepath.WalkDir(filepath.Join(dir, "src"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() && !reTest.MatchString(d.Name()) {
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// SourceDigest is sha256 over "<path>\n<sha256 of the file>\n" for each source file.
func SourceDigest(dir string) (string, error) {
	files, err := SourceFiles(dir)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(data)
		b.WriteString(f + "\n" + hex.EncodeToString(sum[:]) + "\n")
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:]), nil
}

// Manifest is build/build.json.
type Manifest struct {
	Source string            `json:"source"`
	Files  map[string]string `json:"files"`
}

// Check fails when build/build.json or an output it lists is missing, when an output
// changed after the build, or when the sources changed since the build.
func Check(dir string) error {
	rebuild := fmt.Sprintf("build the UI again: cd %s && pnpm install && pnpm build", dir)
	data, err := os.ReadFile(filepath.Join(dir, "build", "build.json"))
	if err != nil {
		return fmt.Errorf("%s is missing; %s", filepath.Join(dir, "build", "build.json"), rebuild)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil || m.Source == "" {
		return fmt.Errorf("%s is not a build manifest; %s", filepath.Join(dir, "build", "build.json"), rebuild)
	}
	for _, f := range []string{"index.html", "THIRD_PARTY_NOTICES.txt"} {
		want, ok := m.Files[f]
		if !ok {
			return fmt.Errorf("build.json does not list %s; %s", f, rebuild)
		}
		data, err := os.ReadFile(filepath.Join(dir, "build", f))
		if err != nil {
			return fmt.Errorf("%s is missing; %s", filepath.Join(dir, "build", f), rebuild)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != want {
			return fmt.Errorf("%s changed after the build; %s", filepath.Join(dir, "build", f), rebuild)
		}
	}
	current, err := SourceDigest(dir)
	if err != nil {
		return err
	}
	if m.Source != current {
		return fmt.Errorf("the UI sources changed after the build in %s; %s", filepath.Join(dir, "build"), rebuild)
	}
	return nil
}
