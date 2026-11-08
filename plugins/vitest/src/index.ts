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

export interface VitestConfig {
  /** Directory holding the vitest project (package root), relative to the project root. Defaults to ".". */
  project_dir?: string;
  /** Implementation files (relative to root) the tests exercise; recorded as inputs. */
  impl_files?: string[];
  /** Extra environment variables for the vitest process (e.g. which trace file a conformance test replays). */
  env?: Record<string, string>;
  /** Optional JSON file (relative to root) written by the tests; its top-level numbers/strings are merged into scope.bounds. */
  bounds_from?: string;
  timeout_ms?: number;
}

interface VitestJson {
  numTotalTests: number;
  numPassedTests: number;
  numFailedTests: number;
  success: boolean;
  testResults: Array<{
    name: string;
    status: string;
    assertionResults: Array<{ fullName: string; title: string; status: string; failureMessages: string[] }>;
  }>;
}

interface Raw {
  version: string;
  code: number | null;
  timedOut: boolean;
  stderr: string;
  report: VitestJson | null;
}

function cfg(job: VerifyJob): Required<Omit<VitestConfig, "bounds_from">> & { bounds_from: string | null } {
  const c = job.config as VitestConfig;
  return { project_dir: c.project_dir ?? ".", impl_files: c.impl_files ?? [], env: c.env ?? {}, bounds_from: c.bounds_from ?? null, timeout_ms: c.timeout_ms ?? 600_000 };
}

/** name -> entry file for packages inside the workspace (packages/*, plugins/*, apps/*). */
function workspacePackages(projectDir: string): Map<string, string> {
  const out = new Map<string, string>();
  for (const group of ["packages", "plugins", "apps"]) {
    const dir = path.join(projectDir, group);
    if (!fs.existsSync(dir)) continue;
    for (const name of fs.readdirSync(dir)) {
      const pj = path.join(dir, name, "package.json");
      if (!fs.existsSync(pj)) continue;
      try {
        const meta = JSON.parse(fs.readFileSync(pj, "utf8")) as { name?: string; main?: string; exports?: unknown };
        const entry = typeof meta.main === "string" ? meta.main : typeof meta.exports === "object" && meta.exports && typeof (meta.exports as Record<string, unknown>)["."] === "string" ? ((meta.exports as Record<string, string>)["."] as string) : null;
        if (meta.name && entry) out.set(meta.name, path.resolve(dir, name, entry));
      } catch {
        // ignore malformed package.json
      }
    }
  }
  return out;
}

