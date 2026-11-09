// Package vitest runs Vitest in a TypeScript/JavaScript project and records the
// transitive imports, manifests and lockfiles the tests depend on.
package vitest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/evanw/esbuild/pkg/api"

	preparedev "github.com/wim-web/prepare-dev"
	"github.com/wim-web/prepare-dev/internal/core"
	"github.com/wim-web/prepare-dev/internal/plugin"
)

type config struct {
	projectDir string
	implFiles  []string
	env        *core.OMap
	boundsFrom string
	timeout    time.Duration
}

func cfg(job *plugin.Job) config {
	c := config{
		projectDir: plugin.ConfigString(job.Config, "project_dir", "."),
		implFiles:  plugin.ConfigStrings(job.Config, "impl_files"),
		boundsFrom: plugin.ConfigString(job.Config, "bounds_from", ""),
		timeout:    time.Duration(plugin.ConfigInt(job.Config, "timeout_ms", 600000)) * time.Millisecond,
		env:        core.NewOMap(),
	}
	if e, ok := job.Config.Map("env"); ok {
		c.env = e
	}
	return c
}

func (c config) envMap() map[string]string {
	out := map[string]string{"CI": "1"}
	for _, k := range c.env.Keys() {
		if s, ok := c.env.Str(k); ok {
			out[k] = s
		}
	}
	return out
}

var reJSFile = regexp.MustCompile(`\.(ts|tsx|mts|cts|js|jsx|mjs|cjs)$`)

// reBundled are the files esbuild loads into the bundle; anything else that is imported
// (assets, .vue, ...) is recorded as an input without following its imports.
var reBundled = regexp.MustCompile(`\.(ts|tsx|mts|cts|js|jsx|mjs|cjs|json|css)$`)

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func realOr(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

const resolveMarker = "prepare-dev-resolve"

// runtimeImports bundles the entries with esbuild, which resolves modules as Vite does
// for plain imports (relative paths, package.json main/exports, tsconfig paths,
// workspace packages linked into node_modules), and returns every file the bundle
// loads plus the imported files it does not load (assets), as real absolute paths.
// Packages that resolve into node_modules are left out: the manifests and lockfiles
// stand for them. Imports esbuild drops are not followed: type-only imports, which do
// not run, and specifiers it cannot resolve, which the test run itself reports.
func runtimeImports(realRoot string, entries []string) (loaded, assets []string, err error) {
	if len(entries) == 0 {
		return nil, nil, nil
	}
	var mu sync.Mutex
	assetSet := map[string]bool{}
	sep := string(filepath.Separator)
	resolver := api.Plugin{
		Name: "prepare-dev-inputs",
		Setup: func(b api.PluginBuild) {
			b.OnResolve(api.OnResolveOptions{Filter: ".*"}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
				if args.Kind == api.ResolveEntryPoint || args.PluginData == resolveMarker {
					return api.OnResolveResult{}, nil
				}
				res := b.Resolve(args.Path, api.ResolveOptions{Kind: args.Kind, ResolveDir: args.ResolveDir, Importer: args.Importer, Namespace: args.Namespace, PluginData: resolveMarker})
				if len(res.Errors) > 0 || res.Path == "" || res.External || res.Namespace != "file" {
					return api.OnResolveResult{Path: args.Path, External: true}, nil
				}
				if strings.Contains(res.Path, sep+"node_modules"+sep) {
					return api.OnResolveResult{Path: res.Path, External: true}, nil
				}
				if !reBundled.MatchString(res.Path) {
					mu.Lock()
					assetSet[res.Path] = true
					mu.Unlock()
					return api.OnResolveResult{Path: res.Path, External: true}, nil
				}
				return api.OnResolveResult{Path: res.Path, Namespace: "file", Suffix: res.Suffix}, nil
			})
		},
	}
	res := api.Build(api.BuildOptions{
		EntryPoints:   entries,
		Bundle:        true,
		Write:         false,
		Metafile:      true,
		AbsWorkingDir: realRoot,
		Outdir:        filepath.Join(os.TempDir(), "prepare-dev-vitest-scan"),
		Platform:      api.PlatformNode,
		Format:        api.FormatESModule,
		LogLevel:      api.LogLevelSilent,
		Plugins:       []api.Plugin{resolver},
		Loader:        map[string]api.Loader{".mts": api.LoaderTS, ".cts": api.LoaderTS, ".mjs": api.LoaderJS, ".cjs": api.LoaderJS},
		// Vite's default resolve.extensions, in its order: "./api" finds api.js before api.ts
		ResolveExtensions: []string{".mjs", ".js", ".mts", ".ts", ".jsx", ".tsx", ".json"},
	})
	if len(res.Errors) > 0 {
		e := res.Errors[0]
		where := ""
		if e.Location != nil {
			where = fmt.Sprintf("%s:%d: ", e.Location.File, e.Location.Line)
		}
		return nil, nil, fmt.Errorf("cannot follow imports: %s%s", where, e.Text)
	}
	var meta struct {
		Inputs map[string]json.RawMessage `json:"inputs"`
	}
	if err := json.Unmarshal([]byte(res.Metafile), &meta); err != nil {
		return nil, nil, fmt.Errorf("esbuild metafile: %w", err)
	}
	for key := range meta.Inputs {
		if strings.HasPrefix(key, "<") || (strings.Contains(key, ":") && !strings.HasPrefix(key, ".") && !strings.HasPrefix(key, "/")) {
			continue // another namespace (data:, <runtime>)
		}
		abs := key
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(realRoot, filepath.FromSlash(key))
		}
		loaded = append(loaded, abs)
	}
	for a := range assetSet {
		assets = append(assets, a)
	}
	sort.Strings(loaded)
	sort.Strings(assets)
	return loaded, assets, nil
}

