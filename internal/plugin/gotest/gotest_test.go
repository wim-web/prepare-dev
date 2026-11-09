package gotest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/wim-web/prepare-dev/internal/core"
	"github.com/wim-web/prepare-dev/internal/plugin"
	rt "github.com/wim-web/prepare-dev/internal/runtime"
)

func write(t *testing.T, root, file, body string) {
	t.Helper()
	abs := filepath.Join(root, file)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInputs(t *testing.T) {
	t.Run("[V3] includes the sources and embedded files of the packages the tests build, go.mod and implementation files", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "go.mod", "module example.com/fixture\n\ngo 1.22\n")
		write(t, root, "lib/lib.go", "package lib\n\nimport _ \"embed\"\n\n//go:embed data.txt\nvar Data string\n\nfunc Value() int { return 1 }\n")
		write(t, root, "lib/data.txt", "hello\n")
		write(t, root, "app/app.go", "package app\n\nimport \"example.com/fixture/lib\"\n\nfunc Run() int { return lib.Value() }\n")
		write(t, root, "app/app_test.go", "package app\n\nimport \"testing\"\n\nfunc TestRun(t *testing.T) {\n\tif Run() != 1 {\n\t\tt.Fatal()\n\t}\n}\n")
		write(t, root, "unrelated/u.go", "package unrelated\n")
		write(t, root, "traces.jsonl", "{}\n")
		job := &plugin.Job{Root: root, RunID: "RUN-test", Plugin: "gotest", Targets: []string{"app/app_test.go"}, Config: core.OM("impl_files", []any{"traces.jsonl"})}
		p := New()
		before, err := p.Inputs(job)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, f := range before.Files {
			got[f.Path] = f.SHA256
		}
		for _, want := range []string{"app/app.go", "lib/lib.go", "lib/data.txt", "go.mod", "traces.jsonl"} {
			if _, ok := got[want]; !ok {
				t.Fatalf("missing input %s in %v", want, got)
			}
		}
		if _, ok := got["unrelated/u.go"]; ok {
			t.Fatal("a package the tests do not build was included")
		}
		if len(before.Targets) != 1 || before.Targets[0].Path != "app/app_test.go" {
			t.Fatalf("targets: %v", before.Targets)
		}
		for _, file := range []string{"lib/lib.go", "lib/data.txt", "go.mod"} {
			initial, _ := os.ReadFile(filepath.Join(root, file))
			write(t, root, file, string(initial)+"\n")
			after, err := p.Inputs(job)
			if err != nil {
				t.Fatal(err)
			}
			changed := false
			for _, f := range after.Files {
				if f.Path == file && f.SHA256 != got[file] {
					changed = true
				}
			}
			if !changed {
				t.Fatalf("%s: change not reflected in its sha256", file)
			}
			write(t, root, file, string(initial))
		}
	})

	t.Run("[V3] includes packages the tests build at run time, testdata, impl_files patterns, non-Go sources and local replacements, but not test files of dependencies", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "go.mod", "module example.com/fixture\n\ngo 1.22\n\nrequire example.com/local v0.0.0\n\nreplace example.com/local => ./third_party/local\n")
		write(t, root, "third_party/local/go.mod", "module example.com/local\n\ngo 1.22\n")
		write(t, root, "third_party/local/local.go", "package local\n\nfunc V() int { return 2 }\n")
		write(t, root, "lib/lib.go", "package lib\n\nimport \"example.com/local\"\n\nfunc Value() int { return local.V() }\n")
		write(t, root, "lib/lib.h", "/* header shipped with the package */\n")
		write(t, root, "lib/lib_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {}\n")
		write(t, root, "lib/testdata/not-mine.txt", "x\n")
		write(t, root, "cmd/tool/main.go", "package main\n\nimport \"example.com/fixture/lib\"\n\nfunc main() { println(lib.Value()) }\n")
		write(t, root, "e2e/e2e_test.go", "package e2e\n\nimport \"testing\"\n\n// builds ./cmd/tool with go build and reads files at run time\nfunc TestTool(t *testing.T) {}\n")
		write(t, root, "e2e/testdata/golden/a.json", "{}\n")
		write(t, root, "traces/a.jsonl", "{}\n")
		write(t, root, "traces/deep/b.jsonl", "{}\n")
		write(t, root, "traces/.DS_Store", "x")
		job := &plugin.Job{Root: root, RunID: "RUN-test", Plugin: "gotest", Targets: []string{"e2e/e2e_test.go"},
			Config: core.OM("input_packages", []any{"./cmd/tool"}, "impl_files", []any{"traces/**"})}
		in, err := New().Inputs(job)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, f := range in.Files {
			got[f.Path] = f.Role
		}
		for want, role := range map[string]string{"cmd/tool/main.go": "import", "lib/lib.go": "import", "lib/lib.h": "import", "third_party/local/local.go": "import",
			"e2e/testdata/golden/a.json": "data", "traces/a.jsonl": "implementation", "traces/deep/b.jsonl": "implementation", "go.mod": "config"} {
			if got[want] != role {
				t.Fatalf("input %s: role %q, want %q (inputs %v)", want, got[want], role, got)
			}
		}
		for _, absent := range []string{"lib/lib_test.go", "lib/testdata/not-mine.txt", "traces/.DS_Store"} {
			if _, ok := got[absent]; ok {
				t.Fatalf("%s should not be an input (inputs %v)", absent, got)
			}
		}
		listings := map[string]bool{}
		for _, l := range in.Listings {
			listings[fmt.Sprintf("%s|%v", l.Path, l.Recursive)] = true
		}
		for _, want := range []string{"e2e|false", "lib|false", "cmd/tool|false", "e2e/testdata|true", "traces|true"} {
			if !listings[want] {
				t.Fatalf("missing listing %s in %v", want, in.Listings)
			}
		}
		job.Config = core.OM("impl_files", []any{"missing/**"})
		if _, err := New().Inputs(job); err == nil {
			t.Fatal("an impl_files pattern without matches should fail")
		}
	})

	t.Run("[V3] a file added to a package, testdata or a pattern's directory changes its listing", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "go.mod", "module example.com/fixture\n\ngo 1.22\n")
		write(t, root, "app/app.go", "package app\n")
		write(t, root, "app/app_test.go", "package app\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n")
		write(t, root, "app/testdata/x.txt", "x\n")
		write(t, root, "traces/a.jsonl", "{}\n")
		job := &plugin.Job{Root: root, RunID: "RUN-test", Plugin: "gotest", Targets: []string{"app/app_test.go"}, Config: core.OM("impl_files", []any{"traces/*.jsonl"})}
		in, err := New().Inputs(job)
		if err != nil {
			t.Fatal(err)
		}
		recorded := map[string]core.InputListing{}
		for _, l := range in.Listings {
			recorded[l.Path] = l
		}
		if changed := changedListings(root, in); len(changed) != 0 {
			t.Fatalf("listings changed without a change: %v", changed)
		}
		// testdata counts hidden names too: every file under it is an input
		for dir, file := range map[string]string{"app": "app/extra.go", "app/testdata": "app/testdata/sub/y.txt", "app/testdata/": "app/testdata/.hidden", "traces": "traces/b.jsonl"} {
			dir = strings.TrimSuffix(dir, "/")
			write(t, root, file, "package app\n")
			if l, ok := recorded[dir]; !ok || plugin.CurrentDigest(root, l) == l.SHA256 {
				t.Fatalf("adding %s did not change the listing of %s (%+v)", file, dir, l)
			}
			if err := os.Remove(filepath.Join(root, file)); err != nil {
				t.Fatal(err)
			}
		}
	})

	t.Run("[V3] runs go test without the cache and maps test names to claims", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "go.mod", "module example.com/fixture\n\ngo 1.22\n")
		write(t, root, "app/app_test.go", "package app\n\nimport \"testing\"\n\nfunc TestClaims(t *testing.T) {\n\tt.Run(\"[C1] passes\", func(t *testing.T) {})\n\tt.Run(\"[C2] fails\", func(t *testing.T) { t.Fatal(\"boom\") })\n\tt.Run(\"[C3] skipped\", func(t *testing.T) { t.Skip() })\n}\n")
		claim := func(id string) plugin.JobClaim {
			return plugin.JobClaim{Spec: "SPEC-0001", Claim: id, SpecRef: core.SubjectRef{ID: "SPEC-0001", Rev: 1, SHA256: "x"}, NativeRef: "app::[" + id + "]"}
		}
		job := &plugin.Job{Root: root, RunID: "RUN-test", Plugin: "gotest", Targets: []string{"app/app_test.go"}, Config: core.NewOMap(),
			Claims: []plugin.JobClaim{claim("C1"), claim("C2"), claim("C3"), claim("C4")}}
		p := New()
		in, err := p.Inputs(job)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := p.Run(job, plugin.Context{ArtifactDir: t.TempDir(), OnProgress: func(string) {}})
		if err != nil {
			t.Fatal(err)
		}
		rec, err := p.Parse(raw, job, in)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"C1": "verified", "C2": "failed", "C3": "unknown", "C4": "unknown"}
		for _, c := range rec.Claims {
			if c.Result != want[c.Claim] {
				t.Fatalf("%s: %s (want %s): %s", c.Claim, c.Result, want[c.Claim], rec.Summary)
			}
		}
		if rec.Result != "failed" || rec.Method != "test" || !rec.Scope.Bounded {
			t.Fatalf("record: %s %s", rec.Result, rec.Summary)
		}
	})
}

