import fs from "node:fs";
import path from "node:path";
import {
  type Capabilities,
  type DetectResult,
  type InputFile,
  type PluginContext,
  type RawOutput,
  type RunRecordDraft,
  type VerificationPlugin,
  type VerifyJob,
  type VerifyResult,
  exec,
  sha256File,
} from "@prepare-dev/core";

export interface Lean4Config {
  /** Lake project directory, relative to the project root. */
  project_dir: string;
  /** Axioms a verified theorem may depend on. */
  allowed_axioms?: string[];
  /** Extra lake build targets (defaults to the modules of the target files). */
  build_targets?: string[];
  timeout_ms?: number;
}

const DEFAULT_AXIOMS = ["propext", "Classical.choice", "Quot.sound"];
const FORBIDDEN = /\b(sorry|admit|native_decide|implemented_by)\b|^\s*axiom\b/m;

interface LeanMessage {
  severity: "error" | "warning" | "information";
  fileName?: string;
  pos?: { line: number; column: number };
  data?: string;
  text?: string;
}

interface Raw {
  tool_versions: Record<string, string>;
  build: { code: number | null; stdout: string; stderr: string; timedOut: boolean };
  check: { code: number | null; messages: LeanMessage[]; stderr: string } | null;
  scan_hits: Array<{ path: string; line: number; text: string }>;
}

function cfg(job: VerifyJob): Required<Pick<Lean4Config, "project_dir" | "allowed_axioms" | "timeout_ms">> & Lean4Config {
  const c = job.config as Partial<Lean4Config>;
  if (!c.project_dir) throw new Error("lean4 plugin needs config.project_dir");
  return { project_dir: c.project_dir, allowed_axioms: c.allowed_axioms ?? DEFAULT_AXIOMS, timeout_ms: c.timeout_ms ?? 600_000, build_targets: c.build_targets };
}

function moduleName(projectDir: string, file: string): string {
  const rel = path.relative(projectDir, file).replace(/\.lean$/, "");
  return rel.split(path.sep).join(".");
}

