import {
  type Answer,
  type DecisionDraft,
  type EventOf,
  type Question,
  type QuestionKind,
  type QuestionOption,
  type SubjectRef,
  answerId,
  nowIso,
  randomId,
} from "@prepare-dev/core";
import { RuntimeError, SimulatedCrash } from "./errors.js";
import type { Store } from "./store.js";

export type ApplyStage = "intent" | "decision" | "question" | "commit";

export interface ApplyHooks {
  /** Called after each persistence stage. Throwing SimulatedCrash leaves the store inflight. */
  afterStage?: (stage: ApplyStage) => void;
}

export interface CreateQuestionInput {
  kind: QuestionKind;
  prompt: string;
  subjects: string[];
  options?: QuestionOption[];
  allow_free_text?: boolean;
  ui?: unknown;
  created_by?: { host?: string; session?: string };
}

export interface AnswerInput {
  choice?: string[] | null;
  text?: string | null;
}

export interface PendingAnswer {
  question_id: string;
  answer_id: string;
  question_rev: number;
  kind: QuestionKind;
  prompt: string;
  subjects: SubjectRef[];
  answered_at: string;
  stale: boolean;
  received: boolean;
  choice: string[] | null;
  text: string | null;
}

export type ApplyResult = { status: "applied" | "already_applied"; decision_id: string; txid: string };

export interface RecoveryReport {
  recovered: Array<{ txid: string; answer_id: string; decision_id: string; outcome: "forward" | "abort"; reason?: string }>;
  retracted: string[];
}

export interface SubjectMismatch {
  id: string;
  expected: SubjectRef;
  current: { rev: number; sha256: string } | null;
}

export class Questions {
  constructor(private readonly store: Store) {}

  // ---------- read ----------

  list(): Question[] {
    return this.store.listQuestionIds().map((id) => this.store.readQuestion(id));
  }

  read(id: string): Question {
    return this.store.readQuestion(id);
  }

  isCommitted(answerIdValue: string): boolean {
    return this.store.committedIntentFor(answerIdValue) !== null;
  }

  private isUnprocessed(a: Answer): boolean {
    return !a.closed && !this.isCommitted(a.answer_id);
  }

  /** Unprocessed answers across all questions: neither committed nor closed (ack and stale do not matter). */
  pending(): PendingAnswer[] {
    const out: PendingAnswer[] = [];
    for (const q of this.list()) {
      for (const a of q.answers) {
        if (!this.isUnprocessed(a)) continue;
        out.push({
          question_id: q.id,
          answer_id: a.answer_id,
          question_rev: q.rev,
          kind: q.kind,
          prompt: q.prompt,
          subjects: q.subjects,
          answered_at: a.answered_at,
          stale: a.stale,
          received: a.received_at !== null,
          choice: a.choice,
          text: a.text,
        });
      }
    }
    out.sort((x, y) => x.answered_at.localeCompare(y.answered_at) || x.answer_id.localeCompare(y.answer_id));
    return out;
  }

  pendingFor(questionId: string): Answer[] {
    const q = this.read(questionId);
    return q.answers.filter((a) => this.isUnprocessed(a));
  }

  // ---------- write ----------

  private deriveState(q: Question): Question["state"] {
    if (q.answers.some((a) => this.isCommitted(a.answer_id))) return "applied";
    if (q.answers.length > 0 && q.answers.every((a) => a.closed)) return "closed";
    if (q.answers.some((a) => a.received_at)) return "received";
    if (q.answers.length > 0) return "answered";
    return q.state === "cancelled" ? "cancelled" : "open";
  }

  private write(q: Question): void {
    q.state = this.deriveState(q);
    this.store.writeQuestion(q);
    this.store.emitter.emit("question", q);
  }

  snapshot(ids: string[]): SubjectRef[] {
    return ids.map((id) => {
      const r = this.store.readRecord(id);
      return { id, rev: r.rev, sha256: r.sha256 };
    });
  }

