import { z } from "zod";

// ---------- shared ----------

export const SubjectRef = z.object({
  id: z.string(),
  rev: z.number().int().min(1),
  sha256: z.string(),
});
export type SubjectRef = z.infer<typeof SubjectRef>;

export const RecordKind = z.enum([
  "discovery",
  "requirement",
  "decision",
  "design",
  "spec",
  "review",
  "record",
  "proposal",
]);
export type RecordKind = z.infer<typeof RecordKind>;

export const RecordStatus = z.enum(["draft", "proposed", "accepted", "rejected", "superseded"]);
export type RecordStatus = z.infer<typeof RecordStatus>;

// ---------- markdown records ----------

export const ClaimNative = z.object({
  plugin: z.string(),
  ref: z.string(),
});

export const Claim = z.object({
  id: z.string(),
  statement: z.string(),
  requirement: z.string().optional(),
  native: z.array(ClaimNative).default([]),
});
export type Claim = z.infer<typeof Claim>;

export const BaseFrontmatter = z.looseObject({
  id: z.string(),
  kind: RecordKind,
  title: z.string(),
  status: RecordStatus.default("draft"),
  rev: z.number().int().min(1).default(1),
  created: z.string(),
  updated: z.string(),
  links: z.record(z.string(), z.array(z.string())).default({}),
});
export type BaseFrontmatter = z.infer<typeof BaseFrontmatter>;

export const DecisionFrontmatter = BaseFrontmatter.extend({
  kind: z.literal("decision"),
  question: z.string().optional(),
  answer: z.string().optional(),
  txid: z.string().optional(),
  chosen: z.string().optional(),
  alternatives: z.array(z.string()).default([]),
  reviewed: z.array(SubjectRef).default([]),
});
export type DecisionFrontmatter = z.infer<typeof DecisionFrontmatter>;

export const SpecFrontmatter = BaseFrontmatter.extend({
  kind: z.literal("spec"),
  claims: z.array(Claim).default([]),
  assumptions: z.array(z.string()).default([]),
  out_of_scope: z.array(z.string()).default([]),
});
export type SpecFrontmatter = z.infer<typeof SpecFrontmatter>;

export const CoverageState = z.enum(["decided", "hypothesis", "unverified", "out_of_scope"]);
export type CoverageState = z.infer<typeof CoverageState>;

export const RequirementFrontmatter = BaseFrontmatter.extend({
  kind: z.literal("requirement"),
  priority: z.enum(["must", "should", "could", "wont"]).default("must"),
  acceptance: z.array(z.string()).default([]),
  coverage: z.record(z.string(), CoverageState).default({}),
});
export type RequirementFrontmatter = z.infer<typeof RequirementFrontmatter>;

export const DiscoveryFrontmatter = BaseFrontmatter.extend({
  kind: z.literal("discovery"),
  problem: z.string().default(""),
  stakeholders: z.array(z.string()).default([]),
  hypotheses: z.array(z.object({ id: z.string(), statement: z.string(), evidence: z.string().default(""), state: CoverageState.default("hypothesis") })).default([]),
  non_goals: z.array(z.string()).default([]),
  coverage: z.record(z.string(), CoverageState).default({}),
});
export type DiscoveryFrontmatter = z.infer<typeof DiscoveryFrontmatter>;

export const DesignFrontmatter = BaseFrontmatter.extend({
  kind: z.literal("design"),
  alternatives: z.array(z.object({ key: z.string(), title: z.string(), status: z.enum(["chosen", "rejected", "open"]).default("open"), reason: z.string().default("") })).default([]),
  chosen: z.string().nullable().default(null),
  tradeoffs: z.array(z.string()).default([]),
  /** New decisions this design introduces that no DIS/REQ/DEC covers (architecture, external interface, dependency). Must be reported, not hidden. */
  new_decisions: z.array(z.string()).default([]),
  coverage: z.record(z.string(), CoverageState).default({}),
});
export type DesignFrontmatter = z.infer<typeof DesignFrontmatter>;

export const FindingSeverity = z.enum(["blocker", "major", "nit"]);
export const FindingProposed = z.enum(["fix", "contest", "escalate", "decline"]);
export const FindingActual = z.enum(["unaddressed", "fixed", "contested_accepted", "escalated", "declined", "accepted_risk"]);

