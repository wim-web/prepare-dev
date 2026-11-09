// Package gotest runs "go test -json" for a Go module and records the files the result
// depends on: every file go list reports for the packages the tests build (and for the
// packages in config input_packages, which the tests build themselves), the testdata
// directories of the tested packages, the files of config impl_files, and the module
// files. It also records the listings of those directories, so that an added file
// makes the evidence stale like a changed one.
package gotest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	preparedev "github.com/wim-web/prepare-dev"
	"github.com/wim-web/prepare-dev/internal/core"
	"github.com/wim-web/prepare-dev/internal/plugin"
)

type config struct {
	projectDir string
	packages   []string
	// inputPackages are packages the tests build or run themselves (go build, go run);
	// their files are inputs like those of the tested packages.
	inputPackages []string
	// implFiles are paths or patterns (with **), relative to the root, of other files
	// the result depends on, such as files the tests read outside testdata.
	implFiles []string
	env       *core.OMap
	timeout   time.Duration
}

func cfg(job *plugin.Job) config {
	c := config{
		projectDir:    plugin.ConfigString(job.Config, "project_dir", "."),
		packages:      plugin.ConfigStrings(job.Config, "packages"),
		inputPackages: plugin.ConfigStrings(job.Config, "input_packages"),
		implFiles:     plugin.ConfigStrings(job.Config, "impl_files"),
		timeout:       time.Duration(plugin.ConfigInt(job.Config, "timeout_ms", 600000)) * time.Millisecond,
		env:           core.NewOMap(),
	}
	if e, ok := job.Config.Map("env"); ok {
		c.env = e
	}
	return c
}

func (c config) envMap() map[string]string {
	out := map[string]string{}
	for _, k := range c.env.Keys() {
		if s, ok := c.env.Str(k); ok {
			out[k] = s
		}
	}
	return out
}

// packagesOf returns the packages to test: config.packages, or the directories of the
// target files as "./dir" patterns relative to the project dir.
func packagesOf(job *plugin.Job, c config) []string {
	if len(c.packages) > 0 {
		return c.packages
	}
	projectDir := filepath.Join(job.Root, c.projectDir)
	seen := map[string]bool{}
	var out []string
	for _, t := range job.Targets {
		dir := filepath.Dir(filepath.Join(job.Root, t))
		rel, err := filepath.Rel(projectDir, dir)
		if err != nil {
			continue
		}
		pkg := "./" + filepath.ToSlash(rel)
		if rel == "." {
			pkg = "."
		}
		if !seen[pkg] {
			seen[pkg] = true
			out = append(out, pkg)
		}
	}
	return out
}

// Plugin is the gotest plugin.
type Plugin struct{}

// New returns the plugin.
func New() *Plugin { return &Plugin{} }

func (p *Plugin) Name() string    { return "gotest" }
func (p *Plugin) Version() string { return "0.1.0" }

func (p *Plugin) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{Languages: []string{"go"}, Methods: []string{"test"}, Properties: []string{"example", "property", "conformance"}, Tools: []string{"go"}}
}

func (p *Plugin) Guidance() string {
	data, _ := preparedev.Guidance("gotest")
	return string(data)
}

func (p *Plugin) Detect(job *plugin.Job) plugin.DetectResult {
	r, err := plugin.Exec("go", []string{"version"}, plugin.ExecOptions{Dir: filepath.Join(job.Root, cfg(job).projectDir), Timeout: time.Minute})
	if err != nil || r.Code != 0 {
		return plugin.DetectResult{Reason: "go not found on PATH"}
	}
	return plugin.DetectResult{Available: true, Versions: map[string]string{"go": strings.TrimSpace(r.Stdout)}}
}

type listedModule struct {
	Main    bool
	Replace *struct {
		Path    string
		Version string
	}
}

