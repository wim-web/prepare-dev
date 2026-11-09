// Package skills copies the bundled skills into a project without losing the user's
// edits. It remembers the digest of every skill it installs (.prepare-dev/skills.json),
// so a later version can replace a skill nobody edited since, and backs up an edited
// skill before replacing it when asked to.
package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Result lists what happened to each skill directory (absolute paths).
type Result struct {
	// Copied were missing and are now installed.
	Copied []string `json:"copied"`
	// Updated were installed by prepare-dev, unedited since, and replaced by this version.
	Updated []string `json:"updated"`
	// Unchanged already match this version.
	Unchanged []string `json:"unchanged"`
	// Replaced differed from both this version and what prepare-dev installed; they were
	// moved to Backup and replaced (only with Options.Replace).
	Replaced []Replacement `json:"replaced"`
	// Conflicts differ and were left alone: edited by the user, installed by another
	// means, a symlink, or outside the root.
	Conflicts []string `json:"conflicts"`
}

// Replacement is a skill moved aside before it was replaced.
type Replacement struct {
	Path   string `json:"path"`
	Backup string `json:"backup"`
}

// Options control Copy.
type Options struct {
	// Replace backs up and replaces skills that differ (not symlinks or paths outside root).
	Replace bool
	// Now names the backup directory; zero means time.Now().
	Now time.Time
}

// ManifestPath is where the installed digests are kept.
func ManifestPath(root string) string {
	return filepath.Join(root, ".prepare-dev", "skills.json")
}

type manifest struct {
	// Installed maps "<destination relative to root>/<skill>" to the digest installed.
	Installed map[string]string `json:"installed"`
}

func readManifest(root string) (manifest, error) {
	m := manifest{Installed: map[string]string{}}
	data, err := os.ReadFile(ManifestPath(root))
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("%s: %w", ManifestPath(root), err)
	}
	if m.Installed == nil {
		m.Installed = map[string]string{}
	}
	return m, nil
}

func writeManifest(root string, m manifest) error {
	keys := make([]string, 0, len(m.Installed))
	for k := range m.Installed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{\n  \"installed\": {")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		kj, _ := json.Marshal(k)
		vj, _ := json.Marshal(m.Installed[k])
		fmt.Fprintf(&b, "\n    %s: %s", kj, vj)
	}
	if len(keys) > 0 {
		b.WriteString("\n  ")
	}
	b.WriteString("}\n}\n")
	path := ManifestPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Digest hashes a tree: each entry's relative path, then file bytes ("symlink" for