func vitestBin(dir string) string {
	p := filepath.Join(dir, "node_modules", ".bin", "vitest")
	if isFile(p) {
		return p
	}
	return ""
}

// Plugin is the vitest plugin.
type Plugin struct{}

// New returns the plugin.
func New() *Plugin { return &Plugin{} }

func (p *Plugin) Name() string    { return "vitest" }
func (p *Plugin) Version() string { return "0.3.0" }

func (p *Plugin) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{Languages: []string{"typescript", "javascript"}, Methods: []string{"test"}, Properties: []string{"example", "property", "conformance"}, Tools: []string{"vitest"}}
}

func (p *Plugin) Guidance() string {
	data, _ := preparedev.Guidance("vitest")
	return string(data)
}

func (p *Plugin) Detect(job *plugin.Job) plugin.DetectResult {
	projectDir := filepath.Join(job.Root, cfg(job).projectDir)
	bin := vitestBin(projectDir)
	if bin == "" {
		bin = vitestBin(job.Root)
	}
	if bin == "" {
		return plugin.DetectResult{Reason: "node_modules/.bin/vitest not found in the project_dir or the root"}
	}
	r, err := plugin.Exec(bin, []string{"--version"}, plugin.ExecOptions{Dir: projectDir, Timeout: time.Minute})
	if err != nil {
		return plugin.DetectResult{Reason: err.Error()}
	}
	if r.Code != 0 {
		return plugin.DetectResult{Reason: r.Stderr}
	}
	return plugin.DetectResult{Available: true, Versions: map[string]string{"vitest": strings.TrimSpace(r.Stdout)}}
}

