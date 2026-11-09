// Package gotest runs "go test -json" for a Go module and records the files the result
// depends on: every file go list reports for the packages the tests build (and for the
// packages in config input_packages, which the tests build themselves), the testdata
// directories of the tested packages, the files of config impl_files, and the module
// files. It also records listings of the directories where a new file could change the
// result (the packages' directories, what go:embed patterns match, testdata), so that
// an added file makes the evidence stale like a changed one.
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
func (p *Plugin) Version() string { return "0.2.0" }

func (p *Plugin) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{Languages: []string{"go"}, Methods: []string{"test"}, Properties: []string{"example", "property", "conformance"}, Tools: []string{"go"}}
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
	// EmbedPatterns are the package's own; the patterns of its test files are only
	// on the package itself (not on "p [p.test]"), in TestEmbedPatterns and
	// XTestEmbedPatterns.
	EmbedPatterns, TestEmbedPatterns, XTestEmbedPatterns []string
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
	listed := map[dirListing]bool{}
	var dirs []dirListing
	addDir := func(l dirListing) {
		if !listed[l] {
			listed[l] = true
			dirs = append(dirs, l)
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
		var listedPkgs []listedPackage
		dec := json.NewDecoder(strings.NewReader(r.Stdout))
		for {
			var lp listedPackage
			if err := dec.Decode(&lp); err == io.EOF {
				break
			} else if err != nil {
				return fmt.Errorf("go list output: %w", err)
			}
			listedPkgs = append(listedPkgs, lp)
		}
		// with -test, the packages whose tests are built appear as ForTest of "p [p.test]"
		tested := map[string]bool{}
		for _, lp := range listedPkgs {
			if lp.ForTest != "" {
				tested[lp.ForTest] = true
			}
		}
		for _, lp := range listedPkgs {
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
				addDir(dirListing{dir: dir}) // a new file in the directory may join the package
			}
			patterns := lp.EmbedPatterns
			if lp.ForTest == "" && tested[lp.ImportPath] {
				patterns = append(append(append([]string{}, patterns...), lp.TestEmbedPatterns...), lp.XTestEmbedPatterns...)
			}
			for _, pat := range patterns {
				for _, l := range embedListings(dir, pat) {
					addDir(l)
				}
			}
			if lp.ForTest != "" {
				testdata[filepath.Join(dir, "testdata")] = true
			}
		}
		return nil
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
		// every name, as every file under testdata is an input (hidden ones too)
		addDir(dirListing{dir: d, recursive: true, mode: plugin.ListAll})
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
		a, b := dirs[i], dirs[j]
		if a.dir != b.dir {
			return a.dir < b.dir
		}
		if a.recursive != b.recursive {
			return !a.recursive
		}
		if a.mode != b.mode {
			return a.mode < b.mode
		}
		return a.match < b.match
	})
	for _, d := range dirs {
		l, err := plugin.ListingOf(job.Root, d.dir, d.recursive, d.mode, d.match)
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

// dirListing is a directory listing to record (see plugin.ListingDigest).
type dirListing struct {
	dir         string
	recursive   bool
	mode, match string
}

// embedListings returns the listings that change whenever the files a //go:embed
// pattern takes from the package directory dir can change without a file it takes now
// changing. go:embed matches the pattern one element at a time against the names in a
// directory with filepath.Match (so "*" matches names beginning with "." too) and takes
// every file under a matched directory, leaving out names beginning with "." or "_"
// there unless the pattern starts with all:. So, from the first element with a wildcard
// on, every directory searched gets a listing of the names its element matches, files
// and directories alike, and every matched directory a listing of the files go:embed
// takes from it.
func embedListings(dir, pattern string) []dirListing {
	tree := plugin.ListGoEmbed
	if p, ok := strings.CutPrefix(pattern, "all:"); ok {
		pattern, tree = p, plugin.ListAll
	}
	elems := strings.Split(pattern, "/")
	var out []dirListing
	searched := []string{dir}
	wild := false
	for i, elem := range elems {
		meta := strings.ContainsAny(elem, "*?[\\")
		wild = wild || meta
		var next []string
		for _, d := range searched {
			if wild {
				out = append(out, dirListing{dir: d, mode: plugin.ListAll, match: elem})
			}
			names := []string{elem}
			if meta {
				names = nil
				entries, _ := os.ReadDir(d)
				for _, e := range entries {
					if ok, _ := filepath.Match(elem, e.Name()); ok {
						names = append(names, e.Name())
					}
				}
			}
			for _, n := range names {
				p := filepath.Join(d, n)
				if st, err := os.Stat(p); err != nil || !st.IsDir() {
					continue // a matched file is in EmbedFiles
				}
				if i == len(elems)-1 {
					out = append(out, dirListing{dir: p, recursive: true, mode: tree})
				} else {
					next = append(next, p)
				}
			}
		}
		searched = next
	}
	return out
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
	// ImportPath is the build a build-output event belongs to (go 1.24 and later report
	// compiler errors this way); FailedBuild, on a package's fail, names the build that
	// failed.
	ImportPath  string
	FailedBuild string
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
	buildOutput := map[string][]string{} // build (ImportPath) -> compiler output
	failedBuild := map[string]string{}   // package -> the build that failed
	timedOut := r.timedOut
	for _, e := range r.events {
		if e.Action == "build-output" {
			buildOutput[e.ImportPath] = append(buildOutput[e.ImportPath], e.Output)
			continue
		}
		if e.Test == "" {
			switch e.Action {
			case "pass", "fail", "skip":
				pkgStatus[e.Package] = e.Action
				if e.FailedBuild != "" {
					failedBuild[e.Package] = e.FailedBuild
				}
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
	// A package can fail outside its tests: TestMain's exit code, a panic or an exit
	// after the tests, a build that failed before any test ran. None of the claims with
	// tests there is verified then. Such a failure is seen when no test of the package
	// failed, and, when one did, if the package panicked outside its tests (go test
	// puts a panic inside a test under that test); a failed test hides the rest, such as
	// TestMain's own check that exits non-zero. A non-zero exit of go test that no failed
	// package or test explains leaves no claim verified.
	//
	// The test binary can also end before all the package's tests ran: it failed to
	// build or exited before running them (no test results), a test did not finish (go
	// test puts what happens then, such as a panic of a goroutine another test left,
	// under the running test), or a test panicked (the binary ends there). Which tests
	// did not run is not known then, so such a package counts for every claim whose ref's
	// package part matches it, like a package that failed to build.
	failedOutside := map[string]bool{}
	truncated := map[string]string{} // package -> why it ended before all its tests ran
	anyFailure := false
	for pkg, st := range pkgStatus {
		if st != "fail" {
			continue
		}
		anyFailure = true
		explained := false
		var unfinished, panicked *testResult
		for _, k := range order {
			t := tests[k]
			if t.pkg != pkg {
				continue
			}
			switch t.status {
			case "fail":
				explained = true
				if panicked == nil && panicLine(t.output) != "" {
					panicked = t
				}
			case "":
				if unfinished == nil {
					unfinished = t
				}
			}
		}
		switch {
		case !hasTests(tests, pkg):
			truncated[pkg] = "failed before running tests: " + firstFailureLine(buildOutput[failedBuild[pkg]], pkgOutput[pkg])
		case unfinished != nil:
			why := "stopped while " + unfinished.name + " ran"
			if line := panicLine(unfinished.output); line != "" {
				why += ": " + line
			}
			truncated[pkg] = why
		case panicked != nil:
			truncated[pkg] = "stopped when " + panicked.name + " panicked, so the tests after it did not run: " + panicLine(panicked.output)
		}
		if !explained || truncated[pkg] != "" || panicLine(pkgOutput[pkg]) != "" {
			failedOutside[pkg] = true
		}
	}
	truncatedPkgs := make([]string, 0, len(truncated))
	for pkg := range truncated {
		truncatedPkgs = append(truncatedPkgs, pkg)
	}
	sort.Strings(truncatedPkgs)
	for _, t := range tests {
		if t.status != "pass" && t.status != "skip" {
			anyFailure = true
		}
	}
	unexplainedExit := r.code != 0 && !timedOut && len(r.events) > 0 && !anyFailure
	overall := "verified"
	if timedOut {
		overall = plugin.Worst(overall, "timeout")
	}
	if len(r.events) == 0 {
		overall = plugin.Worst(overall, "tool_error")
	}
	if unexplainedExit {
		overall = plugin.Worst(overall, "failed")
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
		for _, pkg := range truncatedPkgs {
			if pkgSuffix == "" || strings.HasSuffix(pkg, pkgSuffix) {
				buildFailed = true
				notes = append(notes, fmt.Sprintf("%s: package %s %s", cl.NativeRef, pkg, truncated[pkg]))
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
			case passed == len(matched) && unexplainedExit:
				result = "failed"
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
			seen := map[string]bool{}
			for _, m := range matched {
				if failedOutside[m.pkg] && truncated[m.pkg] == "" && !seen[m.pkg] {
					seen[m.pkg] = true
					result = "failed"
					note := fmt.Sprintf("%s: package %s failed outside its tests (the exit of the test binary, such as TestMain's)", cl.NativeRef, m.pkg)
					if line := outsideLine(pkgOutput[m.pkg]); line != "" {
						note += ": " + line
					}
					notes = append(notes, note)
				}
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
	if unexplainedExit {
		notes = append(notes, fmt.Sprintf("go test exited with status %d without a failed package or test, so no claim is verified (see the log)", r.code))
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
		Tool:        core.RunTool{Name: "go", Version: version, Extra: core.OM("path", plugin.ToolLocation("go"), "exit_code", int64(r.code))},
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

func clip(s string) string {
	if rs := []rune(s); len(rs) > 160 {
		return string(rs[:160])
	}
	return s
}

// panicLine is the first "panic:" or "fatal error:" line a package printed outside its
// tests, or "".
func panicLine(lines []string) string {
	for _, l := range lines {
		if s := strings.TrimSpace(l); strings.HasPrefix(s, "panic: ") || strings.HasPrefix(s, "fatal error: ") {
			return clip(s)
		}
	}
	return ""
}

// outsideLine says why a package failed outside its tests: its panic line, or else the
// last line it printed outside its tests, without go test's own PASS, FAIL, ok and exit
// status lines.
func outsideLine(lines []string) string {
	if s := panicLine(lines); s != "" {
		return s
	}
	for i := len(lines) - 1; i >= 0; i-- {
		s := strings.TrimSpace(lines[i])
		if s == "" || s == "PASS" || s == "FAIL" || strings.HasPrefix(s, "FAIL\t") || strings.HasPrefix(s, "ok ") || strings.HasPrefix(s, "exit status ") {
			continue
		}
		return clip(s)
	}
	return ""
}

// firstFailureLine says why a package failed before running tests: the first compiler
// error of its build, or else what outsideLine says.
func firstFailureLine(build, pkg []string) string {
	for _, l := range build {
		if s := strings.TrimSpace(l); s != "" && !strings.HasPrefix(s, "# ") {
			return clip(s)
		}
	}
	return outsideLine(pkg)
}

func hasTests(tests map[string]*testResult, pkg string) bool {
	for _, t := range tests {
		if t.pkg == pkg {
			return true
		}
	}
	return false
}
