import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { expect, it } from "vitest";
import { z } from "zod";
import { Runtime } from "./runtime.js";
import { Store } from "./store.js";
import { Questions } from "./questions.js";
import { SimulatedCrash } from "./errors.js";

const Observation = z.object({ answer: z.number(), aborted: z.boolean(), decision: z.boolean(), applied: z.boolean(), stale: z.boolean(), ready: z.boolean() });
const Trace = z.object({
  stage: z.enum(["intent", "decision", "question"]),
  initial: Observation,
  steps: z.array(z.object({ action: z.enum(["resume", "crash"]), observe: Observation })),
});
const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
const traces = fs.readFileSync(path.join(repo, "model/traces/recovery.jsonl"), "utf8").trim().split("\n").map((line) => Trace.parse(JSON.parse(line)));

it("[C3][C5][C6] replays Lean recovery traces including repeated crashes at every abort-cleanup write", () => {
  expect(traces).toHaveLength(48);
  for (const trace of traces) {
    const root = fs.mkdtempSync(path.join(os.tmpdir(), "prepare-recovery-"));
    try {
      const rt = Runtime.open(root);
      const req = rt.store.createRecord("requirement", { title: "R" }, "before");
      const q = rt.questions.create({ kind: "free_text", prompt: "Q", subjects: [req.id] });
      const { answer } = rt.questions.answer(q.id, { text: "saved answer" });
      expect(() => rt.questions.apply(q.id, answer.answer_id, { decision: { title: "D", body: "D", alternatives: [] } }, {
        afterStage(stage) { if (stage === trace.stage) throw new SimulatedCrash(stage); },
      })).toThrowError(SimulatedCrash);
      fs.appendFileSync(path.join(root, req.path), "\nchanged while stopped");
      const observe = () => {
        const store = Store.open(root);
        const questions = new Questions(store);
        const a = store.readQuestion(q.id).answers[0]!;
        expect(a.answer_id).toBe(answer.answer_id);
        expect(a.text).toBe("saved answer");
        const ready = !questions.recoveryPending();
        if (!ready) expect(() => questions.ack(q.id, answer.answer_id)).toThrowError(/recovery must run first/);
        expect(questions.read(q.id).answers[0]!.applied_as).toBeNull();
        return { answer: 1, aborted: store.abortedTxids().size > 0, decision: store.hasRecord("DEC-0001"), applied: a.applied_as !== null, stale: a.stale, ready };
      };
      expect(observe()).toEqual(trace.initial);
      for (const step of trace.steps) {
        if (step.action === "resume") {
          try {
            Runtime.open(root, { recoveryHooks: { afterStage(stage) { throw new SimulatedCrash(stage); } } });
          } catch (error) {
            if (!(error instanceof SimulatedCrash)) throw error;
          }
        }
        expect(observe()).toEqual(step.observe);
      }
      const recovered = Runtime.open(root);
      expect(recovered.status.view().recovery_pending).toBe(false);
      expect(recovered.questions.pending()[0]?.answer_id).toBe(answer.answer_id);
      expect(recovered.trace.publicDecisions()).toEqual([]);
    } finally {
      fs.rmSync(root, { recursive: true, force: true });
    }
  }
}, 60_000);