// listedPackage is what go list -json reports. With -test, a tested package appears
// again as "p [p.test]" (ForTest set) whose GoFiles and EmbedFiles include the test
// files; TestGoFiles of other packages are not built and are ignored.
type listedPackage struct {
	ImportPath string
	Dir        string
	ForTest    string
	Standard   bool
	Module     *listedModule
	Error      *struct{ Err string }

	GoFiles, CgoFiles, CFiles, CXXFiles, MFiles, HFiles, FFiles, SFiles []string
	SwigFiles, SwigCXXFiles, SysoFiles, EmbedFiles                      []string
	EmbedPatterns                                                       []string
}

func (lp *listedPackage) files() []string {
	var out []string
	for _, group := range [][]string{lp.GoFiles, lp.CgoFiles, lp.CFiles, lp.CXXFiles, lp.MFiles, lp.HFiles, lp.FFiles, lp.SFiles, lp.SwigFiles, lp.SwigCXXFiles, lp.SysoFiles, lp.EmbedFiles} {
		out = append(out, group...)
	}
	return out
}

// local reports whether the package's files are not covered by go.sum: the main
// module, a module replaced by a local directory, or a vendored package.
func (lp *listedPackage) local(vendorDir string) bool {
	if lp.Standard {
		return false
	}
	if lp.Module == nil {
		return strings.HasPrefix(lp.Dir, vendorDir+string(filepath.Separator))
	}
	if lp.Module.Main {
		return true
	}
	if lp.Module.Replace != nil && lp.Module.Replace.Version == "" {
		return true
	}
	return strings.HasPrefix(lp.Dir, vendorDir+string(filepath.Separator))
}

