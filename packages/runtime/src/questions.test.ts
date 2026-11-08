import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { parseMarkdown, serializeMarkdown } from "@prepare-dev/core";
import { Runtime } from "./runtime.js";
import { RuntimeError, SimulatedCrash } from "./errors.js";
import type { ApplyStage } from "./questions.js";

let root: string;

function open(): Runtime {
  return Runtime.open(root);
}

function seed(rt: Runtime): string {
  const r = rt.store.createRecord("requirement", { title: "回答は依頼した版にのみ一度だけ反映する", status: "proposed" }, "本文 v1\n");
  return r.id;
}

function ask(rt: Runtime, subject: string) {
  return rt.questions.create({
    kind: "single_choice",
    prompt: "対象版が変わっていた場合の扱いは？",
    subjects: [subject],
    options: [
      { key: "hold", label: "保存して再確認", recommended: true },
      { key: "apply", label: "そのまま反映" },
    ],
  });
}

const decision = { title: "保存して再確認する", body: "理由: 古い版への誤適用を避ける。\n", chosen: "hold", alternatives: ["apply"] };

function crashAfter(stage: ApplyStage) {
  return {
    afterStage: (s: ApplyStage) => {
      if (s === stage) throw new SimulatedCrash(s);
    },
  };
}

/** Edit a record on disk the way an external editor would (body only; rev untouched). */
function externalEdit(rt: Runtime, id: string, newBody: string): void {
  const rec = rt.store.readRecord(id);
  const abs = path.join(root, rec.path);
  const { frontmatter } = parseMarkdown(fs.readFileSync(abs, "utf8"));
  fs.writeFileSync(abs, serializeMarkdown({ frontmatter, body: newBody }));
}

beforeEach(() => {
  root = fs.mkdtempSync(path.join(os.tmpdir(), "prepare-dev-test-"));
});

afterEach(() => {
  fs.rmSync(root, { recursive: true, force: true });
});

