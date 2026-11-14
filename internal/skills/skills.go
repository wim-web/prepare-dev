// このファイルは、作り直す前の版（main ブランチ）から持ってきた。変えたのは、記録を置くフォルダの名前だけ。
//
// Package skills copies the bundled skills into a project without losing the user's
// edits. It remembers the digest of every skill it installs (.ppdev/skills.json),
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
	// means, a symlink or a file, or outside the root.
	Conflicts []string `json:"conflicts"`
}

// Replacement is a skill moved aside before it was replaced.
type Replacement struct {
	Path   string `json:"path"`
	Backup string `json:"backup"`
}

// Options control Copy.
type Options struct {
	// Replace backs up and replaces skill directories that differ (not symlinks, files, or
	// destinations that are not directories under root).
	Replace bool
	// Now names the backup directory; zero means time.Now().
	Now time.Time
	// DryRun reports what Copy would do without writing anything (Plan uses it).
	DryRun bool
}

// ManifestPath is where the installed digests are kept.
func ManifestPath(root string) string {
	return filepath.Join(root, ".ppdev", "skills.json")
}

type manifest struct {
	// Installed maps "<destination relative to root>/<skill>" to the digest installed.
	Installed map[string]string `json:"installed"`
	// Destinations are the destinations (relative to root) of the last init that copied
	// skills, the ones status compares. skills.json written before 0.3.0 has none, and the
	// destinations of Installed stand for them.
	Destinations []string `json:"destinations,omitempty"`
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
	b.WriteString("}")
	if m.Destinations != nil {
		dj, _ := json.Marshal(m.Destinations)
		fmt.Fprintf(&b, ",\n  \"destinations\": %s", dj)
	}
	b.WriteString("\n}\n")
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

// Pending is what the next init would do to the skills it installed before (recorded in
// .ppdev/skills.json), as paths relative to root.
type Pending struct {
	// Copy are skills of this build that are not installed next to the others (removed,
	// or added by a newer build): init copies them.
	Copy []string `json:"copy"`
	// Update were installed by init, not edited since, and differ from this build: init
	// replaces them.
	Update []string `json:"update"`
	// Leave differ from this build and init leaves them as they are: edited by the user,
	// or not installed by init. init --replace-skills backs them up and replaces them.
	Leave []string `json:"leave"`
	// Manual are left alone even by init --replace-skills: a symlink or a file where a
	// skill directory goes, or a destination that is a symlink, a file or outside the
	// root. They are fixed by hand.
	Manual []string `json:"manual"`
}

// Plan runs Copy without writing anything into the destinations of the last init that
// copied skills (recorded in skills.json), so status says what init would do. It returns
// nil when init installed no skills (no skills.json, or only init --no-skills) or when
// init would change nothing.
func Plan(source fs.FS, root string) (*Pending, error) {
	m, err := readManifest(root)
	if err != nil {
		return nil, err
	}
	var dests []string
	if m.Destinations != nil {
		for _, d := range m.Destinations {
			dests = append(dests, filepath.Join(root, filepath.FromSlash(d)))
		}
	} else {
		seen := map[string]bool{}
		for key := range m.Installed {
			i := strings.LastIndex(key, "/")
			if i <= 0 || seen[key[:i]] {
				continue
			}
			seen[key[:i]] = true
			dests = append(dests, filepath.Join(root, filepath.FromSlash(key[:i])))
		}
	}
	if len(dests) == 0 {
		return nil, nil
	}
	sort.Strings(dests)
	res, err := Copy(source, dests, root, Options{DryRun: true})
	if err != nil {
		return nil, err
	}
	// what is still left with --replace-skills needs a hand
	forced, err := Copy(source, dests, root, Options{DryRun: true, Replace: true})
	if err != nil {
		return nil, err
	}
	manual := map[string]bool{}
	for _, x := range forced.Conflicts {
		manual[x] = true
	}
	var leave, byHand []string
	for _, x := range res.Conflicts {
		if manual[x] {
			byHand = append(byHand, x)
		} else {
			leave = append(leave, x)
		}
	}
	rel := func(paths []string) []string {
		out := []string{}
		for _, x := range paths {
			if r, err := filepath.Rel(root, x); err == nil {
				out = append(out, filepath.ToSlash(r))
			}
		}
		return out
	}
	pending := &Pending{Copy: rel(res.Copied), Update: rel(res.Updated), Leave: rel(leave), Manual: rel(byHand)}
	if len(pending.Copy) == 0 && len(pending.Update) == 0 && len(pending.Leave) == 0 && len(pending.Manual) == 0 {
		return nil, nil
	}
	return pending, nil
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
	backupDir := filepath.Join(root, ".ppdev", "skills-backup", now.UTC().Format("20060102T150405Z"))
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
		if !opts.DryRun {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return res, err
			}
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
				if !opts.DryRun {
					if err := copyTree(source, name, to); err != nil {
						return res, err
					}
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
				if !opts.DryRun {
					if err := replace(source, name, to, ""); err != nil {
						return res, err
					}
				}
				m.Installed[key] = want
				changed = true
				res.Updated = append(res.Updated, to)
			case opts.Replace:
				backup := filepath.Join(backupDir, relDest, name)
				if !opts.DryRun {
					if err := replace(source, name, to, backup); err != nil {
						return res, err
					}
				}
				m.Installed[key] = want
				changed = true
				res.Replaced = append(res.Replaced, Replacement{Path: to, Backup: backup})
			default:
				res.Conflicts = append(res.Conflicts, to)
			}
		}
	}
	// the destinations of this init are the ones status compares from now on (init
	// --skills-dir leaves the others out; init --no-skills passes none and keeps them)
	if len(destinations) > 0 {
		rels := []string{}
		for _, d := range destinations {
			if r, err := filepath.Rel(root, d); err == nil {
				rels = append(rels, filepath.ToSlash(r))
			}
		}
		sort.Strings(rels)
		if strings.Join(rels, "\x00") != strings.Join(m.Destinations, "\x00") || m.Destinations == nil {
			m.Destinations = rels
			changed = true
		}
	}
	if changed && !opts.DryRun {
		if err := writeManifest(root, m); err != nil {
			return res, err
		}
	}
	return res, nil
}

// replace copies the new version next to the skill, then swaps it in. The old tree is
// moved to backup, or removed when backup is "".
func replace(source fs.FS, name, to, backup string) error {
	fresh := to + ".ppdev-new"
	if err := os.RemoveAll(fresh); err != nil {
		return err
	}
	if err := copyTree(source, name, fresh); err != nil {
		return err
	}
	old := backup
	if old == "" {
		old = to + ".ppdev-old"
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
