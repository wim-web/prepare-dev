// Package lean4 verifies theorems of a Lake project: lake build, then #check and
// #print axioms for every claimed theorem through lean --json.
package lean4

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/wim-web/prepare-dev/internal/core"
	"github.com/wim-web/prepare-dev/internal/plugin"
)

var defaultAxioms = []string{"propext", "Classical.choice", "Quot.sound"}

var forbidden = regexp.MustCompile(`\b(sorry|admit|native_decide|implemented_by)\b|^\s*axiom\b`)
var commentLine = regexp.MustCompile(`^\s*--`)

type config struct {
	projectDir   string
	allowed      []string
	buildTargets []string
	timeout      time.Duration
}

// configKeys are the keys cfg reads; the runtime refuses any other.
var configKeys = map[string]string{"project_dir": "string", "allowed_axioms": "strings", "build_targets": "strings", "timeout_ms": "int"}

func cfg(job *plugin.Job) (config, error) {
	c := config{projectDir: plugin.ConfigString(job.Config, "project_dir", "")}
	if c.projectDir == "" {
		return c, errors.New("lean4 plugin needs config.project_dir")
	}
	c.allowed = defaultAxioms
	if job.Config.Has("allowed_axioms") {
		c.allowed = plugin.ConfigStrings(job.Config, "allowed_axioms")
	}
	if job.Config.Has("build_targets") {
		c.buildTargets = plugin.ConfigStrings(job.Config, "build_targets")
	}
	c.timeout = time.Duration(plugin.ConfigInt(job.Config, "timeout_ms", 600000)) * time.Millisecond
	return c, nil
}

func moduleName(projectDir, file string) string {
	rel, _ := filepath.Rel(projectDir, file)
	rel = strings.TrimSuffix(rel, ".lean")
	return strings.Join(strings.Split(rel, string(filepath.Separator)), ".")
}

type message struct {
	Severity string `json:"severity"`
	Data     string `json:"data"`
	Text     string `json:"text"`
}

func (m message) text() string {
	if m.Data != "" {
		return m.Data
	}
	return m.Text
}

type scanHit struct {
	Path string
	Line int
}

type raw struct {
	versions   map[string]string
	buildCode  int
	buildOut   string
	buildTimed bool
	checkDone  bool
	messages   []message
	scanHits   []scanHit
}

// Plugin is the lean4 plugin.
type Plugin struct{}

// New returns the plugin.
func New() *Plugin { return &Plugin{} }

func (p *Plugin) Name() string    { return "lean4" }
func (p *Plugin) Version() string { return "0.3.0" }

func (p *Plugin) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{Languages: []string{"lean4"}, Methods: []string{"proof"}, Properties: []string{"invariant", "theorem"}, Tools: []string{"lean", "lake"}, Config: configKeys}
}

// Detect runs lean and lake in the project_dir, where elan picks the toolchain of its
// lean-toolchain file.
func (p *Plugin) Detect(job *plugin.Job) plugin.DetectResult {
	root := filepath.Join(job.Root, plugin.ConfigString(job.Config, "project_dir", "."))
	lean, err1 := plugin.Exec("lean", []string{"--version"}, plugin.ExecOptions{Dir: root, Timeout: time.Minute})
	if err1 != nil || lean.Code != 0 {
		return plugin.DetectResult{Reason: "lean not found on PATH (or ~/.elan/bin)"}
	}
	lake, err2 := plugin.Exec("lake", []string{"--version"}, plugin.ExecOptions{Dir: root, Timeout: time.Minute})
	if err2 != nil || lake.Code != 0 {
		return plugin.DetectResult{Reason: "lake not found"}
	}
	first := strings.SplitN(strings.TrimSpace(lake.Stdout), "\n", 2)[0]
	return plugin.DetectResult{Available: true, Versions: map[string]string{"lean": strings.TrimSpace(lean.Stdout), "lake": first}}
}

