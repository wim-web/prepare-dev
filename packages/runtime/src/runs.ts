import os from "node:os";
import path from "node:path";
import {
  type ClaimRequest,
  type InputFile,
  type RunRecord,
  type SpecFrontmatter,
  type SubjectRef,
  type VerificationPlugin,
  type VerifyJob,
  nowIso,
} from "@prepare-dev/core";
import { RuntimeError } from "./errors.js";
import type { Store } from "./store.js";

export interface StartRunInput {
  plugin: string;
  claims: ClaimRequest[];
  targets?: string[];
  config?: Record<string, unknown>;
}

export interface JobState {
  run_id: string;
  plugin: string;
  status: "running" | "done" | "failed";
  started_at: string;
  finished_at: string | null;
  progress: string[];
  result: RunRecord["result"] | null;
  error: string | null;
}

export interface Mismatch {
  kind: "claim_spec" | "claim_requirement" | "target" | "input";
  ref: string;
  expected: string;
  current: string | null;
}

export interface Applicability {
  applicable: boolean;
  mismatches: Mismatch[];
}

export class Runs {
  private readonly plugins = new Map<string, VerificationPlugin>();
  private readonly jobs = new Map<string, JobState>();
  private readonly promises = new Map<string, Promise<void>>();

  constructor(
    private readonly store: Store,
    plugins: VerificationPlugin[],
  ) {
    for (const p of plugins) this.plugins.set(p.name, p);
  }

  pluginNames(): string[] {
    return [...this.plugins.keys()];
  }

  plugin(name: string): VerificationPlugin {
    const p = this.plugins.get(name);
    if (!p) throw new RuntimeError("not_found", `plugin ${name} is not registered`, { available: this.pluginNames() });
    return p;
  }

  list(): RunRecord[] {
    return this.store.listRunIds().map((id) => this.store.readRun(id));
  }

  get(id: string): RunRecord {
    return this.store.readRun(id);
  }

  job(id: string): JobState | null {
    return this.jobs.get(id) ?? null;
  }

  jobsList(): JobState[] {
    return [...this.jobs.values()];
  }

  /** Resolve claim requests against the current spec/requirement files. */
  private resolveClaims(pluginName: string, claims: ClaimRequest[]): VerifyJob["claims"] {
    if (!claims.length) throw new RuntimeError("invalid", "verify_run needs at least one claim");
    return claims.map((c) => {
      const spec = this.store.readRecord(c.spec);
      if (spec.kind !== "spec") throw new RuntimeError("invalid", `${c.spec} is not a spec`);
      const fm = spec.frontmatter as unknown as SpecFrontmatter;
      const claim = fm.claims.find((x) => x.id === c.claim);
      if (!claim) throw new RuntimeError("not_found", `claim ${c.claim} not found in ${c.spec}`);
      const native = claim.native.find((n) => n.plugin === pluginName);
      if (!native) throw new RuntimeError("invalid", `claim ${c.spec}/${c.claim} has no native reference for plugin ${pluginName}`);
      let requirement_ref: SubjectRef | null = null;
      if (claim.requirement) {
        try {
          const r = this.store.readRecord(claim.requirement);
          requirement_ref = { id: r.id, rev: r.rev, sha256: r.sha256 };
        } catch {
          requirement_ref = null;
        }
      }
      return {
        spec: c.spec,
        claim: c.claim,
        spec_ref: { id: spec.id, rev: spec.rev, sha256: spec.sha256 },
        requirement_ref,
        native_ref: native.ref,
      };
    });
  }

  start(input: StartRunInput): JobState {
    this.store.assertAlive();
    const plugin = this.plugin(input.plugin);
    const claims = this.resolveClaims(plugin.name, input.claims);
    const run_id = this.store.allocateId("run");
    const job: VerifyJob = {
      run_id,
      root: this.store.root,
      plugin: plugin.name,
      claims,
      targets: input.targets ?? [],
      config: { ...(this.store.config.plugins[plugin.name] ?? {}), ...(input.config ?? {}) },
    };
    const state: JobState = {
      run_id,
      plugin: plugin.name,
      status: "running",
      started_at: nowIso(),
      finished_at: null,
      progress: [],
      result: null,
      error: null,
    };
    this.jobs.set(run_id, state);
    // reserve the id on disk immediately so a second start cannot reuse it
    this.store.runArtifactDir(run_id);
    const p = this.execute(plugin, job, state).catch((e: unknown) => {
      state.status = "failed";
      state.error = e instanceof Error ? e.message : String(e);
      state.finished_at = nowIso();
    });
    this.promises.set(run_id, p);
    return state;
  }