// Inputs: the targets, every source and embedded file of the main-module packages the
// tests build, the implementation files from config, and go.mod, go.sum and go.work.
func (p *Plugin) Inputs(job *plugin.Job) (plugin.Inputs, error) {
	c := cfg(job)
	projectDir := filepath.Join(job.Root, c.projectDir)
	set := plugin.NewInputSet(job.Root)
	out := plugin.Inputs{Targets: []core.InputFile{}}
	targetSet := map[string]bool{}
	for _, t := range job.Targets {
		sum, err := core.SHA256File(filepath.Join(job.Root, t))
		if err != nil {
			return plugin.Inputs{}, err
		}
		out.Targets = append(out.Targets, core.InputFile{Path: filepath.ToSlash(t), SHA256: sum, Role: "target"})
		targetSet[filepath.Clean(filepath.Join(job.Root, t))] = true
	}
	implFiles, implListings, err := plugin.ExpandPatterns(job.Root, c.implFiles)
	if err != nil {
		return plugin.Inputs{}, err
	}
	for _, f := range implFiles {
		if err := set.Add(f, "implementation"); err != nil {
			return plugin.Inputs{}, err
		}
	}
	out.Listings = append(out.Listings, implListings...)
	pkgs := packagesOf(job, c)
	if len(pkgs) == 0 {
		return plugin.Inputs{}, errors.New("gotest needs targets (test files) or config.packages")
	}
	// go list reports real paths; map them back under the project root as given
	realRoot, err := filepath.EvalSymlinks(job.Root)
	if err != nil {
		realRoot = job.Root
	}
	realProject, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		realProject = projectDir
	}
	underRoot := func(p string) string {
		if realRoot != job.Root && strings.HasPrefix(p, realRoot+string(filepath.Separator)) {
			return filepath.Join(job.Root, p[len(realRoot)+1:])
		}
		return p
	}
	vendorDir := filepath.Join(realProject, "vendor")
	var files []string
	listed := map[string]bool{} // directory -> listing recorded (recursive or not)
	type listing struct {
		dir       string
		recursive bool
	}
	var dirs []listing
	addDir := func(dir string, recursive bool) {
		key := fmt.Sprintf("%s|%v", dir, recursive)
		if !listed[key] {
			listed[key] = true
			dirs = append(dirs, listing{dir, recursive})
		}
	}
	testdata := map[string]bool{}
	list := func(args []string) error {
		r, err := plugin.Exec("go", args, plugin.ExecOptions{Dir: projectDir, Timeout: 5 * time.Minute, Env: c.envMap()})
		if err != nil {
			return err
		}
		if r.Code != 0 {
			return fmt.Errorf("go list failed: %s", strings.TrimSpace(r.Stderr))
		}
		dec := json.NewDecoder(strings.NewReader(r.Stdout))
		for {
			var lp listedPackage
			if err := dec.Decode(&lp); err == io.EOF {
				return nil
			} else if err != nil {
				return fmt.Errorf("go list output: %w", err)
			}
			if !lp.local(vendorDir) {
				continue // the standard library, and modules pinned by go.sum
			}
			dir := underRoot(lp.Dir)
			compiled := false
			for _, f := range lp.files() {
				if filepath.IsAbs(f) {
					continue // generated by go (the test main lives in the build cache)
				}
				files = append(files, filepath.Join(dir, f))
				compiled = true
			}
			if compiled {
				addDir(dir, false) // a new file in the directory may join the package
			}
			for _, pat := range lp.EmbedPatterns {
				pat = strings.TrimPrefix(pat, "all:")
				target := filepath.Join(dir, filepath.FromSlash(pat))
				if st, err := os.Stat(target); err == nil && st.IsDir() {
					addDir(target, true) // an embedded directory takes every file added to it
				} else if strings.ContainsAny(pat, "*?[") {
					addDir(filepath.Dir(target), false)
				}
			}
			if lp.ForTest != "" {
				testdata[filepath.Join(dir, "testdata")] = true
			}
		}
	}
	if err := list(append([]string{"list", "-deps", "-test", "-json"}, pkgs...)); err != nil {
		return plugin.Inputs{}, err
	}
	if len(c.inputPackages) > 0 {
		if err := list(append([]string{"list", "-deps", "-json"}, c.inputPackages...)); err != nil {
			return plugin.Inputs{}, err
		}
	}
	// the tests' own files: everything under the testdata directory of a tested package
	var tdDirs []string
	for d := range testdata {
		tdDirs = append(tdDirs, d)
	}
	sort.Strings(tdDirs)
	for _, d := range tdDirs {
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			continue
		}
		_ = filepath.WalkDir(d, func(p string, e fs.DirEntry, err error) error {
			if err == nil && e.Type().IsRegular() {
				files = append(files, p)
			}
			return nil
		})
		addDir(d, true)
	}
	sort.Strings(files)
	for _, f := range files {
		if targetSet[filepath.Clean(f)] {
			continue
		}
		role := "import"
		if strings.Contains(f, string(filepath.Separator)+"testdata"+string(filepath.Separator)) {
			role = "data"
		}
		if err := set.Add(f, role); err != nil {
			return plugin.Inputs{}, err
		}
	}
	sort.Slice(dirs, func(i, j int) bool {
		return dirs[i].dir < dirs[j].dir || dirs[i].dir == dirs[j].dir && !dirs[i].recursive
	})
	for _, d := range dirs {
		l, err := plugin.Listing(job.Root, d.dir, d.recursive)
		if err != nil {
			return plugin.Inputs{}, err
		}
		out.Listings = append(out.Listings, l)
	}
	for _, x := range [][2]string{{"go.mod", "config"}, {"go.sum", "dependency_lock"}, {"go.work", "config"}, {"go.work.sum", "dependency_lock"}, {filepath.Join("vendor", "modules.txt"), "dependency_lock"}} {
		if abs := filepath.Join(projectDir, x[0]); fileExists(abs) {
			if err := set.Add(abs, x[1]); err != nil {
				return plugin.Inputs{}, err
			}
		}
	}
	out.Files = set.Files()
	return out, nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

type event struct {
	Action  string
	Package string
	Test    string
	Output  string
}

type raw struct {
	version   string
	code      int
	timedOut  bool
	events    []event
	boundsDir string
}