export const lean4Plugin: VerificationPlugin = {
  name: "lean4",
  version: "0.1.0",

  capabilities(): Capabilities {
    return {
      languages: ["lean4"],
      methods: ["proof"],
      properties: ["invariant", "theorem"],
      requires: { tools: [{ name: "lean", hint: "install elan: https://github.com/leanprover/elan" }, { name: "lake" }] },
    };
  },

  guidance() {
    return { docs: ["guidance/lean4.md"], templates: [] };
  },

  async detect(root: string): Promise<DetectResult> {
    const lean = await exec("lean", ["--version"], { cwd: root }).catch(() => null);
    const lake = await exec("lake", ["--version"], { cwd: root }).catch(() => null);
    if (!lean || lean.code !== 0) return { available: false, versions: {}, reason: "lean not found on PATH (or ~/.elan/bin)" };
    if (!lake || lake.code !== 0) return { available: false, versions: {}, reason: "lake not found" };
    return { available: true, versions: { lean: lean.stdout.trim(), lake: lake.stdout.trim().split("\n")[0] ?? "" } };
  },

  async inputs(job: VerifyJob): Promise<{ targets: InputFile[]; files: InputFile[] }> {
    const c = cfg(job);
    const projectDir = path.join(job.root, c.project_dir);
    const targetsAbs = job.targets.map((t) => path.resolve(job.root, t));
    // `lean --src-deps` prints only direct imports: walk it recursively until closed.
    const seen = new Map<string, "target" | "import">();
    const queue = [...targetsAbs];
    for (const t of targetsAbs) seen.set(t, "target");
    while (queue.length) {
      const file = queue.shift()!;
      const r = await exec("lake", ["env", "lean", "--src-deps", path.relative(projectDir, file)], { cwd: projectDir, timeoutMs: 60_000 });
      if (r.code !== 0) throw new Error(`lean --src-deps failed for ${file}: ${r.stderr || r.stdout}`);
      for (const line of r.stdout.split("\n")) {
        const p = line.trim();
        if (!p) continue;
        const abs = path.resolve(projectDir, p);
        if (!abs.startsWith(projectDir + path.sep)) continue; // toolchain sources are covered by lean-toolchain
        if (abs.includes(`${path.sep}.lake${path.sep}`)) continue;
        if (!seen.has(abs)) {
          seen.set(abs, "import");
          queue.push(abs);
        }
      }
    }
    const targets: InputFile[] = [];
    const files: InputFile[] = [];
    for (const [abs, role] of [...seen.entries()].sort()) {
      const entry = { path: path.relative(job.root, abs), sha256: await sha256File(abs), role };
      if (role === "target") targets.push(entry);
      else files.push(entry);
    }
    for (const [name, role] of [
      ["lean-toolchain", "toolchain"],
      ["lakefile.toml", "lake_config"],
      ["lakefile.lean", "lake_config"],
      ["lake-manifest.json", "dependency_lock"],
    ] as const) {
      const abs = path.join(projectDir, name);
      if (fs.existsSync(abs)) files.push({ path: path.relative(job.root, abs), sha256: await sha256File(abs), role });
    }
    return { targets, files };
  },

  async run(job: VerifyJob, ctx: PluginContext): Promise<RawOutput> {
    const c = cfg(job);
    const projectDir = path.join(job.root, c.project_dir);
    const versions = await this.detect(job.root);
    const raw: Raw = { tool_versions: versions.versions, build: { code: null, stdout: "", stderr: "", timedOut: false }, check: null, scan_hits: [] };
    const inputs = await this.inputs(job);

    // 1. source scan for escape hatches
    for (const f of [...inputs.targets, ...inputs.files]) {
      if (!f.path.endsWith(".lean")) continue;
      const text = fs.readFileSync(path.join(job.root, f.path), "utf8");
      text.split("\n").forEach((line, i) => {
        if (FORBIDDEN.test(line) && !/^\s*--/.test(line)) raw.scan_hits.push({ path: f.path, line: i + 1, text: line.trim() });
      });
    }

    // 2. build
    ctx.onProgress("lake build");
    const modules = c.build_targets ?? job.targets.map((t) => moduleName(projectDir, path.resolve(job.root, t)));
    const build = await exec("lake", ["build", ...modules], { cwd: projectDir, signal: ctx.signal, timeoutMs: c.timeout_ms, onLine: (_s, l) => ctx.onProgress(l) });
    raw.build = { code: build.code, stdout: build.stdout, stderr: build.stderr, timedOut: build.timedOut };
    const buildLog = path.join(ctx.artifactDir, "lake-build.log");
    fs.writeFileSync(buildLog, `${build.stdout}\n${build.stderr}`);
    const artifacts: RawOutput["artifacts"] = [{ kind: "log", path: buildLog }];

    // 3. statements and axioms of every claimed theorem
    if (build.code === 0) {
      ctx.onProgress("checking theorem statements and axioms");
      const lines = [...modules.map((m) => `import ${m}`), "set_option pp.proofs false"];
      for (const cl of job.claims) {
        lines.push(`#check @${cl.native_ref}`);
        lines.push(`#print axioms ${cl.native_ref}`);
      }
      const checkFile = path.join(projectDir, ".lake", `prepare-dev-check-${job.run_id}.lean`);
      fs.mkdirSync(path.dirname(checkFile), { recursive: true });
      fs.writeFileSync(checkFile, `${lines.join("\n")}\n`);
      try {
        const check = await exec("lake", ["env", "lean", "--json", checkFile], { cwd: projectDir, signal: ctx.signal, timeoutMs: c.timeout_ms });
        const messages: LeanMessage[] = [];
        for (const line of check.stdout.split("\n")) {
          if (!line.trim()) continue;
          try {
            messages.push(JSON.parse(line) as LeanMessage);
          } catch {
            // non-JSON noise
          }
        }
        raw.check = { code: check.code, messages, stderr: check.stderr };
        const checkLog = path.join(ctx.artifactDir, "lean-check.jsonl");
        fs.writeFileSync(checkLog, check.stdout);
        artifacts.push({ kind: "log", path: checkLog });
      } finally {
        fs.rmSync(checkFile, { force: true });
      }
    }
    return { data: raw, artifacts };
  },

  async parse(rawOut: RawOutput, job: VerifyJob, inputs: { targets: InputFile[]; files: InputFile[] }): Promise<RunRecordDraft> {
    const c = cfg(job);
    const raw = rawOut.data as Raw;
    const allowed = new Set(c.allowed_axioms);
    const buildText = `${raw.build.stdout}\n${raw.build.stderr}`;
    const sorryInBuild = /declaration uses 'sorry'/.test(buildText);
    const msgs = raw.check?.messages ?? [];
    const text = (m: LeanMessage): string => m.data ?? m.text ?? "";

    const claims: RunRecordDraft["claims"] = [];
    const notes: string[] = [];
    let overall: VerifyResult = "verified";
    const worsen = (r: VerifyResult): void => {
      const order: VerifyResult[] = ["verified", "unknown", "failed", "timeout", "tool_error", "unsupported"];
      if (order.indexOf(r) > order.indexOf(overall)) overall = r;
    };
    if (raw.build.timedOut) worsen("timeout");
    else if (raw.build.code !== 0) worsen("failed");
    if (raw.scan_hits.length || sorryInBuild) worsen("unknown");
    const axiomsSeen = new Set<string>();

    for (const cl of job.claims) {
      let statement: string | null = null;
      let axioms: string[] | null = null;
      let result: VerifyResult = raw.build.code === 0 ? "verified" : raw.build.timedOut ? "timeout" : "failed";
      const short = cl.native_ref;
      for (const m of msgs) {
        const t = text(m);
        if (m.severity === "error" && t.includes(short)) {
          result = "failed";
          notes.push(`${short}: ${t.split("\n")[0]}`);
        }
        if (m.severity === "information" && t.startsWith(`@${short} :`)) statement = t.replace(/^@\S+\s*:\s*/, "").trim();
        else if (m.severity === "information" && t.startsWith(`${short} :`)) statement = t.replace(/^\S+\s*:\s*/, "").trim();
        if (t.startsWith(`'${short}' depends on axioms:`)) {
          axioms = t
            .replace(/^'[^']+' depends on axioms:\s*/, "")
            .replace(/^\[|\]$/g, "")
            .split(",")
            .map((s) => s.trim())
            .filter(Boolean);
        } else if (t.startsWith(`'${short}' does not depend on any axioms`)) {
          axioms = [];
        }
      }
      if (raw.build.code === 0 && (statement === null || axioms === null)) {
        result = "unknown";
        notes.push(`${short}: statement or axioms not found in lean output`);
      }
      if (axioms) {
        for (const a of axioms) axiomsSeen.add(a);
        const extra = axioms.filter((a) => !allowed.has(a));
        if (extra.length) {
          result = "unknown";
          notes.push(`${short}: depends on non-allowed axioms ${extra.join(", ")}`);
        }
      }
      if (raw.scan_hits.length || sorryInBuild) result = result === "failed" ? "failed" : "unknown";
      worsen(result);
      claims.push({ spec: cl.spec_ref, claim: cl.claim, requirement: cl.requirement_ref, native: { ref: cl.native_ref, statement }, result });
    }
    if (raw.scan_hits.length) notes.push(`escape hatches found: ${raw.scan_hits.map((h) => `${h.path}:${h.line}`).join(", ")}`);
    const summary = [
      `${claims.filter((x) => x.result === "verified").length}/${claims.length} claims verified by lake build (${raw.tool_versions.lean ?? "lean"})`,
      axiomsSeen.size ? `axioms: ${[...axiomsSeen].sort().join(", ")}` : "",
      ...notes,
    ]
      .filter(Boolean)
      .join(". ");
    return {
      tool: { name: "lean", version: raw.tool_versions.lean ?? "unknown", extra: { lake: raw.tool_versions.lake ?? "" } },
      method: "proof",
      claims,
      targets: inputs.targets,
      inputs: { files: inputs.files, config: { project_dir: c.project_dir, allowed_axioms: c.allowed_axioms } },
      result: overall,
      assumptions: [`axioms: ${[...axiomsSeen].sort().join(", ") || "none"}`, "model assumptions as stated in the spec frontmatter"],
      scope: { bounded: false, bounds: null, excluded: [], covered: claims.map((x) => x.native.ref) },
      artifacts: rawOut.artifacts,
      summary,
    };
  },
};

export default lean4Plugin;