func (p *Plugin) Inputs(job *plugin.Job) (plugin.Inputs, error) {
	c, err := cfg(job)
	if err != nil {
		return plugin.Inputs{}, err
	}
	projectDir := filepath.Join(job.Root, c.projectDir)
	roles := map[string]string{}
	var queue []string
	for _, t := range job.Targets {
		abs := filepath.Join(job.Root, t)
		roles[abs] = "target"
		queue = append(queue, abs)
	}
	// lean --src-deps prints only direct imports: walk it until closed.
	for len(queue) > 0 {
		file := queue[0]
		queue = queue[1:]
		rel, _ := filepath.Rel(projectDir, file)
		r, err := plugin.Exec("lake", []string{"env", "lean", "--src-deps", rel}, plugin.ExecOptions{Dir: projectDir, Timeout: time.Minute})
		if err != nil {
			return plugin.Inputs{}, err
		}
		if r.Code != 0 {
			msg := r.Stderr
			if msg == "" {
				msg = r.Stdout
			}
			return plugin.Inputs{}, fmt.Errorf("lean --src-deps failed for %s: %s", file, msg)
		}
		for _, line := range strings.Split(r.Stdout, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			abs := line
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(projectDir, line)
			}
			abs = filepath.Clean(abs)
			if !strings.HasPrefix(abs, projectDir+string(filepath.Separator)) {
				continue // toolchain sources are covered by lean-toolchain
			}
			if strings.Contains(abs, string(filepath.Separator)+".lake"+string(filepath.Separator)) {
				continue
			}
			if _, ok := roles[abs]; !ok {
				roles[abs] = "import"
				queue = append(queue, abs)
			}
		}
	}
	paths := make([]string, 0, len(roles))
	for p := range roles {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var out plugin.Inputs
	for _, abs := range paths {
		sum, err := core.SHA256File(abs)
		if err != nil {
			return plugin.Inputs{}, err
		}
		rel, _ := filepath.Rel(job.Root, abs)
		f := core.InputFile{Path: filepath.ToSlash(rel), SHA256: sum, Role: roles[abs]}
		if f.Role == "target" {
			out.Targets = append(out.Targets, f)
		} else {
			out.Files = append(out.Files, f)
		}
	}
	for _, x := range [][2]string{{"lean-toolchain", "toolchain"}, {"lakefile.toml", "lake_config"}, {"lakefile.lean", "lake_config"}, {"lake-manifest.json", "dependency_lock"}} {
		abs := filepath.Join(projectDir, x[0])
		if sum, err := core.SHA256File(abs); err == nil {
			rel, _ := filepath.Rel(job.Root, abs)
			out.Files = append(out.Files, core.InputFile{Path: filepath.ToSlash(rel), SHA256: sum, Role: x[1]})
		}
	}
	if out.Targets == nil {
		out.Targets = []core.InputFile{}
	}
	if out.Files == nil {
		out.Files = []core.InputFile{}
	}
	return out, nil
}