var metadataNames = []string{"package.json", "pnpm-lock.yaml", "package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "bun.lock", "bun.lockb", "pnpm-workspace.yaml", ".npmrc", "tsconfig.json"}

// Inputs follows the run-time imports of the tests, implementation files and Vitest
// config, and adds the manifests, lockfiles and workspace config up to the project root.
func (p *Plugin) Inputs(job *plugin.Job) (plugin.Inputs, error) {
	c := cfg(job)
	projectDir := filepath.Join(job.Root, c.projectDir)
	set := plugin.NewInputSet(job.Root)
	var out plugin.Inputs
	out.Targets = []core.InputFile{}
	rootAbs := filepath.Clean(job.Root)
	realRoot := realOr(rootAbs)
	// esbuild reports real paths; map them back under the root as given
	toRoot := func(abs string) string {
		if realRoot != rootAbs && strings.HasPrefix(abs, realRoot+string(filepath.Separator)) {
			return filepath.Join(rootAbs, abs[len(realRoot)+1:])
		}
		return abs
	}
	targets := map[string]bool{}
	var entries []string
	for _, t := range job.Targets {
		abs := filepath.Join(job.Root, t)
		sum, err := core.SHA256File(abs)
		if err != nil {
			return plugin.Inputs{}, err
		}
		out.Targets = append(out.Targets, core.InputFile{Path: t, SHA256: sum, Role: "target"})
		targets[filepath.Clean(abs)] = true
		if reJSFile.MatchString(abs) {
			entries = append(entries, realOr(abs))
		}
	}
	var dirs []string
	add := func(abs, role string) error {
		abs = filepath.Clean(abs)
		if targets[abs] {
			return nil
		}
		dirs = append(dirs, filepath.Dir(abs))
		return set.Add(abs, role)
	}
	implFiles, implListings, err := plugin.ExpandPatterns(job.Root, c.implFiles)
	if err != nil {
		return plugin.Inputs{}, err
	}
	for _, abs := range implFiles {
		if err := add(abs, "implementation"); err != nil {
			return plugin.Inputs{}, err
		}
		if reJSFile.MatchString(abs) {
			entries = append(entries, realOr(abs))
		}
	}
	out.Listings = append(out.Listings, implListings...)
	for _, name := range []string{"vitest.config.ts", "vitest.config.js", "vitest.config.mts", "vitest.config.mjs", "vitest.config.cts", "vitest.config.cjs"} {
		if abs := filepath.Join(projectDir, name); isFile(abs) {
			if err := add(abs, "config"); err != nil {
				return plugin.Inputs{}, err
			}
			entries = append(entries, realOr(abs))
		}
	}
	loaded, assets, err := runtimeImports(realRoot, entries)
	if err != nil {
		return plugin.Inputs{}, err
	}
	listed := map[string]bool{}
	for _, f := range append(loaded, assets...) {
		abs := toRoot(f)
		if err := add(abs, "import"); err != nil {
			return plugin.Inputs{}, err
		}
		// a file added next to an import (api.js beside api.ts) can change what the
		// import resolves to: record the directory's listing
		dir := filepath.Dir(abs)
		if !listed[dir] && (dir == rootAbs || strings.HasPrefix(dir, rootAbs+string(filepath.Separator))) {
			listed[dir] = true
			l, err := plugin.Listing(job.Root, dir, false)
			if err != nil {
				return plugin.Inputs{}, err
			}
			out.Listings = append(out.Listings, l)
		}
	}
	metaDone := map[string]bool{}
	addMetadata := func(from string) error {
		for dir := filepath.Clean(from); ; dir = filepath.Dir(dir) {
			rel, err := filepath.Rel(rootAbs, dir)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return nil
			}
			if metaDone[dir] {
				return nil
			}
			metaDone[dir] = true
			for _, name := range metadataNames {
				if abs := filepath.Join(dir, name); isFile(abs) {
					if err := add(abs, "config"); err != nil {
						return err
					}
				}
			}
			if rel == "." {
				return nil
			}
		}
	}
	var targetDirs []string
	for t := range targets {
		targetDirs = append(targetDirs, filepath.Dir(t))
	}
	sort.Strings(targetDirs)
	dirs = append(append([]string{projectDir}, targetDirs...), dirs...)
	for i := 0; i < len(dirs); i++ { // add appends metadata directories while we walk
		if err := addMetadata(dirs[i]); err != nil {
			return plugin.Inputs{}, err
		}
	}
	out.Files = set.Files()
	return out, nil
}