const IMPORT_RE = /(?:^|\n)\s*(?:import|export)\b[^'"\n]*?\bfrom\s*["']([^"']+)["']|\bimport\s*\(\s*["']([^"']+)["']\s*\)|(?:^|\n)\s*import\s*["']([^"']+)["']/g;

function resolveTs(base: string): string | null {
  const candidates = [base, base.replace(/\.js$/, ".ts"), base.replace(/\.js$/, ".tsx"), `${base}.ts`, `${base}.tsx`, path.join(base, "index.ts")];
  for (const c of candidates) if (fs.existsSync(c) && fs.statSync(c).isFile()) return c;
  return null;
}

function importsOf(file: string, workspace: Map<string, string>): string[] {
  if (!/\.(ts|tsx|mts|js|mjs)$/.test(file)) return [];
  const text = fs.readFileSync(file, "utf8");
  const out: string[] = [];
  for (const m of text.matchAll(IMPORT_RE)) {
    const spec = m[1] ?? m[2] ?? m[3];
    if (!spec) continue;
    let abs: string | null = null;
    if (spec.startsWith(".")) abs = resolveTs(path.resolve(path.dirname(file), spec));
    else if (workspace.has(spec)) abs = workspace.get(spec)!;
    if (abs && !abs.includes(`${path.sep}node_modules${path.sep}`)) out.push(abs);
  }
  return out;
}

function vitestBin(dir: string): string | null {
  const candidates = [path.join(dir, "node_modules", ".bin", "vitest")];
  for (const c of candidates) if (fs.existsSync(c)) return c;
  return null;
}

export const vitestPlugin: VerificationPlugin = {
  name: "vitest",
  version: "0.1.0",

  capabilities(): Capabilities {
    return {
      languages: ["typescript", "javascript"],
      methods: ["test"],
      properties: ["example", "property", "conformance"],
      requires: { tools: [{ name: "vitest", hint: "pnpm add -D vitest" }] },
    };
  },

  guidance() {
    return { docs: ["guidance/vitest.md"], templates: [] };
  },

  async detect(root: string): Promise<DetectResult> {
    const bin = vitestBin(root);
    if (!bin) return { available: false, versions: {}, reason: "node_modules/.bin/vitest not found" };
    const r = await exec(bin, ["--version"], { cwd: root });
    return { available: r.code === 0, versions: { vitest: r.stdout.trim() }, reason: r.code === 0 ? undefined : r.stderr };
  },

  async inputs(job: VerifyJob): Promise<{ targets: InputFile[]; files: InputFile[] }> {
    const c = cfg(job);
    const projectDir = path.join(job.root, c.project_dir);
    const targets: InputFile[] = [];
    for (const t of job.targets) targets.push({ path: t, sha256: await sha256File(path.join(job.root, t)), role: "target" });
    const files: InputFile[] = [];
    const seen = new Set<string>(job.targets.map((t) => path.resolve(job.root, t)));
    // Transitive import closure of the test files (relative imports and workspace packages),
    // so that changing the implementation under test invalidates the evidence.
    const workspace = workspacePackages(projectDir);
    const queue = [...seen];
    while (queue.length) {
      const file = queue.shift()!;
      for (const dep of importsOf(file, workspace)) {
        if (seen.has(dep)) continue;
        seen.add(dep);
        queue.push(dep);
        files.push({ path: path.relative(job.root, dep), sha256: await sha256File(dep), role: "import" });
      }
    }
    for (const f of c.impl_files) {
      const abs = path.resolve(job.root, f);
      if (seen.has(abs)) continue;
      seen.add(abs);
      files.push({ path: f, sha256: await sha256File(abs), role: "implementation" });
    }
    for (const name of ["vitest.config.ts", "vitest.config.js", "vitest.config.mts"]) {
      const abs = path.join(job.root, c.project_dir, name);
      if (fs.existsSync(abs)) files.push({ path: path.relative(job.root, abs), sha256: await sha256File(abs), role: "config" });
    }
    return { targets, files };
  },

  async run(job: VerifyJob, ctx: PluginContext): Promise<RawOutput> {
    const c = cfg(job);
    const projectDir = path.join(job.root, c.project_dir);
    const bin = vitestBin(projectDir) ?? vitestBin(job.root);
    if (!bin) throw new Error("vitest binary not found");
    const out = path.join(ctx.artifactDir, "vitest.json");
    const version = (await exec(bin, ["--version"], { cwd: projectDir })).stdout.trim();
    ctx.onProgress(`vitest run ${job.targets.join(" ")}`);
    const r = await exec(bin, ["run", "--reporter=json", `--outputFile=${out}`, ...job.targets.map((t) => path.relative(projectDir, path.join(job.root, t)))], {
      cwd: projectDir,
      signal: ctx.signal,
      timeoutMs: c.timeout_ms,
      env: { CI: "1", ...c.env },
    });
    const stderrLog = path.join(ctx.artifactDir, "vitest.stderr.log");
    fs.writeFileSync(stderrLog, `${r.stdout}\n${r.stderr}`);
    let report: VitestJson | null = null;
    if (fs.existsSync(out)) report = JSON.parse(fs.readFileSync(out, "utf8")) as VitestJson;
    const raw: Raw = { version, code: r.code, timedOut: r.timedOut, stderr: r.stderr, report };
    return { data: raw, artifacts: [{ kind: "report", path: out }, { kind: "log", path: stderrLog }] };
  },

  async parse(rawOut: RawOutput, job: VerifyJob, inputs: { targets: InputFile[]; files: InputFile[] }): Promise<RunRecordDraft> {
    const raw = rawOut.data as Raw;
    const c = cfg(job);
    const claims: RunRecordDraft["claims"] = [];
    const covered: string[] = [];
    let overall: VerifyResult = "verified";
    const worsen = (r: VerifyResult): void => {
      const order: VerifyResult[] = ["verified", "unknown", "failed", "timeout", "tool_error", "unsupported"];
      if (order.indexOf(r) > order.indexOf(overall)) overall = r;
    };
    if (raw.timedOut) worsen("timeout");
    if (!raw.report) worsen("tool_error");
    const all = (raw.report?.testResults ?? []).flatMap((f) => f.assertionResults.map((a) => ({ file: f.name, ...a })));
    for (const a of all) covered.push(`${path.relative(job.root, a.file)}::${a.fullName}`);
    const notes: string[] = [];
    for (const cl of job.claims) {
      // native ref: "<file suffix>::<name substring>"
      const [fileSuffix, name] = cl.native_ref.split("::");
      const matched = all.filter((a) => (!fileSuffix || a.file.endsWith(fileSuffix)) && (!name || a.fullName.includes(name)));
      let result: VerifyResult;
      if (raw.timedOut) result = "timeout";
      else if (!raw.report) result = "tool_error";
      else if (!matched.length) {
        result = "unknown";
        notes.push(`${cl.native_ref}: no matching test`);
      } else if (matched.every((m) => m.status === "passed")) result = "verified";
      else if (matched.some((m) => m.status === "failed")) {
        result = "failed";
        const failed = matched.filter((m) => m.status === "failed");
        const first = failed[0]?.failureMessages?.[0]?.split("\n")[0] ?? "";
        notes.push(`${cl.native_ref}: ${failed.length} failed${first ? ` (${first.slice(0, 160)})` : ""}`);
      } else result = "unknown";
      worsen(result);
      claims.push({
        spec: cl.spec_ref,
        claim: cl.claim,
        requirement: cl.requirement_ref,
        native: { ref: cl.native_ref, statement: matched.map((m) => m.fullName).join(" | ") || null },
        result,
      });
    }
    const rep = raw.report;
    const bounds: Record<string, unknown> = { tests: rep?.numTotalTests ?? 0 };
    if (c.bounds_from) {
      const abs = path.join(job.root, c.bounds_from);
      if (fs.existsSync(abs)) {
        try {
          const extra = JSON.parse(fs.readFileSync(abs, "utf8")) as Record<string, unknown>;
          for (const [k, v] of Object.entries(extra)) if (typeof v === "number" || typeof v === "string") bounds[k] = v;
        } catch {
          // ignore unreadable bounds file
        }
      }
    }
    const summary = [
      rep ? `${rep.numPassedTests}/${rep.numTotalTests} tests passed (vitest ${raw.version})` : `vitest produced no report (exit ${raw.code})`,
      ...notes,
    ].join(". ");
    return {
      tool: { name: "vitest", version: raw.version || "unknown", extra: {} },
      method: "test",
      claims,
      targets: inputs.targets,
      inputs: { files: inputs.files, config: { project_dir: c.project_dir, env: c.env } },
      result: overall,
      assumptions: ["results hold for the executed test cases only"],
      scope: { bounded: true, bounds, excluded: [], covered },
      artifacts: rawOut.artifacts,
      summary,
    };
  },
};

export default vitestPlugin;