  private async execute(plugin: VerificationPlugin, job: VerifyJob, state: JobState): Promise<void> {
    const started = Date.now();
    const artifactDir = this.store.runArtifactDir(job.run_id);
    const controller = new AbortController();
    const progress = (message: string): void => {
      state.progress.push(`${nowIso()} ${message}`);
      this.store.emitter.emit("job", { run_id: job.run_id, message });
    };
    let record: RunRecord;
    const base = {
      id: job.run_id,
      plugin: { name: plugin.name, version: plugin.version },
    };
    const env = (): RunRecord["environment"] => ({
      platform: `${os.platform()}-${os.arch()}`,
      started_at: new Date(started).toISOString(),
      finished_at: nowIso(),
      duration_ms: Date.now() - started,
    });
    try {
      const detected = await plugin.detect(job.root);
      if (!detected.available) {
        record = {
          ...base,
          tool: { name: plugin.name, version: "unknown", extra: {} },
          method: plugin.capabilities().methods[0] ?? "test",
          claims: job.claims.map((c) => ({
            spec: c.spec_ref,
            claim: c.claim,
            requirement: c.requirement_ref,
            native: { ref: c.native_ref, statement: null },
            result: "unsupported" as const,
          })),
          targets: [],
          inputs: { files: [], config: job.config },
          environment: env(),
          result: "unsupported",
          assumptions: [],
          scope: { bounded: false, bounds: null, excluded: [], covered: [] },
          artifacts: [],
          summary: `tool not available: ${detected.reason ?? "unknown"}`,
        };
      } else {
        progress("collecting inputs");
        const inputs = await plugin.inputs(job);
        progress("running");
        const raw = await plugin.run(job, { signal: controller.signal, artifactDir, onProgress: progress });
        progress("parsing");
        const draft = await plugin.parse(raw, job, inputs);
        record = { ...draft, ...base, environment: env() };
      }
    } catch (e) {
      record = {
        ...base,
        tool: { name: plugin.name, version: "unknown", extra: {} },
        method: plugin.capabilities().methods[0] ?? "test",
        claims: job.claims.map((c) => ({
          spec: c.spec_ref,
          claim: c.claim,
          requirement: c.requirement_ref,
          native: { ref: c.native_ref, statement: null },
          result: "tool_error" as const,
        })),
        targets: [],
        inputs: { files: [], config: job.config },
        environment: env(),
        result: "tool_error",
        assumptions: [],
        scope: { bounded: false, bounds: null, excluded: [], covered: [] },
        artifacts: [],
        summary: `plugin failed: ${e instanceof Error ? e.message : String(e)}`,
      };
    }
    // artifact paths must be relative to root
    record.artifacts = record.artifacts.map((a) => ({ ...a, path: path.isAbsolute(a.path) ? path.relative(this.store.root, a.path) : a.path }));
    this.store.writeRun(record);
    this.store.appendEvent<"run.record">({ type: "run.record", run_id: record.id, result: record.result });
    state.status = "done";
    state.result = record.result;
    state.finished_at = nowIso();
    this.store.emitter.emit("run", record);
  }

  async wait(run_id: string, timeoutMs: number): Promise<JobState & { record: RunRecord | null }> {
    const state = this.jobs.get(run_id);
    if (!state) {
      // not a job of this process: maybe a finished run on disk
      const record = this.store.readRun(run_id);
      return {
        run_id,
        plugin: record.plugin.name,
        status: "done",
        started_at: record.environment.started_at,
        finished_at: record.environment.finished_at,
        progress: [],
        result: record.result,
        error: null,
        record,
      };
    }
    const p = this.promises.get(run_id)!;
    await Promise.race([p, new Promise((r) => setTimeout(r, timeoutMs))]);
    let record: RunRecord | null = null;
    if (state.status === "done") record = this.store.readRun(run_id);
    return { ...state, record };
  }

  /** Does this run still apply to the current versions of everything it depended on? */
  applicability(run: RunRecord): Applicability {
    const mismatches: Mismatch[] = [];
    const check = (kind: Mismatch["kind"], ref: SubjectRef | null): void => {
      if (!ref) return;
      let current: { rev: number; sha256: string } | null = null;
      try {
        const r = this.store.readRecord(ref.id);
        current = { rev: r.rev, sha256: r.sha256 };
      } catch {
        current = null;
      }
      if (!current || current.rev !== ref.rev || current.sha256 !== ref.sha256) {
        mismatches.push({ kind, ref: ref.id, expected: `rev ${ref.rev} ${ref.sha256.slice(0, 12)}`, current: current ? `rev ${current.rev} ${current.sha256.slice(0, 12)}` : null });
      }
    };
    const seen = new Set<string>();
    for (const c of run.claims) {
      if (!seen.has(`s:${c.spec.id}`)) {
        seen.add(`s:${c.spec.id}`);
        check("claim_spec", c.spec);
      }
      if (c.requirement && !seen.has(`r:${c.requirement.id}`)) {
        seen.add(`r:${c.requirement.id}`);
        check("claim_requirement", c.requirement);
      }
    }
    const checkFile = (kind: Mismatch["kind"], f: InputFile): void => {
      const cur = this.store.fileVersion(f.path);
      if (cur !== f.sha256) mismatches.push({ kind, ref: f.path, expected: f.sha256.slice(0, 12), current: cur ? cur.slice(0, 12) : null });
    };
    for (const t of run.targets) checkFile("target", t);
    for (const f of run.inputs.files) checkFile("input", f);
    return { applicable: mismatches.length === 0, mismatches };
  }
}