type report struct {
	NumTotalTests  int `json:"numTotalTests"`
	NumPassedTests int `json:"numPassedTests"`
	TestResults    []struct {
		Name string `json:"name"`
		// Status is "failed" also when the file failed outside its tests (an afterAll
		// that throws, an import that fails); Message then says why.
		Status           string `json:"status"`
		Message          string `json:"message"`
		AssertionResults []struct {
			FullName        string   `json:"fullName"`
			Status          string   `json:"status"`
			FailureMessages []string `json:"failureMessages"`
		} `json:"assertionResults"`
	} `json:"testResults"`
}

type raw struct {
	version  string
	bin      string
	code     int
	timedOut bool
	report   *report
	// unhandled are the run's unhandled errors as the plugin's reporter wrote them;
	// unhandledKnown is false when it wrote nothing. fileErrors are the errors of each
	// test file and its suites (an afterAll inside a describe), which the JSON report
	// leaves out of the file's message; isolateOff is set when a project runs its test
	// files without isolation, so that an error can come from another file's test.
	unhandled      []unhandledError
	unhandledKnown bool
	fileErrors     map[string][]string
	isolateOff     bool
	// hook is the reporter hook that wrote: onTestRunEnd (Vitest 3 and later) or
	// onFinished (earlier versions, whose task tree was not checked with a real Vitest)
	hook string
}

// reporterOutput is what the plugin's reporter writes.
type reporterOutput struct {
	Errors []unhandledError `json:"errors"`
	Files  []struct {
		File   string   `json:"file"`
		Errors []string `json:"errors"`
	} `json:"files"`
	Isolate *bool  `json:"isolate"`
	Hook    string `json:"hook"`
}

