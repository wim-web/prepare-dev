package vitest

import (
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

func paths(files []core.InputFile) map[string]string {
	out := map[string]string{}
	for _, f := range files {
		out[f.Path] = f.SHA256
	}
	return out
}

func TestInputs(t *testing.T) {
	t.Run("[V1] includes dependency metadata and transitive imports of extra implementation and config files", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "package.json", `{"name":"fixture","type":"module"}`)
		write(t, root, "pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
		write(t, root, "pnpm-workspace.yaml", "packages: [packages/*]\n")
		write(t, root, "packages/lib/package.json", `{"name":"@fixture/lib","main":"./src/index.ts"}`)
		write(t, root, "packages/lib/src/index.ts", "export const lib = 1;")
		// pnpm links workspace packages into node_modules
		if err := os.MkdirAll(filepath.Join(root, "node_modules", "@fixture"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join("..", "..", "packages", "lib"), filepath.Join(root, "node_modules", "@fixture", "lib")); err != nil {
			t.Fatal(err)
		}
		write(t, root, "entry.test.ts", "export const test = 1;")
		write(t, root, "extra.ts", "import {\n deep\n} from \"./deep.js\"; import {\n lib\n} from \"@fixture/lib\"; export { deep, lib };")
		write(t, root, "deep.ts", "export { leaf } from \"./leaf.js\"; export const deep = 1;")
		write(t, root, "leaf.ts", "export const leaf = 1;")
		write(t, root, "vitest.config.ts", "import {\n config\n} from \"./config.js\"; export default config;")
		write(t, root, "config.ts", "export const config = {};")
		job := &plugin.Job{Root: root, RunID: "RUN-test", Plugin: "vitest", Targets: []string{"entry.test.ts"}, Config: core.OM("impl_files", []any{"extra.ts"})}
		p := New()
		before, err := p.Inputs(job)
		if err != nil {
			t.Fatal(err)
		}
		got := paths(before.Files)
		if len(got) != len(before.Files) {
			t.Fatal("duplicate inputs")
		}
		for _, want := range []string{"package.json", "pnpm-lock.yaml", "pnpm-workspace.yaml", "packages/lib/package.json", "packages/lib/src/index.ts", "extra.ts", "deep.ts", "leaf.ts", "vitest.config.ts", "config.ts"} {
			if _, ok := got[want]; !ok {
				t.Fatalf("missing input %s in %v", want, got)
			}
		}
		for _, file := range []string{"package.json", "pnpm-lock.yaml", "packages/lib/package.json", "leaf.ts", "config.ts"} {
			initial, _ := os.ReadFile(filepath.Join(root, file))
			write(t, root, file, string(initial)+"\n")
			after, err := p.Inputs(job)
			if err != nil {
				t.Fatal(err)
			}
			if paths(after.Files)[file] == got[file] {
				t.Fatalf("%s: change not reflected in its sha256", file)
			}
			write(t, root, file, string(initial))
		}
	})

	t.Run("[V1] resolves like the bundler: tsconfig paths and assets are inputs, installed packages and type-only imports are not", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "package.json", `{"name":"fixture","type":"module"}`)
		write(t, root, "tsconfig.json", `{"compilerOptions":{"baseUrl":".","paths":{"@app/*":["src/*"]}}}`)
		write(t, root, "node_modules/dep/package.json", `{"name":"dep","main":"index.js"}`)
		write(t, root, "node_modules/dep/index.js", "export const dep = 1;")
		write(t, root, "src/util.ts", "export const util = 1;")
		write(t, root, "src/types.ts", "export type Shape = { n: number };")
		write(t, root, "src/logo.svg", "<svg/>")
		write(t, root, "src/notes.txt", "notes")
		write(t, root, "app.test.ts", "import { util } from \"@app/util\";\nimport type { Shape } from \"./src/types\";\nimport { dep } from \"dep\";\nimport logo from \"./src/logo.svg\";\nimport notes from \"./src/notes.txt?raw\";\nimport { missing } from \"not-installed\";\nconst s: Shape = { n: util + dep };\nexport { s, logo, notes, missing };\n")
		in, err := New().Inputs(&plugin.Job{Root: root, RunID: "RUN-test", Plugin: "vitest", Targets: []string{"app.test.ts"}, Config: core.NewOMap()})
		if err != nil {
			t.Fatal(err)
		}
		got := paths(in.Files)
		for _, want := range []string{"src/util.ts", "src/logo.svg", "src/notes.txt", "tsconfig.json", "package.json"} {
			if _, ok := got[want]; !ok {
				t.Fatalf("missing input %s in %v", want, got)
			}
		}
		for _, absent := range []string{"src/types.ts", "node_modules/dep/index.js", "node_modules/dep/package.json"} {
			if _, ok := got[absent]; ok {
				t.Fatalf("%s should not be an input: %v", absent, got)
			}
		}
	})

	t.Run("[V1] resolves extensionless imports in Vite's order and records the directories of imports", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "package.json", `{"name":"fixture","type":"module"}`)
		write(t, root, "src/api.ts", "export const api = 'ts';")
		write(t, root, "src/api.js", "export const api = 'js';")
		write(t, root, "app.test.ts", "import { api } from \"./src/api\";\nexport { api };\n")
		job := &plugin.Job{Root: root, RunID: "RUN-test", Plugin: "vitest", Targets: []string{"app.test.ts"}, Config: core.NewOMap()}
		in, err := New().Inputs(job)
		if err != nil {
			t.Fatal(err)
		}
		got := paths(in.Files)
		if _, ok := got["src/api.js"]; !ok {
			t.Fatalf("Vite resolves ./src/api to api.js first: %v", got)
		}
		found := false
		for _, l := range in.Listings {
			if l.Path == "src" && !l.Recursive {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing listing of src: %v", in.Listings)
		}
	})

	t.Run("detects vitest installed only under the project_dir", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "ui/package.json", `{"name":"ui"}`)
		bin := filepath.Join(root, "ui", "node_modules", ".bin", "vitest")
		write(t, root, "ui/node_modules/.bin/vitest", "#!/bin/sh\necho 9.9.9\n")
		if err := os.Chmod(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		job := &plugin.Job{Root: root, RunID: "RUN-test", Plugin: "vitest", Config: core.OM("project_dir", "ui")}
		if d := New().Detect(job); !d.Available || d.Versions["vitest"] != "9.9.9" {
			t.Fatalf("detect with project_dir: %+v", d)
		}
		if d := New().Detect(&plugin.Job{Root: root, RunID: "RUN-test", Plugin: "vitest", Config: core.NewOMap()}); d.Available {
			t.Fatalf("detect without project_dir should not find it: %+v", d)
		}
	})

	t.Run("[V1] includes ancestor lockfiles for a nested project", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "package.json", `{"name":"root"}`)
		write(t, root, "yarn.lock", "# lock\n")
		write(t, root, "apps/site/package.json", `{"name":"site"}`)
		write(t, root, "apps/site/test.ts", "export const value = 1;")
		in, err := New().Inputs(&plugin.Job{Root: root, RunID: "RUN-test", Plugin: "vitest", Targets: []string{"apps/site/test.ts"}, Config: core.OM("project_dir", "apps/site")})
		if err != nil {
			t.Fatal(err)
		}
		got := paths(in.Files)
		for _, want := range []string{"package.json", "yarn.lock", "apps/site/package.json"} {
			if _, ok := got[want]; !ok {
				t.Fatalf("missing input %s in %v", want, got)
			}
		}
	})
}