// anything that is neither a file nor a directory), in sorted order.
func Digest(fsys fs.FS, dir string) string {
	h := sha256.New()
	var walk func(base string)
	walk = func(base string) {
		entries, err := fs.ReadDir(fsys, base)
		if err != nil {
			return
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, e := range entries {
			p := base + "/" + e.Name()
			rel := strings.TrimPrefix(p, dir+"/")
			h.Write([]byte(filepath.FromSlash(rel)))
			switch {
			case e.IsDir():
				walk(p)
			case e.Type().IsRegular():
				data, _ := fs.ReadFile(fsys, p)
				h.Write(data)
			default:
				h.Write([]byte("symlink"))
			}
		}
	}
	walk(dir)
	return hex.EncodeToString(h.Sum(nil))
}

// insideRoot reports whether dest (which may not exist yet) lies under root once
// symlinks of its existing ancestors are resolved.
func insideRoot(realRoot, dest string) bool {
	ancestor := dest
	for {
		if _, err := os.Lstat(ancestor); err == nil || ancestor == filepath.Dir(ancestor) {
			break
		}
		ancestor = filepath.Dir(ancestor)
	}
	realAnc, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(realRoot, realAnc)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Copy installs every skill (a directory with SKILL.md) of source into each destination
// (absolute paths under root).
func Copy(source fs.FS, destinations []string, root string, opts Options) (Result, error) {
	res := Result{Copied: []string{}, Updated: []string{}, Unchanged: []string{}, Replaced: []Replacement{}, Conflicts: []string{}}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return res, err
	}
	m, err := readManifest(root)
	if err != nil {
		return res, err
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	backupDir := filepath.Join(root, ".prepare-dev", "skills-backup", now.UTC().Format("20060102T150405Z"))
	names, err := fs.ReadDir(source, ".")
	if err != nil {
		return res, err
	}
	sort.Slice(names, func(i, j int) bool { return names[i].Name() < names[j].Name() })
	changed := false
	for _, dest := range destinations {
		if !insideRoot(realRoot, dest) {
			res.Conflicts = append(res.Conflicts, dest)
			continue
		}
		if st, err := os.Lstat(dest); err == nil && (!st.IsDir() || st.Mode()&fs.ModeSymlink != 0) {
			res.Conflicts = append(res.Conflicts, dest)
			continue
		}
		relDest, err := filepath.Rel(root, dest)
		if err != nil {
			return res, err
		}
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return res, err
		}
		for _, n := range names {
			name := n.Name()
			if !n.IsDir() {
				continue
			}
			if _, err := fs.Stat(source, name+"/SKILL.md"); err != nil {
				continue
			}
			want := Digest(source, name)
			key := filepath.ToSlash(filepath.Join(relDest, name))
			to := filepath.Join(dest, name)
			st, err := os.Lstat(to)
			if errors.Is(err, fs.ErrNotExist) {
				if err := copyTree(source, name, to); err != nil {
					return res, err
				}
				m.Installed[key] = want
				changed = true
				res.Copied = append(res.Copied, to)
				continue
			}
			if err != nil {
				return res, err
			}
			if !st.IsDir() || st.Mode()&fs.ModeSymlink != 0 {
				res.Conflicts = append(res.Conflicts, to)
				continue
			}
			have := Digest(os.DirFS(dest), name)
			switch {
			case have == want:
				if m.Installed[key] != want {
					m.Installed[key] = want
					changed = true
				}
				res.Unchanged = append(res.Unchanged, to)
			case m.Installed[key] == have:
				// installed by prepare-dev and not edited since: take this version
				if err := replace(source, name, to, ""); err != nil {
					return res, err
				}
				m.Installed[key] = want
				changed = true
				res.Updated = append(res.Updated, to)
			case opts.Replace:
				backup := filepath.Join(backupDir, relDest, name)
				if err := replace(source, name, to, backup); err != nil {
					return res, err
				}
				m.Installed[key] = want
				changed = true
				res.Replaced = append(res.Replaced, Replacement{Path: to, Backup: backup})
			default:
				res.Conflicts = append(res.Conflicts, to)
			}
		}
	}
	if changed {
		if err := writeManifest(root, m); err != nil {
			return res, err
		}
	}
	return res, nil
}

// replace copies the new version next to the skill, then swaps it in. The old tree is
// moved to backup, or removed when backup is "".
func replace(source fs.FS, name, to, backup string) error {
	fresh := to + ".prepare-dev-new"
	if err := os.RemoveAll(fresh); err != nil {
		return err
	}
	if err := copyTree(source, name, fresh); err != nil {
		return err
	}
	old := backup
	if old == "" {
		old = to + ".prepare-dev-old"
		if err := os.RemoveAll(old); err != nil {
			return err
		}
	} else if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		return err
	}
	if err := os.Rename(to, old); err != nil {
		return err
	}
	if err := os.Rename(fresh, to); err != nil {
		_ = os.Rename(old, to)
		return err
	}
	if backup == "" {
		return os.RemoveAll(old)
	}
	return nil
}

func copyTree(source fs.FS, dir, to string) error {
	return fs.WalkDir(source, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(to, filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(p, dir), "/")))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(source, p)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		if _, err := f.Write(data); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	})
}