// changedListings returns the recorded listings whose digest is different now.
func changedListings(root string, in plugin.Inputs) []string {
	var out []string
	for _, l := range in.Listings {
		if plugin.CurrentDigest(root, l) != l.SHA256 {
			out = append(out, fmt.Sprintf("%s|%v|%s|%s", l.Path, l.Recursive, l.Mode, l.Match))
		}
	}
	return out
}

func inputPaths(in plugin.Inputs) string {
	var out []string
	for _, f := range in.Files {
		out = append(out, f.Path)
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

func TestEmbedListings(t *testing.T) {
	t.Run("[V3] a file added under a directory that go:embed assets/* matches makes the old evidence stale, and a new run fails", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "go.mod", "module example.com/fixture\n\ngo 1.22\n")
		write(t, root, "app/app.go", "package app\n\nimport \"embed\"\n\n//go:embed assets/*\nvar Assets embed.FS\n")
		write(t, root, "app/assets/nested/one.txt", "one\n")
		write(t, root, "app/app_test.go", "package app\n\nimport \"testing\"\n\nfunc TestAssets(t *testing.T) {\n\tfiles, err := Assets.ReadDir(\"assets/nested\")\n\tif err != nil || len(files) != 1 {\n\t\tt.Fatalf(\"files=%d err=%v\", len(files), err)\n\t}\n}\n")
		r, err := rt.Open(root, rt.Options{})
		if err != nil {
			t.Fatal(err)
		}
		spec, err := r.Store.CreateRecord("spec", core.OM("title", "Assets", "claims", []any{core.OM("id", "C1", "statement", "exactly one asset", "native", []any{core.OM("plugin", "gotest", "ref", "app::TestAssets")})}), "")
		if err != nil {
			t.Fatal(err)
		}
		job := &plugin.Job{Root: root, RunID: "RUN-0001", Plugin: "gotest", Targets: []string{"app/app_test.go"}, Config: core.NewOMap(),
			Claims: []plugin.JobClaim{{Spec: spec.ID, Claim: "C1", SpecRef: core.SubjectRef{ID: spec.ID, Rev: spec.Rev, SHA256: spec.SHA256}, NativeRef: "app::TestAssets"}}}
		p := New()
		runOnce := func() *core.RunRecord {
			in, err := p.Inputs(job)
			if err != nil {
				t.Fatal(err)
			}
			out, err := p.Run(job, plugin.Context{ArtifactDir: t.TempDir(), OnProgress: func(string) {}})
			if err != nil {
				t.Fatal(err)
			}
			rec, err := p.Parse(out, job, in)
			if err != nil {
				t.Fatal(err)
			}
			rec.Inputs.Listings = in.Listings
			return rec
		}
		before := runOnce()
		if before.Result != "verified" || !r.Runs.Applicability(before).Applicable {
			t.Fatalf("the fixture should pass and apply: %s", before.Summary)
		}
		write(t, root, "app/assets/nested/two.txt", "two\n")
		app := r.Runs.Applicability(before)
		if app.Applicable || len(app.Mismatches) == 0 || app.Mismatches[0].Kind != "listing" {
			t.Fatalf("the old evidence still applies after an embedded file was added: %+v", app)
		}
		if after := runOnce(); after.Result != "failed" {
			t.Fatalf("the fixture should fail with the added asset: %s", after.Summary)
		}
	})

	t.Run("[V3] the listings of go:embed patterns change exactly when the files go list embeds do", func(t *testing.T) {
		embed := func(pattern string) map[string]string {
			return map[string]string{"app/app.go": "package app\n\nimport \"embed\"\n\n//go:embed " + pattern + "\nvar F embed.FS\n"}
		}
		cases := []struct {
			name   string
			src    map[string]string
			files  []string
			add    string // a path ending in / adds an empty directory
			embeds bool   // what go list says (checked too)
		}{
			{"a file in a subdirectory of a wildcard's match", embed("assets/*"), []string{"app/assets/nested/one.txt"}, "app/assets/nested/deeper/two.txt", true},
			{"a new directory a wildcard matches", embed("assets/*"), []string{"app/assets/one.txt"}, "app/assets/new/two.txt", true},
			{"a hidden file a wildcard matches", embed("assets/*"), []string{"app/assets/one.txt"}, "app/assets/.hidden", true},
			{"a file deep under a directory pattern", embed("assets"), []string{"app/assets/one.txt"}, "app/assets/a/b/two.txt", true},
			{"a file under node_modules of a directory pattern", embed("web"), []string{"app/web/index.html"}, "app/web/node_modules/x/y.js", true},
			{"a hidden file under an all: directory", embed("all:assets"), []string{"app/assets/one.txt"}, "app/assets/sub/.hidden", true},
			{"a match two levels below a wildcard", embed("static/*/img/*.png"), []string{"app/static/a/img/x.png"}, "app/static/b/img/y.png", true},
			{"a file under a directory a test file embeds", map[string]string{"app/app.go": "package app\n", "app/app_test.go": "package app\n\nimport (\n\t\"embed\"\n\t\"testing\"\n)\n\n//go:embed tdir\nvar T embed.FS\n\nfunc TestA(t *testing.T) {}\n"}, []string{"app/tdir/x.txt"}, "app/tdir/sub/y.txt", true},
			{"a match of an external test file's pattern", map[string]string{"app/app.go": "package app\n", "app/x_test.go": "package app_test\n\nimport (\n\t\"embed\"\n\t\"testing\"\n)\n\n//go:embed xdir/*.txt\nvar X embed.FS\n\nfunc TestX(t *testing.T) {}\n"}, []string{"app/xdir/a.txt"}, "app/xdir/b.txt", true},
			{"a hidden file under a directory pattern (go:embed leaves it out)", embed("assets"), []string{"app/assets/one.txt"}, "app/assets/.DS_Store", false},
			{"a file beginning with _ under a directory pattern (left out)", embed("assets"), []string{"app/assets/one.txt"}, "app/assets/sub/_draft.txt", false},
			{"a name the wildcard does not match", embed("assets/*.txt"), []string{"app/assets/one.txt"}, "app/assets/two.png", false},
			{"an empty directory under a directory pattern", embed("assets"), []string{"app/assets/one.txt"}, "app/assets/empty/", false},
			{"a directory a dependency's test file embeds (its tests are not built)", map[string]string{"app/app.go": "package app\n\nimport _ \"example.com/fixture/lib\"\n", "lib/lib.go": "package lib\n", "lib/lib_test.go": "package lib\n\nimport (\n\t\"embed\"\n\t\"testing\"\n)\n\n//go:embed ldir\nvar L embed.FS\n\nfunc TestL(t *testing.T) {}\n"}, []string{"lib/ldir/a.txt"}, "lib/ldir/b.txt", false},
		}
		for _, c := range cases {
			root := t.TempDir()
			write(t, root, "go.mod", "module example.com/fixture\n\ngo 1.22\n")
			write(t, root, "app/app_test.go", "package app\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n")
			for f, body := range c.src {
				write(t, root, f, body)
			}
			for _, f := range c.files {
				write(t, root, f, "x\n")
			}
			job := &plugin.Job{Root: root, RunID: "RUN-test", Plugin: "gotest", Targets: []string{"app/app_test.go"}, Config: core.NewOMap()}
			in, err := New().Inputs(job)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if strings.HasSuffix(c.add, "/") {
				if err := os.MkdirAll(filepath.Join(root, c.add), 0o755); err != nil {
					t.Fatal(err)
				}
			} else {
				write(t, root, c.add, "x\n")
			}
			again, err := New().Inputs(job)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if embeds := inputPaths(in) != inputPaths(again); embeds != c.embeds {
				t.Fatalf("%s: go list's files changed: %v, the case expects %v", c.name, embeds, c.embeds)
			}
			if changed := changedListings(root, in); (len(changed) > 0) != c.embeds {
				t.Fatalf("%s: changed listings %v, want a change: %v (listings %+v)", c.name, changed, c.embeds, in.Listings)
			}
		}
	})
}

