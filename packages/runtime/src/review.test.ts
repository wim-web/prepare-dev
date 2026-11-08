import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { Runtime } from "./runtime.js";
import { buildBundle } from "./bundle.js";

let root: string;
beforeEach(() => {
  root = fs.mkdtempSync(path.join(os.tmpdir(), "prepare-dev-review-"));
});
afterEach(() => {
  fs.rmSync(root, { recursive: true, force: true });
});

describe("review records and bundles (M2)", () => {
  it("validates review frontmatter, counts unaddressed findings in status, and lists reviews in trace", () => {
    const rt = Runtime.open(root);
    const req = rt.store.createRecord("requirement", { title: "R", status: "proposed" }, "要求本文\n");
    const spec = rt.store.createRecord("spec", { title: "S", links: { specifies: [req.id] }, claims: [] }, "仕様本文\n");
    const reqRef = { id: req.id, rev: req.rev, sha256: req.sha256 };
    const specRef = { id: spec.id, rev: spec.rev, sha256: spec.sha256 };

    // a review without a reviewer is rejected by the schema
    expect(() => rt.store.createRecord("review", { title: "bad", stage: "comprehension", subjects: [specRef] }, "")).toThrow();

    const rev = rt.store.createRecord(
      "review",
      {
        title: "SPEC の comprehension レビュー",
        stage: "comprehension",
        subjects: [specRef],
        inputs: [specRef, reqRef],
        reviewer: { persona: "comprehension", persona_version: "egm@dea7761", host: "test", context: "fresh" },
        paraphrase: "この文書は X を提案している。",
        findings: [
          { id: "comprehension-1-1", severity: "blocker", scope: "§2", summary: "用語 A が未定義", proposed: "fix" },
          { id: "comprehension-1-2", severity: "nit", scope: "§3", summary: "表現が曖昧", proposed: "decline", actual: "declined", actual_ref: null },
          { id: "comprehension-1-3", severity: "major", scope: "§1", summary: "前提が書かれていない", proposed: "escalate" },
        ],
      },
      "レビュー本文\n",
    );
    const fm = rt.store.readRecord(rev.id).frontmatter as unknown as { findings: Array<{ actual: string }> };
    expect(fm.findings.map((f) => f.actual)).toEqual(["unaddressed", "declined", "unaddressed"]);

    const st = rt.status.view();
    expect(st.open_findings.map((f) => f.finding_id)).toEqual(["comprehension-1-1", "comprehension-1-3"]);
    expect(st.open_findings[0]!.subjects).toEqual([spec.id]);

    const tr = rt.trace.for(spec.id);
    expect(tr.reviews.length).toBe(1);
    expect(tr.reviews[0]!.findings).toEqual({ total: 3, unaddressed: 2, blockers_open: 1 });
    expect(tr.reviews[0]!.subjects_current).toBe(true);

    // editing the reviewed spec makes the review refer to an older version
    const cur = rt.store.readRecord(spec.id);
    rt.store.writeRecord(spec.id, {}, "仕様本文 v2\n", { rev: cur.rev, sha256: cur.sha256 });
    expect(rt.trace.for(spec.id).reviews[0]!.subjects_current).toBe(false);
  });

  it("bundles records at pinned versions with related decisions and writes the file", () => {
    const rt = Runtime.open(root);
    const req = rt.store.createRecord("requirement", { title: "要求", status: "accepted" }, "要求の本文\n");
    const des = rt.store.createRecord("design", { title: "設計", alternatives: [{ key: "a", title: "A", status: "chosen" }], chosen: "a" }, "設計の本文\n");
    const dec = rt.store.createRecord("decision", { title: "判断", status: "accepted", reviewed: [{ id: req.id, rev: req.rev, sha256: req.sha256 }], chosen: "a" }, "理由\n");
    const unrelated = rt.store.createRecord("decision", { title: "無関係", status: "accepted" }, "x\n");

    const b = buildBundle(rt.store, [req.id, des.id], () => true, { out: true, title: "テスト" });
    expect(b.inputs.map((i) => i.id)).toEqual([req.id, des.id, dec.id]);
    expect(b.inputs.find((i) => i.id === req.id)).toEqual({ id: req.id, rev: 1, sha256: req.sha256 });
    expect(b.markdown).toContain("## REQ-0001 — 要求");
    expect(b.markdown).toContain("要求の本文");
    expect(b.markdown).toContain("設計の本文");
    expect(b.markdown).toContain(`## ${dec.id} — 判断`);
    expect(b.markdown).not.toContain(unrelated.id);
    expect(b.path).toMatch(/^\.prepare-dev\/reviews\/bundle-.*\.md$/);
    expect(fs.readFileSync(path.join(root, b.path!), "utf8")).toBe(b.markdown);

    const noExpand = buildBundle(rt.store, [req.id], () => true, { expand: false });
    expect(noExpand.inputs.map((i) => i.id)).toEqual([req.id]);
    expect(noExpand.path).toBeNull();
  });

  it("status lists questions still waiting for the human, and flags ones whose subjects moved", () => {
    const rt = Runtime.open(root);
    const spec = rt.store.createRecord("spec", { title: "仕様", claims: [] }, "本文\n");
    const ask = (prompt: string) =>
      rt.questions.create({ kind: "single_choice", prompt, subjects: [spec.id], options: [{ key: "a", label: "A", recommended: true }] });
    const q1 = ask("一つ目");
    const q2 = ask("二つ目");
    expect(rt.status.view().waiting_questions.map((w) => [w.question_id, w.subjects, w.subjects_current])).toEqual([
      [q1.id, [spec.id], true],
      [q2.id, [spec.id], true],
    ]);

    // an answered question is no longer waiting (it moves to pending until applied or closed)
    rt.questions.answer(q1.id, { choice: ["a"] });
    const st = rt.status.view();
    expect(st.waiting_questions.map((w) => w.question_id)).toEqual([q2.id]);
    expect(st.pending.map((p) => p.question_id)).toEqual([q1.id]);

    // editing the subject after asking is visible before anyone answers
    rt.store.writeRecord(spec.id, {}, "本文 v2\n", { rev: spec.rev, sha256: spec.sha256 });
    expect(rt.status.view().waiting_questions[0]!.subjects_current).toBe(false);
  });

  it("re-asks a question at new subject versions by cancelling the unanswered old one", () => {
    const rt = Runtime.open(root);
    const spec = rt.store.createRecord("spec", { title: "仕様", claims: [] }, "本文\n");
    const ask = (prompt: string) =>
      rt.questions.create({ kind: "single_choice", prompt, subjects: [spec.id], options: [{ key: "a", label: "A", recommended: true }] });
    const old = ask("古い版への質問");
    const answered = ask("回答済みの質問");
    rt.questions.answer(answered.id, { choice: ["a"] });

    const cur = rt.store.readRecord(spec.id);
    rt.store.writeRecord(spec.id, {}, "本文 v2\n", { rev: cur.rev, sha256: cur.sha256 });
    const fresh = ask("新しい版への質問");

    // reason is required, the replacement must exist, and answered questions are handled per answer
    expect(() => rt.questions.cancel(old.id, " ")).toThrow(/reason/);
    expect(() => rt.questions.cancel(old.id, "置き換え", "Q-9999")).toThrow();
    expect(() => rt.questions.cancel(answered.id, "置き換え")).toThrow(/apply or close/);

    const c = rt.questions.cancel(old.id, "対象の改訂で再質問", fresh.id);
    expect(c.state).toBe("cancelled");
    expect(c.cancelled).toEqual({ at: expect.any(String), reason: "対象の改訂で再質問", replaced_by: fresh.id });
    // idempotent: the first cancellation stays
    expect(rt.questions.cancel(old.id, "別の理由").cancelled!.reason).toBe("対象の改訂で再質問");
    expect(() => rt.questions.answer(old.id, { choice: ["a"] })).toThrow(/cancelled/);

    const st = rt.status.view();
    expect(st.waiting_questions.map((w) => [w.question_id, w.subjects_current])).toEqual([[fresh.id, true]]);
    expect(st.pending.map((p) => p.question_id)).toEqual([answered.id]);
    expect(rt.store.allEvents().filter((e) => e.type === "question.cancel")).toEqual([
      expect.objectContaining({ question_id: old.id, replaced_by: fresh.id }),
    ]);

    // the cancellation survives a restart (the question file is the record)
    const rt2 = Runtime.open(root);
    expect(rt2.questions.list().find((q) => q.id === old.id)!.state).toBe("cancelled");
  });

  it("trace follows links in both directions so DIS → REQ → DES → SPEC can be walked", () => {
    const rt = Runtime.open(root);
    const dis = rt.store.createRecord("discovery", { title: "問題", problem: "p" }, "本文\n");
    const req = rt.store.createRecord("requirement", { title: "要求", links: { derived_from: [dis.id] } }, "本文\n");
    const des = rt.store.createRecord("design", { title: "設計", links: { derived_from: [req.id] } }, "本文\n");
    const spec = rt.store.createRecord("spec", { title: "仕様", links: { specifies: [req.id] }, claims: [] }, "本文\n");

    const fromDis = rt.trace.for(dis.id).links;
    expect(fromDis).toEqual([expect.objectContaining({ id: req.id, kind: "requirement", via: "derived_from", direction: "in" })]);

    const fromReq = rt.trace.for(req.id).links.map((l) => [l.id, l.via, l.direction]);
    expect(fromReq).toEqual([
      [dis.id, "derived_from", "out"],
      [des.id, "derived_from", "in"],
      [spec.id, "specifies", "in"],
    ]);

    // a link to something that is not a record (a doc path) is ignored, not an error
    rt.store.writeRecord(req.id, { links: { derived_from: [dis.id], references: ["docs/x.md"] } }, "本文\n", { rev: req.rev, sha256: req.sha256 });
    expect(rt.trace.for(req.id).links.map((l) => l.id)).toEqual([dis.id, des.id, spec.id]);
  });
});