func (p *Plugin) Run(job *plugin.Job, ctx plugin.Context) (plugin.RawOutput, error) {
	c, err := cfg(job)
	if err != nil {
		return plugin.RawOutput{}, err
	}
	projectDir := filepath.Join(job.Root, c.projectDir)
	r := &raw{versions: p.Detect(job).Versions}
	inputs, err := p.Inputs(job)
	if err != nil {
		return plugin.RawOutput{}, err
	}
	// 1. source scan for escape hatches
	for _, f := range append(append([]core.InputFile{}, inputs.Targets...), inputs.Files...) {
		if !strings.HasSuffix(f.Path, ".lean") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(job.Root, filepath.FromSlash(f.Path)))
		if err != nil {
			return plugin.RawOutput{}, err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if forbidden.MatchString(line) && !commentLine.MatchString(line) {
				r.scanHits = append(r.scanHits, scanHit{Path: f.Path, Line: i + 1})
			}
		}
	}
	// 2. build
	ctx.OnProgress("lake build")
	modules := c.buildTargets
	if modules == nil {
		for _, t := range job.Targets {
			modules = append(modules, moduleName(projectDir, filepath.Join(job.Root, t)))
		}
	}
	build, err := plugin.Exec("lake", append([]string{"build"}, modules...), plugin.ExecOptions{Dir: projectDir, Ctx: ctx.Ctx, Timeout: c.timeout, OnLine: ctx.OnProgress})
	if err != nil {
		return plugin.RawOutput{}, err
	}
	r.buildCode, r.buildOut, r.buildTimed = build.Code, build.Stdout+"\n"+build.Stderr, build.TimedOut
	buildLog := filepath.Join(ctx.ArtifactDir, "lake-build.log")
	if err := os.WriteFile(buildLog, []byte(build.Stdout+"\n"+build.Stderr), 0o644); err != nil {
		return plugin.RawOutput{}, err
	}
	artifacts := []core.Artifact{{Kind: "log", Path: buildLog}}
	// 3. statements and axioms of every claimed theorem
	if build.Code == 0 && !build.TimedOut {
		ctx.OnProgress("checking theorem statements and axioms")
		lines := []string{}
		for _, m := range modules {
			lines = append(lines, "import "+m)
		}
		lines = append(lines, "set_option pp.proofs false")
		for _, cl := range job.Claims {
			lines = append(lines, "#check @"+cl.NativeRef, "#print axioms "+cl.NativeRef)
		}
		checkFile := filepath.Join(projectDir, ".lake", "prepare-dev-check-"+job.RunID+".lean")
		if err := os.MkdirAll(filepath.Dir(checkFile), 0o755); err != nil {
			return plugin.RawOutput{}, err
		}
		if err := os.WriteFile(checkFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			return plugin.RawOutput{}, err
		}
		check, err := plugin.Exec("lake", []string{"env", "lean", "--json", checkFile}, plugin.ExecOptions{Dir: projectDir, Ctx: ctx.Ctx, Timeout: c.timeout})
		os.Remove(checkFile)
		if err != nil {
			return plugin.RawOutput{}, err
		}
		r.checkDone = true
		for _, line := range strings.Split(check.Stdout, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var m message
			if json.Unmarshal([]byte(line), &m) == nil {
				r.messages = append(r.messages, m)
			}
		}
		checkLog := filepath.Join(ctx.ArtifactDir, "lean-check.jsonl")
		if err := os.WriteFile(checkLog, []byte(check.Stdout), 0o644); err != nil {
			return plugin.RawOutput{}, err
		}
		artifacts = append(artifacts, core.Artifact{Kind: "log", Path: checkLog})
	}
	return plugin.RawOutput{Data: r, Artifacts: artifacts}, nil
}

var (
	reAtPrefix    = regexp.MustCompile(`^@\S+\s*:\s*`)
	rePrefix      = regexp.MustCompile(`^\S+\s*:\s*`)
	reAxiomPrefix = regexp.MustCompile(`^'[^']+' depends on axioms:\s*`)
)