  /** Compare a snapshot with the actual files now (rev and sha256). */
  checkSubjects(subjects: SubjectRef[]): SubjectMismatch[] {
    const out: SubjectMismatch[] = [];
    for (const s of subjects) {
      let current: { rev: number; sha256: string } | null = null;
      try {
        const r = this.store.readRecord(s.id);
        current = { rev: r.rev, sha256: r.sha256 };
      } catch {
        current = null;
      }
      if (!current || current.rev !== s.rev || current.sha256 !== s.sha256) out.push({ id: s.id, expected: s, current });
    }
    return out;
  }

  create(input: CreateQuestionInput): Question {
    this.store.assertAlive();
    this.assertQuiescent();
    if (!input.subjects.length) throw new RuntimeError("invalid", "a question needs at least one subject");
    const subjects = this.snapshot(input.subjects);
    const id = this.store.allocateId("question");
    const q: Question = {
      id,
      rev: 1,
      state: "open",
      kind: input.kind,
      created_at: nowIso(),
      created_by: input.created_by ?? {},
      subjects,
      prompt: input.prompt,
      options: input.options ?? [],
      allow_free_text: input.allow_free_text ?? true,
      ui: { spec: input.ui ?? null },
      answers: [],
    };
    this.write(q);
    this.store.appendEvent<"question.create">({ type: "question.create", question_id: id, subjects });
    return q;
  }

  answer(questionId: string, input: AnswerInput): { question: Question; answer: Answer } {
    this.store.assertAlive();
    this.assertQuiescent();
    const q = this.read(questionId);
    if (q.state === "cancelled") throw new RuntimeError("closed", `question ${questionId} is cancelled`);
    if (q.kind !== "free_text" && (!input.choice || input.choice.length === 0) && !input.text) {
      throw new RuntimeError("invalid", "answer needs a choice or text");
    }
    if (input.choice) {
      const keys = new Set(q.options.map((o) => o.key));
      for (const c of input.choice) if (!keys.has(c)) throw new RuntimeError("invalid", `unknown option ${c}`);
    }
    const subjectsNow = this.snapshot(q.subjects.map((s) => s.id));
    const stale = q.subjects.some((s) => {
      const now = subjectsNow.find((n) => n.id === s.id)!;
      return now.rev !== s.rev || now.sha256 !== s.sha256;
    });
    const a: Answer = {
      answer_id: answerId(q.id, q.answers.length + 1),
      answered_at: nowIso(),
      subjects_at_answer: subjectsNow,
      stale,
      choice: input.choice ?? null,
      text: input.text ?? null,
      received_at: null,
      applied_as: null,
      closed: null,
    };
    q.answers.push(a);
    this.write(q); // persist first, then notify
    this.store.appendEvent<"answer.save">({ type: "answer.save", question_id: q.id, answer_id: a.answer_id, stale });
    return { question: q, answer: a };
  }

  /** Resolve when the question has an unprocessed answer, or after timeoutMs with status pending. */
  wait(
    questionId: string,
    timeoutMs: number,
    signal?: AbortSignal,
  ): Promise<{ status: "answered"; question: Question; answers: Answer[] } | { status: "pending"; question: Question }> {
    this.store.assertAlive();
    const q = this.read(questionId);
    const now = this.pendingFor(questionId);
    if (now.length) return Promise.resolve({ status: "answered", question: q, answers: now });
    return new Promise((resolve) => {
      const done = (): void => {
        cleanup();
        const q2 = this.read(questionId);
        const answers = this.pendingFor(questionId);
        resolve(answers.length ? { status: "answered", question: q2, answers } : { status: "pending", question: q2 });
      };
      const onEvent = (ev: EventOf<"answer.save">): void => {
        if (ev.type === "answer.save" && ev.question_id === questionId) done();
      };
      const timer = setTimeout(done, timeoutMs);
      const onAbort = (): void => done();
      const cleanup = (): void => {
        clearTimeout(timer);
        this.store.emitter.off("event", onEvent);
        signal?.removeEventListener("abort", onAbort);
      };
      this.store.emitter.on("event", onEvent);
      signal?.addEventListener("abort", onAbort, { once: true });
    });
  }

