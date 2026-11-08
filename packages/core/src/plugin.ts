import type { InputFile, RunRecord, SubjectRef, VerifyMethod } from "./schemas.js";

export interface ClaimRequest {
  /** Spec record id, e.g. SPEC-0001 */
  spec: string;
  /** Claim id within the spec, e.g. C1 */
  claim: string;
}

export interface VerifyJob {
  run_id: string;
  /** Absolute project root (the directory that holds prepare/). */
  root: string;
  plugin: string;
  claims: Array<
    ClaimRequest & {
      spec_ref: SubjectRef;
      requirement_ref: SubjectRef | null;
      /** Native reference for this plugin, taken from the spec's claims[].native. */
      native_ref: string;
    }
  >;
  /** Plugin specific target descriptors (paths relative to root, module names, ...). */
  targets: string[];
  config: Record<string, unknown>;
}

export interface PluginContext {
  signal: AbortSignal;
  /** Directory for raw logs and artifacts of this run (relative paths returned in the record must be under root). */
  artifactDir: string;
  onProgress(message: string): void;
}

export interface RawOutput {
  /** Free-form; parse() interprets it. */
  data: unknown;
  artifacts: Array<{ kind: "log" | "counterexample" | "generated_test" | "report" | "other"; path: string }>;
}

export interface Capabilities {
  languages: string[];
  methods: VerifyMethod[];
  properties: string[];
  requires: { tools: Array<{ name: string; hint?: string }>; platforms?: string[] };
}

export interface DetectResult {
  available: boolean;
  versions: Record<string, string>;
  reason?: string;
}

/** What parse() must fill in; the runtime adds id, plugin, environment, and run bookkeeping. */
export type RunRecordDraft = Omit<RunRecord, "id" | "plugin" | "environment">;

export interface VerificationPlugin {
  name: string;
  version: string;
  capabilities(): Capabilities;
  guidance(): { docs: string[]; templates: string[] };
  detect(root: string): Promise<DetectResult>;
  /** Enumerate the input set (targets plus transitive dependencies, lockfiles, toolchain, config). */
  inputs(job: VerifyJob): Promise<{ targets: InputFile[]; files: InputFile[] }>;
  run(job: VerifyJob, ctx: PluginContext): Promise<RawOutput>;
  parse(raw: RawOutput, job: VerifyJob, inputs: { targets: InputFile[]; files: InputFile[] }): Promise<RunRecordDraft>;
}