// unhandledError is an error Vitest caught outside the tests' own results (an exception
// thrown after a test, a rejected promise), with the test file and test it came from
// when Vitest knows them.
type unhandledError struct {
	File    string `json:"file"`
	Test    string `json:"test"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

// unhandledReporter is a Vitest reporter module that writes to out what the JSON report
// leaves out: the run's unhandled errors, with the test file and test they came from
// (VITEST_TEST_PATH, VITEST_TEST_NAME); the errors of each test file and its suites (an
// afterAll that throws inside a describe); and whether every project isolates its test
// files. Vitest 3 and later call onInit and onTestRunEnd; earlier versions call
// onFinished, which has no suites.
func unhandledReporter(out string) []byte {
	path, _ := json.Marshal(out)
	return []byte(`import { writeFileSync } from "node:fs";
const out = ` + string(path) + `;
let written = false;
let isolate = null;
const text = (e) => String((e && e.message) || e || "");
function write(errors, modules, hook) {
  if (written) return;
  written = true;
  const list = Array.from(errors || [], (e) => ({
    file: (e && e.VITEST_TEST_PATH) || "",
    test: (e && e.VITEST_TEST_NAME) || "",
    name: (e && e.name) || "",
    message: text(e),
  }));
  const files = [];
  for (const m of modules || []) {
    const found = [];
    try {
      for (const e of (m.errors && m.errors()) || []) found.push(text(e));
      if (m.children && m.children.allSuites) {
        for (const suite of m.children.allSuites()) {
          for (const e of (suite.errors && suite.errors()) || []) found.push(text(e));
        }
      }
    } catch (e) {
      found.push("the reporter could not read the errors: " + text(e));
    }
    if (found.length) files.push({ file: m.moduleId || "", errors: found });
  }
  writeFileSync(out, JSON.stringify({ errors: list, files, isolate, hook }) + "\n");
}
export default class PrepareDevUnhandledErrors {
  onInit(vitest) {
    try {
      const projects = vitest.projects && vitest.projects.length ? vitest.projects : [vitest];
      isolate = projects.every((p) => !p.config || p.config.isolate !== false);
    } catch {}
  }
  onTestRunEnd(testModules, unhandledErrors) { write(unhandledErrors, testModules, "onTestRunEnd"); }
  onFinished(files, errors) {
    // before Vitest 3: files are tasks, and a file's or suite's errors are in result.errors
    const errorsOf = (task) => () => (task.result && task.result.errors) || [];
    const suites = function* (task) {
      for (const t of task.tasks || []) {
        if (t.type === "suite") {
          yield { errors: errorsOf(t) };
          yield* suites(t);
        }
      }
    };
    const modules = Array.from(files || [], (f) => ({
      moduleId: f.filepath || "",
      errors: errorsOf(f),
      children: { allSuites: () => suites(f) },
    }));
    write(errors, modules, "onFinished");
  }
}
`)
}

func (p *Plugin) Run(job *plugin.Job, ctx plugin.Context) (plugin.RawOutput, error) {
	c := cfg(job)
	projectDir := filepath.Join(job.Root, c.projectDir)
	bin := vitestBin(projectDir)
	if bin == "" {
		bin = vitestBin(job.Root)
	}
	if bin == "" {
		return plugin.RawOutput{}, errors.New("vitest binary not found")
	}
	out := filepath.Join(ctx.ArtifactDir, "vitest.json")
	unhandledPath := filepath.Join(ctx.ArtifactDir, "vitest-unhandled.json")
	ver, err := plugin.Exec(bin, []string{"--version"}, plugin.ExecOptions{Dir: projectDir, Timeout: time.Minute})
	if err != nil {
		return plugin.RawOutput{}, err
	}
	reporterDir, err := os.MkdirTemp("", "prepare-dev-vitest-")
	if err != nil {
		return plugin.RawOutput{}, err
	}
	defer os.RemoveAll(reporterDir)
	reporter := filepath.Join(reporterDir, "unhandled-errors.mjs")
	if err := os.WriteFile(reporter, unhandledReporter(unhandledPath), 0o644); err != nil {
		return plugin.RawOutput{}, err
	}
	ctx.OnProgress("vitest run " + strings.Join(job.Targets, " "))
	args := []string{"run", "--reporter=json", "--outputFile=" + out, "--reporter=" + reporter}
	for _, t := range job.Targets {
		rel, _ := filepath.Rel(projectDir, filepath.Join(job.Root, t))
		args = append(args, rel)
	}
	r, err := plugin.Exec(bin, args, plugin.ExecOptions{Dir: projectDir, Ctx: ctx.Ctx, Timeout: c.timeout, Env: c.envMap()})
	if err != nil {
		return plugin.RawOutput{}, err
	}
	logPath := filepath.Join(ctx.ArtifactDir, "vitest.stderr.log")
	if err := os.WriteFile(logPath, []byte(r.Stdout+"\n"+r.Stderr), 0o644); err != nil {
		return plugin.RawOutput{}, err
	}
	rw := &raw{version: strings.TrimSpace(ver.Stdout), bin: bin, code: r.Code, timedOut: r.TimedOut}
	if data, err := os.ReadFile(out); err == nil {
		var rep report
		if json.Unmarshal(data, &rep) == nil {
			rw.report = &rep
		}
	}
	artifacts := []core.Artifact{{Kind: "report", Path: out}, {Kind: "log", Path: logPath}}
	if data, err := os.ReadFile(unhandledPath); err == nil {
		var u reporterOutput
		if json.Unmarshal(data, &u) == nil {
			rw.unhandled, rw.unhandledKnown = u.Errors, true
			rw.isolateOff = u.Isolate != nil && !*u.Isolate
			rw.hook = u.Hook
			for _, f := range u.Files {
				if len(f.Errors) > 0 {
					if rw.fileErrors == nil {
						rw.fileErrors = map[string][]string{}
					}
					rw.fileErrors[f.File] = f.Errors
				}
			}
			artifacts = append(artifacts, core.Artifact{Kind: "report", Path: unhandledPath})
		}
	}
	return plugin.RawOutput{Data: rw, Artifacts: artifacts}, nil
}

type assertion struct {
	file, fullName, status string
	failures               []string
}

func (p *Plugin) Parse(out plugin.RawOutput, job *plugin.Job, inputs plugin.Inputs) (*core.RunRecord, error) {
	r := out.Data.(*raw)
	c := cfg(job)
	overall := "verified"
	if r.timedOut {
		overall = plugin.Worst(overall, "timeout")
	}
	if r.report == nil {
		overall = plugin.Worst(overall, "tool_error")
	}
	var all []assertion
	covered := []string{}
	relOf := func(name string) string {
		rel, err := filepath.Rel(job.Root, name)
		if err != nil {
			rel = name
		}
		return filepath.ToSlash(rel)
	}
	// Failures outside tests, by file: a file whose own run failed (an afterAll or other
	// hook that throws, a file that cannot be loaded; its message says why, also when a
	// test in it failed too) and unhandled errors (an exception thrown after a test, a
	// rejected promise; Vitest exits with 1 and still reports success for them).
	fileFailure := map[string]string{} // file -> why
	hasTests := map[string]bool{}
	anyFailure := false
	if r.report != nil {
		for _, f := range r.report.TestResults {
			failedTest := false
			for _, a := range f.AssertionResults {
				all = append(all, assertion{file: f.Name, fullName: a.FullName, status: a.Status, failures: a.FailureMessages})
				covered = append(covered, relOf(f.Name)+"::"+a.FullName)
				if a.Status == "failed" {
					failedTest, anyFailure = true, true
				}
			}
			hasTests[f.Name] = len(f.AssertionResults) > 0
			if f.Status == "failed" {
				anyFailure = true
				if msg := firstLine(f.Message); msg != "" || !failedTest {
					fileFailure[f.Name] = msg
				}
			}
		}
	}
	// errors of a file and its suites from the reporter: an afterAll inside a describe is
	// in no message of the JSON report
	reporterFiles := make([]string, 0, len(r.fileErrors))
	for f := range r.fileErrors {
		reporterFiles = append(reporterFiles, f)
	}
	sort.Strings(reporterFiles)
	for _, f := range reporterFiles {
		anyFailure = true
		why := fileFailure[f]
		for _, e := range r.fileErrors[f] {
			if m := firstLine(e); m != "" && !strings.Contains(why, m) {
				if why != "" {
					why += "; "
				}
				why += m
			}
		}
		fileFailure[f] = why
	}
	var unattributed []string // unhandled errors whose test file is not known
	for _, e := range r.unhandled {
		anyFailure = true
		why := "unhandled error"
		if m := firstLine(e.Message); m != "" {
			why += ": " + m
		}
		if e.File != "" && r.isolateOff {
			// without isolation the test files share one environment, so the error may come
			// from a test of another file
			why += " (reported in " + relOf(e.File) + ", but test files are not isolated)"
		}
		if e.File == "" || r.isolateOff {
			unattributed = append(unattributed, why)
			continue
		}
		if prev := fileFailure[e.File]; prev != "" {
			why = prev + "; " + why
		}
		fileFailure[e.File] = why
	}
	failedFiles := make([]string, 0, len(fileFailure))
	for f := range fileFailure {
		failedFiles = append(failedFiles, f)
	}
	sort.Strings(failedFiles)
	unexplainedExit := r.code != 0 && !r.timedOut && r.report != nil && !anyFailure
	if unexplainedExit || len(unattributed) > 0 {
		overall = plugin.Worst(overall, "failed")
	}
	var notes []string
	claims := []core.RunClaim{}
	for _, cl := range job.Claims {
		fileSuffix, name, _ := strings.Cut(cl.NativeRef, "::")
		var matched []assertion
		inMatched := map[string]bool{}
		for _, a := range all {
			if (fileSuffix == "" || strings.HasSuffix(filepath.ToSlash(a.file), fileSuffix)) && (name == "" || strings.Contains(a.fullName, name)) {
				matched = append(matched, a)
				inMatched[a.file] = true
			}
		}
		// the failures outside tests that count for this claim: those of a file with one of
		// its tests, and those of a file without test results (which tests it had is not
		// known) whose path the ref's file part matches (an empty part matches every file)
		var outside []string
		for _, f := range failedFiles {
			if inMatched[f] || (!hasTests[f] && (fileSuffix == "" || strings.HasSuffix(filepath.ToSlash(f), fileSuffix))) {
				outside = append(outside, f)
			}
		}
		result := "unknown"
		switch {
		case r.timedOut:
			result = "timeout"
		case r.report == nil:
			result = "tool_error"
		case len(matched) == 0:
			if len(outside) == 0 {
				notes = append(notes, cl.NativeRef+": no matching test")
			}
		default:
			passed, failed := 0, []assertion{}
			for _, m := range matched {
				switch m.status {
				case "passed":
					passed++
				case "failed":
					failed = append(failed, m)
				}
			}
			switch {
			case passed == len(matched):
				result = "verified"
			case len(failed) > 0:
				result = "failed"
				first := ""
				if len(failed[0].failures) > 0 {
					first = clip(firstLine(failed[0].failures[0]))
				}
				note := fmt.Sprintf("%s: %d failed", cl.NativeRef, len(failed))
				if first != "" {
					note += " (" + first + ")"
				}
				notes = append(notes, note)
			}
		}
		if !r.timedOut && r.report != nil {
			for _, f := range outside {
				result = "failed"
				note := fmt.Sprintf("%s: %s failed outside its tests", cl.NativeRef, relOf(f))
				if why := fileFailure[f]; why != "" {
					note += ": " + clip(why)
				}
				notes = append(notes, note)
			}
			if result == "verified" && (unexplainedExit || len(unattributed) > 0) {
				result = "failed"
			}
		}
		overall = plugin.Worst(overall, result)
		var stmt *string
		if len(matched) > 0 {
			names := make([]string, len(matched))
			for i, m := range matched {
				names[i] = m.fullName
			}
			s := strings.Join(names, " | ")
			stmt = &s
		}
		claims = append(claims, core.RunClaim{Spec: cl.SpecRef, Claim: cl.Claim, Requirement: cl.RequirementRef, Native: core.RunNative{Ref: cl.NativeRef, Statement: stmt}, Result: result})
	}
	bounds := core.OM("tests", int64(0))
	if r.report != nil {
		bounds.Set("tests", int64(r.report.NumTotalTests))
	}
	plugin.MergeBounds(bounds, filepath.Join(job.Root, c.boundsFrom), c.boundsFrom != "")
	summary := fmt.Sprintf("vitest produced no report (exit %d)", r.code)
	if r.report != nil {
		summary = fmt.Sprintf("%d/%d tests passed (vitest %s)", r.report.NumPassedTests, r.report.NumTotalTests, r.version)
	}
	if len(unattributed) > 0 {
		notes = append(notes, clip(strings.Join(unattributed, "; "))+": where it happened is not known, so no claim is verified")
	}
	if unexplainedExit {
		notes = append(notes, fmt.Sprintf("vitest exited with status %d though no test or file failed and no unhandled error was reported, so no claim is verified (see the log)", r.code))
	}
	if r.code != 0 && r.report != nil && !r.unhandledKnown {
		notes = append(notes, "the reporter for unhandled errors wrote nothing, so they were not read")
	}
	if r.hook == "onFinished" {
		notes = append(notes, "the errors of files and suites were read through onFinished (Vitest before 3), which was not checked with a real Vitest")
	}
	if len(notes) > 0 {
		summary += ". " + strings.Join(notes, ". ")
	}
	version := r.version
	if version == "" {
		version = "unknown"
	}
	return &core.RunRecord{
		Tool:        core.RunTool{Name: "vitest", Version: version, Extra: core.OM("path", plugin.HomeRelative(r.bin), "exit_code", int64(r.code))},
		Method:      "test",
		Claims:      claims,
		Targets:     inputs.Targets,
		Inputs:      core.RunInputs{Files: inputs.Files, Config: core.OM("project_dir", c.projectDir, "env", c.env)},
		Result:      overall,
		Assumptions: []string{"results hold for the executed test cases only"},
		Scope:       core.RunScope{Bounded: true, Bounds: bounds, Excluded: []string{}, Covered: covered},
		Artifacts:   out.Artifacts,
		Summary:     summary,
	}, nil
}

func firstLine(s string) string {
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(s), "\n", 2)[0])
}

func clip(s string) string {
	if rs := []rune(s); len(rs) > 160 {
		return string(rs[:160])
	}
	return s
}