  ack(questionId: string, answerIdValue: string): Answer {
    this.store.assertAlive();
    const q = this.read(questionId);
    const a = this.findAnswer(q, answerIdValue);
    if (!a.received_at) {
      a.received_at = nowIso();
      this.write(q);
      this.store.appendEvent<"answer.ack">({ type: "answer.ack", question_id: q.id, answer_id: a.answer_id });
    }
    return a;
  }

  close(questionId: string, answerIdValue: string, reason: string): Answer {
    this.store.assertAlive();
    this.assertQuiescent();
    if (!reason.trim()) throw new RuntimeError("invalid", "close needs a reason");
    const q = this.read(questionId);
    const a = this.findAnswer(q, answerIdValue);
    if (this.isCommitted(a.answer_id)) throw new RuntimeError("conflict", `answer ${a.answer_id} is already applied`);
    if (!a.closed) {
      a.closed = { at: nowIso(), reason };
      this.write(q);
      this.store.appendEvent<"answer.close">({ type: "answer.close", question_id: q.id, answer_id: a.answer_id, reason });
    }
    return a;
  }

  // ---------- apply (SPEC-0001 §2.1) ----------

  private assertQuiescent(): void {
    const inflight = this.store.inflightIntents();
    if (inflight.length) {
      throw new RuntimeError("inflight", "an apply transaction is in flight; recovery must run first", {
        txids: inflight.map((i) => i.txid),
      });
    }
  }

  private findAnswer(q: Question, answerIdValue: string): Answer {
    const a = q.answers.find((x) => x.answer_id === answerIdValue);
    if (!a) throw new RuntimeError("not_found", `answer ${answerIdValue} not found in ${q.id}`);
    return a;
  }

  private allocateDecisionId(): string {
    return this.store.allocateId("decision");
  }

  apply(
    questionId: string,
    answerIdValue: string,
    input: { decision: DecisionDraft; expected_question_rev?: number },
    hooks?: ApplyHooks,
  ): ApplyResult {
    this.store.assertAlive();
    this.assertQuiescent();
    const q = this.read(questionId);
    const a = this.findAnswer(q, answerIdValue);
    if (input.expected_question_rev !== undefined && input.expected_question_rev !== q.rev) {
      throw new RuntimeError("question_rev", `question ${q.id} is at rev ${q.rev}, expected ${input.expected_question_rev}`);
    }
    // 1. idempotent retry
    const committed = this.store.committedIntentFor(a.answer_id);
    if (committed) return { status: "already_applied", decision_id: committed.decision_id, txid: committed.txid };
    // 2. closed
    if (a.closed) throw new RuntimeError("closed", `answer ${a.answer_id} was closed: ${a.closed.reason}`);
    // 3. version check against the actual files
    const mismatches = this.checkSubjects(q.subjects);
    if (a.stale || mismatches.length) {
      if (!a.stale) {
        a.stale = true;
        this.write(q);
      }
      throw new RuntimeError("stale", `answer ${a.answer_id} targets an older version of its subjects`, { mismatches });
    }
    // 4. stage a: intent (allocates the decision id exactly once)
    const txid = randomId(8);
    const decision_id = this.allocateDecisionId();
    const intent = this.store.appendEvent<"apply.intent">({
      type: "apply.intent",
      txid,
      question_id: q.id,
      answer_id: a.answer_id,
      decision_id,
      subjects: q.subjects,
      decision: input.decision,
    });
    this.afterStage(hooks, "intent");
    return this.rollForward(intent, hooks);
  }

  private afterStage(hooks: ApplyHooks | undefined, stage: ApplyStage): void {
    try {
      hooks?.afterStage?.(stage);
    } catch (e) {
      if (e instanceof SimulatedCrash) this.store.kill();
      throw e;
    }
  }

