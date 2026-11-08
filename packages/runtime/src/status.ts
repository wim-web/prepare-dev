import type { PendingAnswer, Questions } from "./questions.js";
import type { JobState, Runs } from "./runs.js";
import type { Store } from "./store.js";
import type { Trace, TraceDecision, TraceRun } from "./trace.js";

export interface StatusView {
  root: string;
  pending: PendingAnswer[];
  inflight: Array<{ txid: string; answer_id: string; decision_id: string }>;
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
    const inflight = this.store.inflightIntents().map((i) => ({ txid: i.txid, answer_id: i.answer_id, decision_id: i.decision_id }));
    const decisions = this.trace.publicDecisions().map((d) => this.trace.decisionView(d));
    const stale_decisions = decisions.filter((d) => !d.reviewed_current);
    const runs = this.runs.list();
    const runViews = runs.map((r) => ({
      ...this.trace.runView(r, r.claims[0]?.claim ?? ""),
      claims: r.claims.map((c) => `${c.spec.id}/${c.claim}`),
    }));
    return {
      root: this.store.root,
      pending,
      inflight,
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
