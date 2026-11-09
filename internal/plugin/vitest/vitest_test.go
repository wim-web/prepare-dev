package vitest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// The report shapes below are what Vitest 5.1.0 writes (checked by running it): an
// afterAll that throws fails its file and gives it a message while every test passed;
// a file that cannot be loaded is failed with a message and no tests; an unhandled
// error exits with 1, leaves the report saying success, and reaches only reporters
// (the plugin's reporter writes it with the test file it came from).
func TestFailuresOutsideTests(t *testing.T) {
	claim := func(id, ref string) plugin.JobClaim {
		return plugin.JobClaim{Spec: "SPEC-0004", Claim: id, SpecRef: core.SubjectRef{ID: "SPEC-0004", Rev: 1, SHA256: "x"}, NativeRef: ref}
	}
	// parseRaw parses a run whose report is reportJSON; rw gives the rest of what the
	// plugin's reporter wrote
	parseRaw := func(t *testing.T, rw *raw, reportJSON string, claims ...plugin.JobClaim) *core.RunRecord {
		t.Helper()
		var rep report
		if err := json.Unmarshal([]byte(reportJSON), &rep); err != nil {
			t.Fatal(err)
		}
		job := &plugin.Job{Root: "/p", RunID: "RUN-test", Plugin: "vitest", Targets: []string{"a.test.ts", "b.test.ts"}, Config: core.NewOMap(), Claims: claims}
		rw.version, rw.report, rw.unhandledKnown = "test", &rep, true
		rec, err := New().Parse(plugin.RawOutput{Data: rw}, job, plugin.Inputs{})
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	parse := func(t *testing.T, code int, reportJSON string, unhandled []unhandledError, claims ...plugin.JobClaim) *core.RunRecord {
		t.Helper()
		return parseRaw(t, &raw{code: code, unhandled: unhandled}, reportJSON, claims...)
	}
	results := func(rec *core.RunRecord) map[string]string {
		out := map[string]string{}
		for _, c := range rec.Claims {
			out[c.Claim] = c.Result
		}
		return out
	}
	t.Run("[V4] a file that fails outside its tests (afterAll) verifies none of its claims, also when one of its tests failed", func(t *testing.T) {
		rec := parse(t, 1, `{"numTotalTests":4,"numPassedTests":3,"numFailedTests":1,"success":false,"testResults":[
			{"name":"/p/a.test.ts","status":"passed","message":"","assertionResults":[{"fullName":"[C1] passes","status":"passed","failureMessages":[]}]},
			{"name":"/p/b.test.ts","status":"failed","message":"teardown broke\n    at b.test.ts:3","assertionResults":[{"fullName":"[C2] ok","status":"passed","failureMessages":[]}]},
			{"name":"/p/d.test.ts","status":"failed","message":"teardown broke too","assertionResults":[{"fullName":"[C4] ok","status":"passed","failureMessages":[]},{"fullName":"[C4x] fails","status":"failed","failureMessages":["expected 1 to be 2"]}]}]}`, nil,
			claim("C1", "a.test.ts::[C1]"), claim("C2", "b.test.ts::[C2]"), claim("C4", "d.test.ts::[C4]"))
		if r := results(rec); r["C1"] != "verified" || r["C2"] != "failed" || r["C4"] != "failed" || rec.Result != "failed" || !strings.Contains(rec.Summary, "teardown broke") || !strings.Contains(rec.Summary, "teardown broke too") {
			t.Fatalf("%v %s: %s", r, rec.Result, rec.Summary)
		}
	})
	t.Run("[V4] a file that cannot be loaded fails the claims whose ref's file part matches it", func(t *testing.T) {
		rec := parse(t, 1, `{"numTotalTests":1,"numPassedTests":1,"numFailedTests":0,"success":false,"testResults":[
			{"name":"/p/c.test.ts","status":"failed","message":"Cannot find module './missing-module' imported from /p/c.test.ts","assertionResults":[]},
			{"name":"/p/e.test.ts","status":"passed","message":"","assertionResults":[{"fullName":"[C5] clean","status":"passed","failureMessages":[]}]}]}`, nil,
			claim("C3", "c.test.ts::[C3]"), claim("C5", "e.test.ts::[C5]"), claim("C6", "::[C6]"))
		// which tests c.test.ts had is not known, so a ref without a file part counts it
		if r := results(rec); r["C3"] != "failed" || r["C5"] != "verified" || r["C6"] != "failed" || !strings.Contains(rec.Summary, "Cannot find module") {
			t.Fatalf("%v %s: %s", r, rec.Result, rec.Summary)
		}
	})
	t.Run("[V4] an unhandled error fails the claims of its test file, also when another file failed; one without a file leaves no claim verified", func(t *testing.T) {
		report := `{"numTotalTests":3,"numPassedTests":2,"numFailedTests":1,"success":false,"testResults":[
			{"name":"/p/a.test.ts","status":"passed","message":"","assertionResults":[{"fullName":"[C1] passes","status":"passed","failureMessages":[]}]},
			{"name":"/p/b.test.ts","status":"failed","message":"","assertionResults":[{"fullName":"[C2] fails","status":"failed","failureMessages":["expected 1 to be 2"]}]},
			{"name":"/p/e.test.ts","status":"passed","message":"","assertionResults":[{"fullName":"[C5] clean","status":"passed","failureMessages":[]}]}]}`
		rec := parse(t, 1, report, []unhandledError{{File: "/p/a.test.ts", Test: "[C1] passes", Name: "Error", Message: "late failure"}},
			claim("C1", "a.test.ts::[C1]"), claim("C2", "b.test.ts::[C2]"), claim("C5", "e.test.ts::[C5]"))
		if r := results(rec); r["C1"] != "failed" || r["C2"] != "failed" || r["C5"] != "verified" || !strings.Contains(rec.Summary, "unhandled error: late failure") {
			t.Fatalf("%v %s: %s", r, rec.Result, rec.Summary)
		}
		rec = parse(t, 1, report, []unhandledError{{Message: "lost"}}, claim("C5", "e.test.ts::[C5]"))
		if r := results(rec); r["C5"] != "failed" || rec.Result != "failed" || !strings.Contains(rec.Summary, "where it happened is not known") {
			t.Fatalf("%v %s: %s", r, rec.Result, rec.Summary)
		}
		// without isolation an error reported in one file may come from another file's test
		rec = parseRaw(t, &raw{code: 1, unhandled: []unhandledError{{File: "/p/a.test.ts", Message: "late failure"}}, isolateOff: true}, report,
			claim("C1", "a.test.ts::[C1]"), claim("C5", "e.test.ts::[C5]"))
		if r := results(rec); r["C1"] != "failed" || r["C5"] != "failed" || !strings.Contains(rec.Summary, "not isolated") {
			t.Fatalf("%v %s: %s", r, rec.Result, rec.Summary)
		}
	})
	t.Run("[V4] an afterAll that throws inside a describe is seen from the reporter, also when another test of the file failed", func(t *testing.T) {
		// the JSON report leaves it out of the file's message
		report := `{"numTotalTests":3,"numPassedTests":2,"numFailedTests":1,"success":false,"testResults":[
			{"name":"/p/d.test.ts","status":"failed","message":"","assertionResults":[{"fullName":"inner [N1] ok","status":"passed","failureMessages":[]},{"fullName":"[N2] fails","status":"failed","failureMessages":["expected 1 to be 2"]}]},
			{"name":"/p/e.test.ts","status":"passed","message":"","assertionResults":[{"fullName":"[C5] clean","status":"passed","failureMessages":[]}]}]}`
		rec := parseRaw(t, &raw{code: 1, fileErrors: map[string][]string{"/p/d.test.ts": {"nested teardown broke"}}, hook: "onTestRunEnd"}, report,
			claim("N1", "d.test.ts::[N1]"), claim("C5", "e.test.ts::[C5]"))
		if r := results(rec); r["N1"] != "failed" || r["C5"] != "verified" || !strings.Contains(rec.Summary, "nested teardown broke") || strings.Contains(rec.Summary, "onFinished") {
			t.Fatalf("%v %s: %s", r, rec.Result, rec.Summary)
		}
		// an earlier Vitest calls onFinished, whose task tree was not checked with a real Vitest
		rec = parseRaw(t, &raw{code: 1, fileErrors: map[string][]string{"/p/d.test.ts": {"nested teardown broke"}}, hook: "onFinished"}, report,
			claim("N1", "d.test.ts::[N1]"))
		if r := results(rec); r["N1"] != "failed" || !strings.Contains(rec.Summary, "read through onFinished") {
			t.Fatalf("%v %s: %s", r, rec.Result, rec.Summary)
		}
	})
	t.Run("[V4] a non-zero exit that no failed test, file or unhandled error explains verifies no claim", func(t *testing.T) {
		rec := parse(t, 1, `{"numTotalTests":1,"numPassedTests":1,"numFailedTests":0,"success":true,"testResults":[
			{"name":"/p/a.test.ts","status":"passed","message":"","assertionResults":[{"fullName":"[C1] passes","status":"passed","failureMessages":[]}]}]}`, nil,
			claim("C1", "a.test.ts::[C1]"))
		if r := results(rec); r["C1"] != "failed" || rec.Result != "failed" || !strings.Contains(rec.Summary, "exited with status 1") {
			t.Fatalf("%v %s: %s", r, rec.Result, rec.Summary)
		}
		if code, _ := rec.Tool.Extra.Get("exit_code"); code != int64(1) {
			t.Fatalf("tool.extra.exit_code: %v", code)
		}
	})
	t.Run("[V4] the reporter module writes unhandled errors to the given path", func(t *testing.T) {
		src := string(unhandledReporter("/p/out \"x\".json"))
		for _, want := range []string{`const out = "/p/out \"x\".json";`, "VITEST_TEST_PATH", "onTestRunEnd(testModules, unhandledErrors)", "onFinished(files, errors)", "allSuites()", "isolate !== false"} {
			if !strings.Contains(src, want) {
				t.Fatalf("missing %q in:\n%s", want, src)
			}
		}
	})
}