func TestPackageFailures(t *testing.T) {
	claim := func(id, ref string) plugin.JobClaim {
		return plugin.JobClaim{Spec: "SPEC-0001", Claim: id, SpecRef: core.SubjectRef{ID: "SPEC-0001", Rev: 1, SHA256: "x"}, NativeRef: ref}
	}
	t.Run("[V4] a package whose test binary fails after its tests passed (TestMain's exit code) verifies none of its claims", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "go.mod", "module example.com/fixture\n\ngo 1.22\n")
		write(t, root, "app/app_test.go", "package app\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestClaim(t *testing.T) {}\n\nfunc TestMain(m *testing.M) {\n\tm.Run()\n\tprintln(\"leak check failed\")\n\tos.Exit(1)\n}\n")
		write(t, root, "lib/lib_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestOther(t *testing.T) {}\n")
		job := &plugin.Job{Root: root, RunID: "RUN-test", Plugin: "gotest", Targets: []string{"app/app_test.go", "lib/lib_test.go"}, Config: core.NewOMap(),
			Claims: []plugin.JobClaim{claim("C1", "app::TestClaim"), claim("C2", "lib::TestOther")}}
		p := New()
		in, err := p.Inputs(job)
		if err != nil {
			t.Fatal(err)
		}
		out, err := p.Run(job, plugin.Context{ArtifactDir: t.TempDir(), OnProgress: func(string) {}})
		if err != nil {
			t.Fatal(err)
		}
		rec, err := p.Parse(out, job, in)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"C1": "failed", "C2": "verified"}
		for _, c := range rec.Claims {
			if c.Result != want[c.Claim] {
				t.Fatalf("%s: %s, want %s (%s)", c.Claim, c.Result, want[c.Claim], rec.Summary)
			}
		}
		if rec.Result != "failed" || !strings.Contains(rec.Summary, "outside its tests") || !strings.Contains(rec.Summary, "leak check failed") {
			t.Fatalf("record: %s: %s", rec.Result, rec.Summary)
		}
		if code, _ := rec.Tool.Extra.Get("exit_code"); code != int64(1) {
			t.Fatalf("tool.extra.exit_code: %v", code)
		}
	})

	runAll := func(t *testing.T, root string, targets []string, claims ...plugin.JobClaim) *core.RunRecord {
		t.Helper()
		job := &plugin.Job{Root: root, RunID: "RUN-test", Plugin: "gotest", Targets: targets, Config: core.NewOMap(), Claims: claims}
		p := New()
		in, err := p.Inputs(job)
		if err != nil {
			t.Fatal(err)
		}
		out, err := p.Run(job, plugin.Context{ArtifactDir: t.TempDir(), OnProgress: func(string) {}})
		if err != nil {
			t.Fatal(err)
		}
		rec, err := p.Parse(out, job, in)
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	results := func(rec *core.RunRecord) map[string]string {
		out := map[string]string{}
		for _, c := range rec.Claims {
			out[c.Claim] = c.Result
		}
		return out
	}

	t.Run("[V4] a panic outside the tests is seen even when another test of the package failed", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "go.mod", "module example.com/fixture\n\ngo 1.22\n")
		write(t, root, "tm/tm_test.go", "package tm\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n\nfunc TestB(t *testing.T) { t.Fatal(\"boom\") }\n\nfunc TestMain(m *testing.M) {\n\tm.Run()\n\tpanic(\"after tests\")\n}\n")
		write(t, root, "lib/lib_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestOther(t *testing.T) {}\n")
		rec := runAll(t, root, []string{"tm/tm_test.go", "lib/lib_test.go"},
			claim("C1", "tm::TestA"), claim("C2", "lib::TestOther"))
		want := map[string]string{"C1": "failed", "C2": "verified"}
		if r := results(rec); r["C1"] != want["C1"] || r["C2"] != want["C2"] || !strings.Contains(rec.Summary, "panic: after tests") {
			t.Fatalf("%v, want %v: %s", r, want, rec.Summary)
		}
	})

	t.Run("[V4] a test that panics stops the test binary, so the claims its package's ref matches fail, the tests after it included", func(t *testing.T) {
		// TestC never runs: [X] would be verified by TestA alone
		root := t.TempDir()
		write(t, root, "go.mod", "module example.com/fixture\n\ngo 1.22\n")
		write(t, root, "app/app_test.go", "package app\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { t.Run(\"[X] first\", func(t *testing.T) {}) }\n\nfunc TestB(t *testing.T) {\n\tvar m map[string]int\n\tm[\"x\"] = 1\n}\n\nfunc TestC(t *testing.T) { t.Run(\"[X] second\", func(t *testing.T) { t.Fatal(\"boom\") }) }\n")
		write(t, root, "lib/lib_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestOther(t *testing.T) {}\n")
		rec := runAll(t, root, []string{"app/app_test.go", "lib/lib_test.go"},
			claim("X", "app::[X]"), claim("C", "app::TestC"), claim("O", "lib::TestOther"))
		want := map[string]string{"X": "failed", "C": "failed", "O": "verified"}
		if r := results(rec); r["X"] != want["X"] || r["C"] != want["C"] || r["O"] != want["O"] || !strings.Contains(rec.Summary, "stopped when TestB panicked") || !strings.Contains(rec.Summary, "assignment to entry in nil map") {
			t.Fatalf("%v, want %v: %s", r, want, rec.Summary)
		}
	})

	t.Run("[V4] a package that fails to build fails the claims whose ref's package part matches it, with the compiler's first error", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "go.mod", "module example.com/fixture\n\ngo 1.22\n")
		write(t, root, "bf/bf_test.go", "package bf\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { undefinedThing() }\n")
		write(t, root, "lib/lib_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestOther(t *testing.T) {}\n")
		// go list does not compile, so the inputs are collected; the run fails to build bf
		rec := runAll(t, root, []string{"bf/bf_test.go", "lib/lib_test.go"},
			claim("C1", "bf::TestA"), claim("C2", "lib::TestOther"), claim("C3", "::TestOther"))
		// which tests bf would have had is not known, so a ref without a package part counts it
		want := map[string]string{"C1": "failed", "C2": "verified", "C3": "failed"}
		if r := results(rec); r["C1"] != want["C1"] || r["C2"] != want["C2"] || r["C3"] != want["C3"] || !strings.Contains(rec.Summary, "undefined: undefinedThing") {
			t.Fatalf("%v, want %v: %s", r, want, rec.Summary)
		}
	})

	t.Run("[V4] a goroutine a test left that panics while another test runs fails every claim of the package", func(t *testing.T) {
		// go test puts the panic under the running test, which gets no result
		root := t.TempDir()
		write(t, root, "go.mod", "module example.com/fixture\n\ngo 1.22\n")
		write(t, root, "app/app_test.go", "package app\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestA(t *testing.T) {\n\tgo func() {\n\t\ttime.Sleep(50 * time.Millisecond)\n\t\tpanic(\"leaked goroutine of TestA\")\n\t}()\n}\n\nfunc TestB(t *testing.T) { time.Sleep(2 * time.Second) }\n")
		write(t, root, "lib/lib_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestOther(t *testing.T) {}\n")
		rec := runAll(t, root, []string{"app/app_test.go", "lib/lib_test.go"},
			claim("C1", "app::TestA"), claim("C2", "app::TestB"), claim("C3", "lib::TestOther"))
		want := map[string]string{"C1": "failed", "C2": "failed", "C3": "verified"}
		if r := results(rec); r["C1"] != want["C1"] || r["C2"] != want["C2"] || r["C3"] != want["C3"] || rec.Result != "failed" || !strings.Contains(rec.Summary, "stopped while TestB ran: panic: leaked goroutine of TestA") {
			t.Fatalf("%v, want %v: %s", r, want, rec.Summary)
		}
	})

	t.Run("[V4] a package that ends before running its tests gives its last line as the reason", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "go.mod", "module example.com/fixture\n\ngo 1.22\n")
		write(t, root, "app/app_test.go", "package app\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestA(t *testing.T) {}\n\nfunc TestMain(m *testing.M) {\n\tprintln(\"connecting\")\n\tprintln(\"setup failed: db not reachable\")\n\tos.Exit(1)\n}\n")
		rec := runAll(t, root, []string{"app/app_test.go"}, claim("C1", "app::TestA"))
		if r := results(rec); r["C1"] != "failed" || !strings.Contains(rec.Summary, "setup failed: db not reachable") {
			t.Fatalf("%v: %s", r, rec.Summary)
		}
	})

	t.Run("[V4] a non-zero exit of go test that no failed package or test explains verifies no claim", func(t *testing.T) {
		job := &plugin.Job{Root: t.TempDir(), RunID: "RUN-test", Plugin: "gotest", Targets: []string{"app/app_test.go"}, Config: core.NewOMap(),
			Claims: []plugin.JobClaim{claim("C1", "app::TestClaim")}}
		out := plugin.RawOutput{Data: &raw{version: "go version test", code: 2, events: []event{
			{Action: "run", Package: "example.com/fixture/app", Test: "TestClaim"},
			{Action: "pass", Package: "example.com/fixture/app", Test: "TestClaim"},
			{Action: "pass", Package: "example.com/fixture/app"},
		}}}
		rec, err := New().Parse(out, job, plugin.Inputs{})
		if err != nil {
			t.Fatal(err)
		}
		if rec.Claims[0].Result != "failed" || rec.Result != "failed" || !strings.Contains(rec.Summary, "exited with status 2") {
			t.Fatalf("%s %s: %s", rec.Claims[0].Result, rec.Result, rec.Summary)
		}
	})
}
