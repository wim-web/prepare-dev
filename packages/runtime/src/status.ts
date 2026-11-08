import type { PendingAnswer, Questions } from "./questions.js";
import type { JobState, Runs } from "./runs.js";
import type { Store } from "./store.js";
import type { ReviewFrontmatter } from "@prepare-dev/core";
import type { Trace, TraceDecision, TraceRun } from "./trace.js";

export interface OpenFinding {
  review_id: string;
  finding_id: string;
  severity: string;
  scope: string;
  summary: string;
  proposed: string;
  subjects: string[];
}

export interface WaitingQuestion {
  question_id: string;
  prompt: string;
  subjects: string[];
  created_at: string;
  /** The pinned subject versions are still the current ones (an answer would not be stale). */
  subjects_current: boolean;
}

export interface StatusView {
  root: string;
  pending: PendingAnswer[];
  /** Questions that are open and have no answer yet: the human still has to decide. */
  waiting_questions: WaitingQuestion[];
  inflight: Array<{ txid: string; answer_id: string; decision_id: string }>;
  recovery_pending: boolean;
  /** Review findings nobody has acted on yet (proposals do not count as handling). */
  open_findings: OpenFinding[];
  stale_decisions: TraceDecision[];
  runs_not_applicable: Array<TraceRun & { claims: string[] }>;
  runs_current: Array<TraceRun & { claims: string[] }>;
  jobs: JobState[];
  problems: Array<{ path: string; message: string }>;
  counts: { records: number; questions: number; runs: number; events: number };
}

export class Status {
  constructor(
    private readonly store: Store,
    private readonly questions: Questions,
    private readonly runs: Runs,
    private readonly trace: Trace,
  ) {}

  view(): StatusView {
    const pending = this.questions.pending();
    const waiting_questions: WaitingQuestion[] = this.questions
      .list()
      .filter((q) => q.state === "open" && q.answers.length === 0)
      .map((q) => ({
        question_id: q.id,
        prompt: q.prompt,
        subjects: q.subjects.map((x) => x.id),
        created_at: q.created_at,
        subjects_current: this.questions.checkSubjects(q.subjects).length === 0,
      }));
    const inflight = this.store.inflightIntents().map((i) => ({ txid: i.txid, answer_id: i.answer_id, decision_id: i.decision_id }));
    const decisions = this.trace.publicDecisions().map((d) => this.trace.decisionView(d));
    const stale_decisions = decisions.filter((d) => !d.reviewed_current);
    const open_findings: OpenFinding[] = [];
    for (const r of this.store.listRecords("review")) {
      const fm = r.frontmatter as unknown as ReviewFrontmatter;
      for (const f of fm.findings ?? []) {
        if (f.actual !== "unaddressed") continue;
        open_findings.push({
          review_id: r.id,
          finding_id: f.id,
          severity: f.severity,
          scope: f.scope,
          summary: f.summary,
          proposed: f.proposed,
          subjects: (fm.subjects ?? []).map((s) => s.id),
        });
      }
    }
    const runs = this.runs.list();
    const runViews = runs.map((r) => ({
      ...this.trace.runView(r, r.claims[0]?.claim ?? ""),
      claims: r.claims.map((c) => `${c.spec.id}/${c.claim}`),
    }));
    return {
      root: this.store.root,
      pending,
      waiting_questions,
      inflight,
      recovery_pending: this.questions.recoveryPending(),
      open_findings,
      stale_decisions,
      runs_not_applicable: runViews.filter((r) => !r.applicable),
      runs_current: runViews.filter((r) => r.applicable),
      jobs: this.runs.jobsList(),
      problems: this.store.problems,
      counts: {
        records: this.store.indexEntries().length,
        questions: this.store.listQuestionIds().length,
        runs: runs.length,
        events: this.store.allEvents().length,
      },
    };
  }
}
