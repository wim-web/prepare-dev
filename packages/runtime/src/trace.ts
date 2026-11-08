import type { DecisionFrontmatter, RunRecord, SpecFrontmatter, SubjectRef } from "@prepare-dev/core";
import type { Questions } from "./questions.js";
import type { Applicability, Runs } from "./runs.js";
import type { RecordView, Store } from "./store.js";

export interface TraceRun {
  run_id: string;
  plugin: string;
  method: RunRecord["method"];
  result: RunRecord["result"];
  claim_result: RunRecord["result"];
  finished_at: string;
  applicable: boolean;
  mismatches: Applicability["mismatches"];
  bounded: boolean;
  summary: string;
}

export interface TraceClaim {
  id: string;
  statement: string;
  requirement: string | null;
  runs: TraceRun[];
  /** Best current evidence: a run that is applicable to the current versions, or null. */
  current: TraceRun | null;
}

export interface TraceSpec {
  id: string;
  title: string;
  rev: number;
  status: string;
  claims: TraceClaim[];
}

export interface TraceDecision {
  id: string;
  title: string;
  status: string;
  question: string | null;
  answer: string | null;
  committed: boolean;
  /** Reviewed versions still match the current files. */
  reviewed_current: boolean;
  reviewed: SubjectRef[];
}

export interface TraceQuestion {
  id: string;
  state: string;
  prompt: string;
  answers: Array<{ answer_id: string; stale: boolean; received: boolean; committed: boolean; closed: boolean; applied_as: string | null }>;
}

export interface TraceView {
  record: { id: string; kind: string; title: string; rev: number; sha256: string; status: string };
  questions: TraceQuestion[];
  decisions: TraceDecision[];
  specs: TraceSpec[];
}

export class Trace {
  constructor(
    private readonly store: Store,
    private readonly questions: Questions,
    private readonly runs: Runs,
  ) {}

  /** Decisions whose transaction is committed (or that were not created from an answer). */
  publicDecisions(): RecordView[] {
    return this.store.listRecords("decision").filter((d) => this.isPublic(d));
  }

  isPublic(d: RecordView): boolean {
    const txid = d.frontmatter.txid as string | undefined;
    return !txid || this.store.isCommitted(txid);
  }

  decisionView(d: RecordView): TraceDecision {
    const fm = d.frontmatter as unknown as DecisionFrontmatter;
    const reviewed = fm.reviewed ?? [];
    const mismatches = this.questions.checkSubjects(reviewed);
    return {
      id: d.id,
      title: fm.title,
      status: fm.status,
      question: fm.question ?? null,
      answer: fm.answer ?? null,
      committed: !fm.txid || this.store.isCommitted(fm.txid),
      reviewed_current: mismatches.length === 0,
      reviewed,
    };
  }

  runView(run: RunRecord, claimId: string): TraceRun {
    const app = this.runs.applicability(run);
    const claim = run.claims.find((c) => c.claim === claimId);
    return {
      run_id: run.id,
      plugin: run.plugin.name,
      method: run.method,
      result: run.result,
      claim_result: claim?.result ?? run.result,
      finished_at: run.environment.finished_at,
      applicable: app.applicable,
      mismatches: app.mismatches,
      bounded: run.scope.bounded,
      summary: run.summary,
    };
  }

  specView(spec: RecordView, allRuns: RunRecord[]): TraceSpec {
    const fm = spec.frontmatter as unknown as SpecFrontmatter;
    const claims: TraceClaim[] = fm.claims.map((c) => {
      const runs = allRuns
        .filter((r) => r.claims.some((rc) => rc.spec.id === spec.id && rc.claim === c.id))
        .map((r) => this.runView(r, c.id))
        .sort((a, b) => b.finished_at.localeCompare(a.finished_at));
      const current = runs.find((r) => r.applicable && r.claim_result === "verified") ?? runs.find((r) => r.applicable) ?? null;
      return { id: c.id, statement: c.statement, requirement: c.requirement ?? null, runs, current };
    });
    return { id: spec.id, title: fm.title, rev: spec.rev, status: fm.status, claims };
  }

  for(id: string): TraceView {
    const rec = this.store.readRecord(id);
    const allRuns = this.runs.list();
    const questions: TraceQuestion[] = this.questions
      .list()
      .filter((q) => q.subjects.some((s) => s.id === id))
      .map((q) => ({
        id: q.id,
        state: q.state,
        prompt: q.prompt,
        answers: q.answers.map((a) => ({
          answer_id: a.answer_id,
          stale: a.stale,
          received: a.received_at !== null,
          committed: this.questions.isCommitted(a.answer_id),
          closed: a.closed !== null,
          applied_as: this.questions.isCommitted(a.answer_id) ? (a.applied_as?.decision_id ?? null) : null,
        })),
      }));
    const decisions = this.publicDecisions()
      .filter((d) => {
        const fm = d.frontmatter as unknown as DecisionFrontmatter;
        const links = (fm.links ?? {}) as Record<string, string[]>;
        return (fm.reviewed ?? []).some((s) => s.id === id) || (links.rationale_for ?? []).includes(id) || d.id === id;
      })
      .map((d) => this.decisionView(d));
    let specs: TraceSpec[] = [];
    if (rec.kind === "spec") {
      specs = [this.specView(rec, allRuns)];
    } else if (rec.kind === "requirement") {
      specs = this.store
        .listRecords("spec")
        .filter((s) => {
          const fm = s.frontmatter as unknown as SpecFrontmatter;
          const links = (fm.links ?? {}) as Record<string, string[]>;
          return (links.specifies ?? []).includes(id) || fm.claims.some((c) => c.requirement === id);
        })
        .map((s) => this.specView(s, allRuns));
    }
    return {
      record: { id: rec.id, kind: rec.kind, title: rec.frontmatter.title, rev: rec.rev, sha256: rec.sha256, status: rec.frontmatter.status },
      questions,
      decisions,
      specs,
    };
  }
}