  /** Stages b, c, d; idempotent so recovery can repeat it. */
  private rollForward(intent: EventOf<"apply.intent">, hooks?: ApplyHooks): ApplyResult {
    // stage b: decision file (provisional until commit)
    if (!this.store.hasRecord(intent.decision_id)) {
      const d = intent.decision;
      this.store.writeRecord(
        intent.decision_id,
        {
          kind: "decision",
          title: d.title,
          status: "accepted",
          question: intent.question_id,
          answer: intent.answer_id,
          txid: intent.txid,
          chosen: d.chosen,
          alternatives: d.alternatives,
          reviewed: intent.subjects,
          links: { rationale_for: intent.subjects.map((s) => s.id) },
        },
        d.body,
      );
    }
    this.afterStage(hooks, "decision");
    // stage c: applied_as on the answer (provisional until commit)
    const q = this.read(intent.question_id);
    const a = this.findAnswer(q, intent.answer_id);
    if (!a.applied_as || a.applied_as.txid !== intent.txid) {
      a.applied_as = { decision_id: intent.decision_id, txid: intent.txid };
      this.write(q);
    }
    this.afterStage(hooks, "question");
    // stage d: commit — the only confirmation point
    if (!this.store.isCommitted(intent.txid)) {
      this.store.appendEvent<"apply.commit">({ type: "apply.commit", txid: intent.txid });
    }
    this.afterStage(hooks, "commit");
    const q2 = this.read(intent.question_id);
    this.write(q2); // refresh derived state
    return { status: "applied", decision_id: intent.decision_id, txid: intent.txid };
  }

  // ---------- recovery (SPEC-0001 §2.2) ----------

  recover(): RecoveryReport {
    this.store.assertAlive();
    const report: RecoveryReport = { recovered: [], retracted: [] };
    for (const intent of this.store.inflightIntents()) {
      const mismatches = this.checkSubjects(intent.subjects);
      if (mismatches.length === 0) {
        this.rollForward(intent);
        report.recovered.push({ txid: intent.txid, answer_id: intent.answer_id, decision_id: intent.decision_id, outcome: "forward" });
      } else {
        this.store.appendEvent<"apply.abort">({
          type: "apply.abort",
          txid: intent.txid,
          reason: `subjects changed: ${mismatches.map((m) => m.id).join(", ")}`,
        });
        this.retract(intent);
        report.recovered.push({
          txid: intent.txid,
          answer_id: intent.answer_id,
          decision_id: intent.decision_id,
          outcome: "abort",
          reason: mismatches.map((m) => m.id).join(", "),
        });
      }
    }
    // Retraction and roll-forward are idempotent; repeat them for every past transaction so a
    // crash during recovery itself cannot leave provisional files behind.
    const aborted = this.store.abortedTxids();
    const committed = this.store.committedTxids();
    for (const intent of this.store.intents()) {
      if (aborted.has(intent.txid)) {
        if (this.retract(intent)) report.retracted.push(intent.txid);
      } else if (committed.has(intent.txid)) {
        this.ensureCommittedFiles(intent);
      }
    }
    return report;
  }

  /** Remove provisional files of an aborted transaction. Returns true if anything was removed. */
  private retract(intent: EventOf<"apply.intent">): boolean {
    let changed = false;
    if (this.store.hasRecord(intent.decision_id)) {
      const d = this.store.readRecord(intent.decision_id);
      if (d.frontmatter.txid === intent.txid) {
        this.store.removeRecord(intent.decision_id, `aborted transaction ${intent.txid}`);
        changed = true;
      }
    }
    let q: Question;
    try {
      q = this.read(intent.question_id);
    } catch {
      return changed;
    }
    const a = q.answers.find((x) => x.answer_id === intent.answer_id);
    if (a && ((a.applied_as && a.applied_as.txid === intent.txid) || !a.stale)) {
      if (a.applied_as && a.applied_as.txid === intent.txid) a.applied_as = null;
      a.stale = true;
      this.write(q);
      changed = true;
    }
    return changed;
  }

  private ensureCommittedFiles(intent: EventOf<"apply.intent">): void {
    if (!this.store.hasRecord(intent.decision_id)) {
      // committed but decision file is gone (manual deletion): rewrite it from the journal
      this.rollForward(intent);
      return;
    }
    const q = this.read(intent.question_id);
    const a = q.answers.find((x) => x.answer_id === intent.answer_id);
    if (a && (!a.applied_as || a.applied_as.txid !== intent.txid)) {
      a.applied_as = { decision_id: intent.decision_id, txid: intent.txid };
      this.write(q);
    }
  }
}