export const Finding = z.object({
  id: z.string(),
  severity: FindingSeverity,
  scope: z.string(),
  summary: z.string(),
  rationale: z.string().default(""),
  /** What the reviewing/resolving agent proposed. A proposal is not a completion. */
  proposed: FindingProposed,
  proposed_note: z.string().default(""),
  /** What actually happened. Only a human decision (DEC) can turn a finding into accepted_risk or contested_accepted. */
  actual: FindingActual.default("unaddressed"),
  actual_ref: z.string().nullable().default(null),
});
export type Finding = z.infer<typeof Finding>;

export const ReviewStage = z.enum(["zoom_out", "comprehension", "design_critique", "implementation_readiness", "custom"]);

export const ReviewFrontmatter = BaseFrontmatter.extend({
  kind: z.literal("review"),
  stage: ReviewStage,
  /** Records under review. */
  subjects: z.array(SubjectRef),
  /** Everything the reviewer was given (subjects plus references), pinned. */
  inputs: z.array(SubjectRef).default([]),
  reviewer: z.object({
    persona: z.string(),
    persona_version: z.string(),
    host: z.string().default("unknown"),
    context: z.enum(["fresh", "shared"]).default("fresh"),
  }),
  /** Comprehension stage: the reviewer's own-words restatement; a wrong paraphrase is a finding. */
  paraphrase: z.string().nullable().default(null),
  findings: z.array(Finding).default([]),
});
export type ReviewFrontmatter = z.infer<typeof ReviewFrontmatter>;

// ---------- questions and answers ----------

export const QuestionKind = z.enum(["single_choice", "multi_choice", "free_text", "review"]);
export type QuestionKind = z.infer<typeof QuestionKind>;

export const QuestionOption = z.object({
  key: z.string(),
  label: z.string(),
  summary: z.string().optional(),
  recommended: z.boolean().optional(),
  reason: z.string().optional(),
});
export type QuestionOption = z.infer<typeof QuestionOption>;

export const Answer = z.object({
  answer_id: z.string(),
  answered_at: z.string(),
  subjects_at_answer: z.array(SubjectRef),
  stale: z.boolean(),
  choice: z.array(z.string()).nullable(),
  text: z.string().nullable(),
  received_at: z.string().nullable(),
  /** Provisional until events.jsonl has apply.commit for the same txid. */
  applied_as: z.object({ decision_id: z.string(), txid: z.string() }).nullable(),
  closed: z.object({ at: z.string(), reason: z.string() }).nullable(),
});
export type Answer = z.infer<typeof Answer>;

export const QuestionState = z.enum(["open", "answered", "received", "applied", "cancelled", "closed"]);

export const Question = z.object({
  id: z.string(),
  rev: z.number().int().min(1),
  state: QuestionState,
  kind: QuestionKind,
  created_at: z.string(),
  created_by: z.object({ host: z.string().optional(), session: z.string().optional() }).default({}),
  subjects: z.array(SubjectRef),
  prompt: z.string(),
  options: z.array(QuestionOption).default([]),
  allow_free_text: z.boolean().default(true),
  /** Legacy display data is preserved on old questions, but never rendered. */
  ui: z.unknown().optional(),
  answers: z.array(Answer).default([]),
  /** Set when the question was withdrawn before any answer (e.g. replaced by a question at newer subject versions). */
  cancelled: z.object({ at: z.string(), reason: z.string(), replaced_by: z.string().nullable().default(null) }).nullable().default(null),
});
export type Question = z.infer<typeof Question>;

// ---------- verification runs ----------

export const VerifyMethod = z.enum([
  "proof",
  "model_check",
  "bounded_check",
  "simulation",
  "test",
  "static_analysis",
]);
export type VerifyMethod = z.infer<typeof VerifyMethod>;

export const VerifyResult = z.enum(["verified", "failed", "unknown", "timeout", "tool_error", "unsupported"]);
export type VerifyResult = z.infer<typeof VerifyResult>;

export const InputFile = z.object({
  path: z.string(),
  sha256: z.string(),
  role: z.string(),
});
export type InputFile = z.infer<typeof InputFile>;

export const RunClaim = z.object({
  spec: SubjectRef,
  claim: z.string(),
  requirement: SubjectRef.nullable(),
  native: z.object({ ref: z.string(), statement: z.string().nullable() }),
  result: VerifyResult,
});
export type RunClaim = z.infer<typeof RunClaim>;