func (p *Plugin) Parse(out plugin.RawOutput, job *plugin.Job, inputs plugin.Inputs) (*core.RunRecord, error) {
	c, err := cfg(job)
	if err != nil {
		return nil, err
	}
	r := out.Data.(*raw)
	allowed := map[string]bool{}
	for _, a := range c.allowed {
		allowed[a] = true
	}
	sorryInBuild := strings.Contains(r.buildOut, "declaration uses 'sorry'")
	overall := "verified"
	if r.buildTimed {
		overall = plugin.Worst(overall, "timeout")
	} else if r.buildCode != 0 {
		overall = plugin.Worst(overall, "failed")
	}
	if len(r.scanHits) > 0 || sorryInBuild {
		overall = plugin.Worst(overall, "unknown")
	}
	axiomsSeen := map[string]bool{}
	var notes []string
	claims := []core.RunClaim{}
	for _, cl := range job.Claims {
		var statement *string
		var axioms []string
		axiomsFound := false
		result := "failed"
		switch {
		case r.buildCode == 0 && !r.buildTimed:
			result = "verified"
		case r.buildTimed:
			result = "timeout"
		}
		ref := cl.NativeRef
		for _, m := range r.messages {
			t := m.text()
			if m.Severity == "error" && strings.Contains(t, ref) {
				result = "failed"
				notes = append(notes, ref+": "+strings.SplitN(t, "\n", 2)[0])
			}
			if m.Severity == "information" && strings.HasPrefix(t, "@"+ref+" :") {
				s := strings.TrimSpace(reAtPrefix.ReplaceAllString(t, ""))
				statement = &s
			} else if m.Severity == "information" && strings.HasPrefix(t, ref+" :") {
				s := strings.TrimSpace(rePrefix.ReplaceAllString(t, ""))
				statement = &s
			}
			if strings.HasPrefix(t, "'"+ref+"' depends on axioms:") {
				list := strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(reAxiomPrefix.ReplaceAllString(t, "")), "]"), "[")
				axioms = nil
				for _, a := range strings.Split(list, ",") {
					if a = strings.TrimSpace(a); a != "" {
						axioms = append(axioms, a)
					}
				}
				axiomsFound = true
			} else if strings.HasPrefix(t, "'"+ref+"' does not depend on any axioms") {
				axioms = nil
				axiomsFound = true
			}
		}
		if r.buildCode == 0 && !r.buildTimed && (statement == nil || !axiomsFound) {
			result = "unknown"
			notes = append(notes, ref+": statement or axioms not found in lean output")
		}
		var extra []string
		for _, a := range axioms {
			axiomsSeen[a] = true
			if !allowed[a] {
				extra = append(extra, a)
			}
		}
		if len(extra) > 0 {
			result = "unknown"
			notes = append(notes, ref+": depends on non-allowed axioms "+strings.Join(extra, ", "))
		}
		if (len(r.scanHits) > 0 || sorryInBuild) && result != "failed" {
			result = "unknown"
		}
		overall = plugin.Worst(overall, result)
		claims = append(claims, core.RunClaim{Spec: cl.SpecRef, Claim: cl.Claim, Requirement: cl.RequirementRef, Native: core.RunNative{Ref: ref, Statement: statement}, Result: result})
	}
	if len(r.scanHits) > 0 {
		var hits []string
		for _, h := range r.scanHits {
			hits = append(hits, fmt.Sprintf("%s:%d", h.Path, h.Line))
		}
		notes = append(notes, "escape hatches found: "+strings.Join(hits, ", "))
	}
	seen := make([]string, 0, len(axiomsSeen))
	for a := range axiomsSeen {
		seen = append(seen, a)
	}
	sort.Strings(seen)
	verified := 0
	covered := []string{}
	for _, cl := range claims {
		if cl.Result == "verified" {
			verified++
		}
		covered = append(covered, cl.Native.Ref)
	}
	leanVersion := r.versions["lean"]
	if leanVersion == "" {
		leanVersion = "lean"
	}
	parts := []string{fmt.Sprintf("%d/%d claims verified by lake build (%s)", verified, len(claims), leanVersion)}
	if len(seen) > 0 {
		parts = append(parts, "axioms: "+strings.Join(seen, ", "))
	}
	parts = append(parts, notes...)
	axiomsText := strings.Join(seen, ", ")
	if axiomsText == "" {
		axiomsText = "none"
	}
	allowedList := make([]any, len(c.allowed))
	for i, a := range c.allowed {
		allowedList[i] = a
	}
	toolVersion := r.versions["lean"]
	if toolVersion == "" {
		toolVersion = "unknown"
	}
	return &core.RunRecord{
		Tool:        core.RunTool{Name: "lean", Version: toolVersion, Extra: core.OM("lake", r.versions["lake"], "lake_path", plugin.ToolLocation("lake"))},
		Method:      "proof",
		Claims:      claims,
		Targets:     inputs.Targets,
		Inputs:      core.RunInputs{Files: inputs.Files, Config: core.OM("project_dir", c.projectDir, "allowed_axioms", allowedList)},
		Result:      overall,
		Assumptions: []string{"axioms: " + axiomsText, "model assumptions as stated in the spec frontmatter"},
		Scope:       core.RunScope{Bounded: false, Bounds: nil, Excluded: []string{}, Covered: covered},
		Artifacts:   out.Artifacts,
		Summary:     strings.Join(parts, ". "),
	}, nil
}