func (p *Plugin) Run(job *plugin.Job, ctx plugin.Context) (plugin.RawOutput, error) {
	c := cfg(job)
	projectDir := filepath.Join(job.Root, c.projectDir)
	ver, err := plugin.Exec("go", []string{"version"}, plugin.ExecOptions{Dir: projectDir, Timeout: time.Minute})
	if err != nil {
		return plugin.RawOutput{}, err
	}
	pkgs := packagesOf(job, c)
	// tests may describe what they covered in JSON files in this run's own directory;
	// they go to scope.bounds
	boundsDir := filepath.Join(ctx.ArtifactDir, "bounds")
	if err := os.MkdirAll(boundsDir, 0o755); err != nil {
		return plugin.RawOutput{}, err
	}
	env := c.envMap()
	env["PREPARE_DEV_BOUNDS_DIR"] = boundsDir
	// -count=1 disables the test cache: a cached result is not evidence of this run.
	args := append([]string{"test", "-json", "-count=1", fmt.Sprintf("-timeout=%dms", c.timeout.Milliseconds())}, pkgs...)
	ctx.OnProgress("go " + strings.Join(args, " "))
	// the daemon's own PREPARE_DEV_ variables (for example PREPARE_DEV_E2E_BINARY) are not
	// passed on: only config.env decides what the tests see
	r, err := plugin.Exec("go", args, plugin.ExecOptions{Dir: projectDir, Ctx: ctx.Ctx, Timeout: c.timeout + time.Minute, Env: env, DropEnvPrefix: "PREPARE_DEV_"})
	if err != nil {
		return plugin.RawOutput{}, err
	}
	reportPath := filepath.Join(ctx.ArtifactDir, "go-test.jsonl")
	if err := os.WriteFile(reportPath, []byte(r.Stdout), 0o644); err != nil {
		return plugin.RawOutput{}, err
	}
	logPath := filepath.Join(ctx.ArtifactDir, "go-test.stderr.log")
	if err := os.WriteFile(logPath, []byte(r.Stderr), 0o644); err != nil {
		return plugin.RawOutput{}, err
	}
	rw := &raw{version: strings.TrimSpace(ver.Stdout), code: r.Code, timedOut: r.TimedOut, boundsDir: boundsDir}
	sc := bufio.NewScanner(bytes.NewReader([]byte(r.Stdout)))
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var e event
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Action != "" {
			rw.events = append(rw.events, e)
		}
	}
	return plugin.RawOutput{Data: rw, Artifacts: []core.Artifact{{Kind: "report", Path: reportPath}, {Kind: "log", Path: logPath}}}, nil
}

type testResult struct {
	pkg, name, status string
	output            []string
}