export const Artifact = z.object({
  kind: z.enum(["log", "counterexample", "generated_test", "report", "other"]),
  path: z.string(),
});

export const RunRecord = z.object({
  id: z.string(),
  plugin: z.object({ name: z.string(), version: z.string() }),
  tool: z.object({ name: z.string(), version: z.string(), extra: z.record(z.string(), z.string()).default({}) }),
  method: VerifyMethod,
  claims: z.array(RunClaim),
  targets: z.array(InputFile),
  inputs: z.object({
    files: z.array(InputFile),
    config: z.record(z.string(), z.unknown()).default({}),
  }),
  environment: z.object({
    platform: z.string(),
    started_at: z.string(),
    finished_at: z.string(),
    duration_ms: z.number(),
  }),
  result: VerifyResult,
  assumptions: z.array(z.string()).default([]),
  scope: z.object({
    bounded: z.boolean(),
    bounds: z.record(z.string(), z.unknown()).nullable(),
    excluded: z.array(z.string()).default([]),
    covered: z.array(z.string()).default([]),
  }),
  artifacts: z.array(Artifact).default([]),
  summary: z.string(),
});
export type RunRecord = z.infer<typeof RunRecord>;

export const DecisionDraft = z.object({
  title: z.string(),
  body: z.string(),
  chosen: z.string().optional(),
  alternatives: z.array(z.string()).default([]),
});
export type DecisionDraft = z.infer<typeof DecisionDraft>;

// ---------- events (append-only journal) ----------

const base = { ts: z.string() };

export const Event = z.discriminatedUnion("type", [
  z.object({ ...base, type: z.literal("record.write"), id: z.string(), path: z.string(), rev: z.number().int(), sha256: z.string() }),
  z.object({ ...base, type: z.literal("record.discover"), id: z.string(), path: z.string(), rev: z.number().int(), sha256: z.string() }),
  z.object({ ...base, type: z.literal("record.remove"), id: z.string(), path: z.string(), reason: z.string() }),
  z.object({
    ...base,
    type: z.literal("external_change"),
    id: z.string(),
    path: z.string(),
    rev_from: z.number().int(),
    rev_to: z.number().int(),
    sha256_from: z.string(),
    sha256_to: z.string(),
  }),
  z.object({ ...base, type: z.literal("question.create"), question_id: z.string(), subjects: z.array(SubjectRef) }),
  z.object({ ...base, type: z.literal("answer.save"), question_id: z.string(), answer_id: z.string(), stale: z.boolean() }),
  z.object({ ...base, type: z.literal("answer.ack"), question_id: z.string(), answer_id: z.string() }),
  z.object({ ...base, type: z.literal("answer.close"), question_id: z.string(), answer_id: z.string(), reason: z.string() }),
  z.object({ ...base, type: z.literal("question.cancel"), question_id: z.string(), reason: z.string(), replaced_by: z.string().nullable() }),
  z.object({
    ...base,
    type: z.literal("apply.intent"),
    txid: z.string(),
    question_id: z.string(),
    answer_id: z.string(),
    decision_id: z.string(),
    subjects: z.array(SubjectRef),
    /** The decision draft, kept in the journal so a crash before stage b can be rolled forward. */
    decision: DecisionDraft,
  }),
  z.object({ ...base, type: z.literal("apply.commit"), txid: z.string() }),
  z.object({ ...base, type: z.literal("apply.abort"), txid: z.string(), reason: z.string() }),
  z.object({ ...base, type: z.literal("run.record"), run_id: z.string(), result: VerifyResult }),
]);
export type Event = z.infer<typeof Event>;
export type EventOf<T extends Event["type"]> = Extract<Event, { type: T }>;

// ---------- config ----------

export const ToolIdentity = z.object({ version: z.string(), build: z.string(), root: z.string(), mode: z.enum(["source", "package"]) });
export type ToolIdentity = z.infer<typeof ToolIdentity>;

export const ProjectConfig = z.object({
  records_dir: z.string().default("prepare"),
  wait: z.object({ default_timeout_ms: z.number().int().default(50_000) }).default({ default_timeout_ms: 50_000 }),
  plugins: z.record(z.string(), z.record(z.string(), z.unknown())).default({}),
});
export type ProjectConfig = z.infer<typeof ProjectConfig>;
