package gotest

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/wim-web/prepare-dev/internal/core"
	"github.com/wim-web/prepare-dev/internal/plugin"
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
		digest := map[string]string{}
		for _, l := range in.Listings {
			digest[l.Path] = l.SHA256
		}
		for dir, file := range map[string]string{"app": "app/extra.go", "app/testdata": "app/testdata/sub/y.txt", "traces": "traces/b.jsonl"} {
			write(t, root, file, "package app\n")
			l, err := plugin.Listing(root, filepath.Join(root, dir), dir == "app/testdata")
			if err != nil {
				t.Fatal(err)
			}
			if l.SHA256 == digest[dir] {
				t.Fatalf("adding %s did not change the listing of %s", file, dir)
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