func (p *Plugin) Parse(out plugin.RawOutput, job *plugin.Job, inputs plugin.Inputs) (*core.RunRecord, error) {
	r := out.Data.(*raw)
	c := cfg(job)
	tests := map[string]*testResult{}
	var order []string
	pkgStatus := map[string]string{}
	pkgOutput := map[string][]string{}
	timedOut := r.timedOut
	for _, e := range r.events {
		if e.Test == "" {
			switch e.Action {
			case "pass", "fail", "skip":
				pkgStatus[e.Package] = e.Action
			case "output":
				pkgOutput[e.Package] = append(pkgOutput[e.Package], e.Output)
				if strings.HasPrefix(e.Output, "panic: test timed out") {
					timedOut = true
				}
			}
			continue
		}
		key := e.Package + "\x00" + e.Test
		t, ok := tests[key]
		if !ok {
			t = &testResult{pkg: e.Package, name: e.Test}
			tests[key] = t
			order = append(order, key)
		}
		switch e.Action {
		case "pass", "fail", "skip":
			t.status = e.Action
		case "output":
			t.output = append(t.output, e.Output)
		}
	}
	overall := "verified"
	if timedOut {
		overall = plugin.Worst(overall, "timeout")
	}
	if len(r.events) == 0 {
		overall = plugin.Worst(overall, "tool_error")
	}
	covered := []string{}
	for _, k := range order {
		t := tests[k]
		covered = append(covered, t.pkg+"::"+t.name)
	}
	var notes []string
	claims := []core.RunClaim{}
	for _, cl := range job.Claims {
		pkgSuffix, name, _ := strings.Cut(cl.NativeRef, "::")
		var matched []*testResult
		for _, k := range order {
			t := tests[k]
			if (pkgSuffix == "" || strings.HasSuffix(t.pkg, pkgSuffix)) && (name == "" || strings.Contains(t.name, name)) {
				matched = append(matched, t)
			}
		}
		buildFailed := false
		for pkg, st := range pkgStatus {
			if st == "fail" && (pkgSuffix == "" || strings.HasSuffix(pkg, pkgSuffix)) && !hasTests(tests, pkg) {
				buildFailed = true
				first := ""
				for _, line := range pkgOutput[pkg] {
					if s := strings.TrimSpace(line); s != "" {
						first = s
						break
					}
				}
				notes = append(notes, fmt.Sprintf("%s: package %s failed before running tests (%s)", cl.NativeRef, pkg, first))
			}
		}
		result := "unknown"
		switch {
		case timedOut:
			result = "timeout"
		case len(r.events) == 0:
			result = "tool_error"
		case buildFailed:
			result = "failed"
		case len(matched) == 0:
			notes = append(notes, cl.NativeRef+": no matching test")
		default:
			passed, skipped := 0, 0
			var failed []*testResult
			for _, m := range matched {
				switch m.status {
				case "pass":
					passed++
				case "skip":
					skipped++
				default:
					failed = append(failed, m)
				}
			}
			switch {
			case passed == len(matched):
				result = "verified"
			case len(failed) > 0:
				result = "failed"
				first := ""
				for _, line := range failed[0].output {
					if s := strings.TrimSpace(line); s != "" && !strings.HasPrefix(s, "=== ") && !strings.HasPrefix(s, "--- ") {
						first = s
						break
					}
				}
				if rs := []rune(first); len(rs) > 160 {
					first = string(rs[:160])
				}
				note := fmt.Sprintf("%s: %d failed", cl.NativeRef, len(failed))
				if first != "" {
					note += " (" + first + ")"
				}
				notes = append(notes, note)
			default:
				notes = append(notes, fmt.Sprintf("%s: %d skipped", cl.NativeRef, skipped))
			}
		}
		overall = plugin.Worst(overall, result)
		var stmt *string
		if len(matched) > 0 {
			names := make([]string, len(matched))
			for i, m := range matched {
				names[i] = m.name
			}
			s := strings.Join(names, " | ")
			stmt = &s
		}
		claims = append(claims, core.RunClaim{Spec: cl.SpecRef, Claim: cl.Claim, Requirement: cl.RequirementRef, Native: core.RunNative{Ref: cl.NativeRef, Statement: stmt}, Result: result})
	}
	passedAll := 0
	for _, k := range order {
		if tests[k].status == "pass" {
			passedAll++
		}
	}
	bounds := core.OM("tests", int64(len(order)))
	plugin.MergeBoundsDir(bounds, r.boundsDir)
	summary := fmt.Sprintf("%d/%d tests passed (%s)", passedAll, len(order), r.version)
	if len(r.events) == 0 {
		summary = fmt.Sprintf("go test produced no JSON events (exit %d)", r.code)
	}
	if len(notes) > 0 {
		summary += ". " + strings.Join(notes, ". ")
	}
	pkgList := make([]any, 0)
	for _, p := range packagesOf(job, c) {
		pkgList = append(pkgList, p)
	}
	version := r.version
	if version == "" {
		version = "unknown"
	}
	return &core.RunRecord{
		Tool:        core.RunTool{Name: "go", Version: version, Extra: core.OM("path", plugin.ToolLocation("go"))},
		Method:      "test",
		Claims:      claims,
		Targets:     inputs.Targets,
		Inputs:      core.RunInputs{Files: inputs.Files, Config: core.OM("project_dir", c.projectDir, "packages", pkgList, "env", c.env)},
		Result:      overall,
		Assumptions: []string{"results hold for the executed test cases only", "third-party modules match go.sum"},
		Scope:       core.RunScope{Bounded: true, Bounds: bounds, Excluded: []string{}, Covered: covered},
		Artifacts:   out.Artifacts,
		Summary:     summary,
	}, nil
}

func hasTests(tests map[string]*testResult, pkg string) bool {
	for _, t := range tests {
		if t.pkg == pkg {
			return true
		}
	}
	return false
}