describe("questions and apply (SPEC-0001)", () => {
  it("[C1] M1-1/2/7: create, answer, wait, apply once, trace", async () => {
    const rt = open();
    const req = seed(rt);
    const q = ask(rt, req);
    const waiting = rt.questions.wait(q.id, 5_000);
    const { answer } = rt.questions.answer(q.id, { choice: ["hold"] });
    const w = await waiting;
    expect(w.status).toBe("answered");
    if (w.status !== "answered") return;
    expect(w.answers[0]!.answer_id).toBe(answer.answer_id);
    expect(rt.questions.pending().map((p) => p.answer_id)).toEqual([answer.answer_id]);

    rt.questions.ack(q.id, answer.answer_id);
    expect(rt.questions.pending().length).toBe(1); // ack does not process

    const r1 = rt.questions.apply(q.id, answer.answer_id, { decision });
    expect(r1.status).toBe("applied");
    const r2 = rt.questions.apply(q.id, answer.answer_id, { decision });
    expect(r2.status).toBe("already_applied");
    expect(r2.decision_id).toBe(r1.decision_id);
    expect(rt.questions.pending()).toEqual([]);
    expect(rt.store.listRecords("decision").map((d) => d.id)).toEqual([r1.decision_id]);

    const trace = rt.trace.for(req);
    expect(trace.decisions.map((d) => d.id)).toEqual([r1.decision_id]);
    expect(trace.decisions[0]!.committed).toBe(true);
    expect(trace.decisions[0]!.reviewed_current).toBe(true);
    expect(trace.questions[0]!.answers[0]!.applied_as).toBe(r1.decision_id);
  });

  it("[C3] M1-3: wait interrupted, then answered: pending still returns it", async () => {
    const rt = open();
    const q = ask(rt, seed(rt));
    const w = await rt.questions.wait(q.id, 50);
    expect(w.status).toBe("pending");
    const { answer } = rt.questions.answer(q.id, { choice: ["hold"] });
    expect(rt.questions.pending().map((p) => p.answer_id)).toEqual([answer.answer_id]);
    const w2 = await rt.questions.wait(q.id, 50);
    expect(w2.status).toBe("answered");
  });

  it("[C3] M1-4: ack then process restart before apply: answer stays pending", () => {
    const rt = open();
    const q = ask(rt, seed(rt));
    const { answer } = rt.questions.answer(q.id, { choice: ["hold"] });
    rt.questions.ack(q.id, answer.answer_id);
    const rt2 = open();
    const p = rt2.questions.pending();
    expect(p.map((x) => x.answer_id)).toEqual([answer.answer_id]);
    expect(p[0]!.received).toBe(true);
    expect(rt2.status.view().pending.length).toBe(1);
  });

  it("[C2] M1-5: subject edited through the runtime after the answer: apply is rejected as stale", () => {
    const rt = open();
    const req = seed(rt);
    const q = ask(rt, req);
    const { answer } = rt.questions.answer(q.id, { choice: ["hold"] });
    const cur = rt.store.readRecord(req);
    rt.store.writeRecord(req, {}, "本文 v2\n", { rev: cur.rev, sha256: cur.sha256 });
    expect(() => rt.questions.apply(q.id, answer.answer_id, { decision })).toThrowError(/older version/);
    const a = rt.questions.read(q.id).answers[0]!;
    expect(a.stale).toBe(true);
    expect(rt.questions.pending()[0]!.stale).toBe(true); // still pending, for re-confirmation
    expect(rt.store.listRecords("decision")).toEqual([]);
  });

  it("[C2] M1-6: external edit without rev change + index regeneration: apply is rejected", () => {
    const rt = open();
    const req = seed(rt);
    const q = ask(rt, req);
    const { answer } = rt.questions.answer(q.id, { choice: ["hold"] });
    externalEdit(rt, req, "外部エディタで書き換えた本文\n");
    fs.rmSync(path.join(root, ".prepare-dev", "index.json"));
    const rt2 = open(); // rebuilds the index from events, scans, detects the change
    const ev = rt2.store.allEvents().filter((e) => e.type === "external_change");
    expect(ev.length).toBe(1);
    const rec = rt2.store.readRecord(req);
    expect(rec.rev).toBe(2);
    expect(() => rt2.questions.apply(q.id, answer.answer_id, { decision })).toThrowError(RuntimeError);
    expect(rt2.questions.read(q.id).answers[0]!.stale).toBe(true);
  });

  it("[C1][C3] M1-8: crash after stage b or c, restart: exactly one decision, rolled forward", () => {
    for (const stage of ["intent", "decision", "question"] as ApplyStage[]) {
      const rt = open();
      const req = seed(rt);
      const q = ask(rt, req);
      const { answer } = rt.questions.answer(q.id, { choice: ["hold"] });
      expect(() => rt.questions.apply(q.id, answer.answer_id, { decision }, crashAfter(stage))).toThrowError(SimulatedCrash);
      expect(() => rt.questions.pending()).toThrowError(/dead/);
      const rt2 = open();
      expect(rt2.recovery.recovered.length).toBe(1);
      expect(rt2.recovery.recovered[0]!.outcome).toBe("forward");
      expect(rt2.store.inflightIntents()).toEqual([]);
      const decisions = rt2.store.listRecords("decision");
      expect(decisions.length).toBe(1);
      expect(rt2.questions.pending()).toEqual([]);
      const r = rt2.questions.apply(q.id, answer.answer_id, { decision });
      expect(r.status).toBe("already_applied");
      expect(r.decision_id).toBe(decisions[0]!.id);
      fs.rmSync(root, { recursive: true, force: true });
      root = fs.mkdtempSync(path.join(os.tmpdir(), "prepare-dev-test-"));
    }
  });

  it("[C2][C3] M1-9: crash after stage c, subject edited while down, restart: abort, nothing public, answer pending and stale", () => {
    const rt = open();
    const req = seed(rt);
    const q = ask(rt, req);
    const { answer } = rt.questions.answer(q.id, { choice: ["hold"] });
    expect(() => rt.questions.apply(q.id, answer.answer_id, { decision }, crashAfter("question"))).toThrowError(SimulatedCrash);
    // provisional files exist on disk now
    expect(fs.existsSync(path.join(root, "prepare", "decisions", "DEC-0001.md"))).toBe(true);
    const rtDown = Runtime; // the daemon is down; the human edits the subject
    void rtDown;
    const abs = path.join(root, "prepare", "requirements", `${req}.md`);
    const { frontmatter } = parseMarkdown(fs.readFileSync(abs, "utf8"));
    fs.writeFileSync(abs, serializeMarkdown({ frontmatter, body: "停止中に編集\n" }));

    const rt2 = open();
    expect(rt2.recovery.recovered[0]!.outcome).toBe("abort");
    expect(rt2.store.inflightIntents()).toEqual([]);
    expect(rt2.store.abortedTxids().size).toBe(1);
    expect(fs.existsSync(path.join(root, "prepare", "decisions", "DEC-0001.md"))).toBe(false);
    expect(rt2.trace.publicDecisions()).toEqual([]);
    expect(rt2.status.view().stale_decisions).toEqual([]);
    const p = rt2.questions.pending();
    expect(p.map((x) => x.answer_id)).toEqual([answer.answer_id]);
    expect(p[0]!.stale).toBe(true);
    expect(rt2.questions.read(q.id).answers[0]!.applied_as).toBeNull();
    // the aborted decision id is consumed; a later apply would get a new one
    expect(rt2.store.allocateId("decision")).toBe("DEC-0002");
  });

  it("[C1] M1-10: crash after commit before the response, subject edited, retry: original result, no retraction", () => {
    const rt = open();
    const req = seed(rt);
    const q = ask(rt, req);
    const { answer } = rt.questions.answer(q.id, { choice: ["hold"] });
    expect(() => rt.questions.apply(q.id, answer.answer_id, { decision }, crashAfter("commit"))).toThrowError(SimulatedCrash);
    const abs = path.join(root, "prepare", "requirements", `${req}.md`);
    const { frontmatter } = parseMarkdown(fs.readFileSync(abs, "utf8"));
    fs.writeFileSync(abs, serializeMarkdown({ frontmatter, body: "確定後に編集\n" }));

    const rt2 = open();
    expect(rt2.recovery.recovered).toEqual([]); // nothing inflight: committed transactions are never aborted
    const r = rt2.questions.apply(q.id, answer.answer_id, { decision });
    expect(r.status).toBe("already_applied");
    expect(r.decision_id).toBe("DEC-0001");
    expect(rt2.trace.publicDecisions().map((d) => d.id)).toEqual(["DEC-0001"]);
    const view = rt2.trace.decisionView(rt2.store.readRecord("DEC-0001"));
    expect(view.committed).toBe(true);
    expect(view.reviewed_current).toBe(false); // decided against an older version; shown as such, not retracted
    expect(rt2.questions.pending()).toEqual([]);
  });

  it("close: closes an unprocessed answer and refuses to close an applied one", () => {
    const rt = open();
    const q = ask(rt, seed(rt));
    const { answer } = rt.questions.answer(q.id, { choice: ["apply"] });
    rt.questions.close(q.id, answer.answer_id, "再質問で置き換え");
    expect(rt.questions.pending()).toEqual([]);
    expect(() => rt.questions.apply(q.id, answer.answer_id, { decision })).toThrowError(/closed/);
    const { answer: a2 } = rt.questions.answer(q.id, { choice: ["hold"] });
    rt.questions.apply(q.id, a2.answer_id, { decision });
    expect(() => rt.questions.close(q.id, a2.answer_id, "x")).toThrowError(/already applied/);
  });

  it("record_put: conflict on wrong expected version", () => {
    const rt = open();
    const req = seed(rt);
    const cur = rt.store.readRecord(req);
    expect(() => rt.store.writeRecord(req, {}, "x", { rev: cur.rev, sha256: "deadbeef" })).toThrowError(/changed/);
    const next = rt.store.writeRecord(req, {}, "x", { rev: cur.rev, sha256: cur.sha256 });
    expect(next.rev).toBe(2);
  });
});
