import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { RunRecord, VerificationPlugin } from "@prepare-dev/core";
import { Runtime } from "./runtime.js";

let root: string;

beforeEach(() => {
  root = fs.mkdtempSync(path.join(os.tmpdir(), "prepare-dev-runs-"));
});
afterEach(() => {
  fs.rmSync(root, { recursive: true, force: true });
});

function fakeRun(rt: Runtime, specId: string, reqId: string, inputPath: string): RunRecord {
  const spec = rt.store.readRecord(specId);
  const req = rt.store.readRecord(reqId);
  const run: RunRecord = {
    id: rt.store.allocateId("run"),
    plugin: { name: "fake", version: "0" },
    tool: { name: "fake", version: "0", extra: {} },
    method: "test",
    claims: [{ spec: { id: spec.id, rev: spec.rev, sha256: spec.sha256 }, claim: "C1", requirement: { id: req.id, rev: req.rev, sha256: req.sha256 }, native: { ref: "x", statement: null }, result: "verified" }],
    targets: [{ path: inputPath, sha256: rt.store.fileVersion(inputPath)!, role: "target" }],
    inputs: { files: [], config: {} },
    environment: { platform: "test", started_at: "2026-11-08T00:00:00Z", finished_at: "2026-11-08T00:00:01Z", duration_ms: 1000 },
    result: "verified",
    assumptions: [],
    scope: { bounded: true, bounds: { tests: 1 }, excluded: [], covered: [] },
    artifacts: [],
    summary: "fake",
  };
  rt.store.writeRun(run);
  rt.store.appendEvent<"run.record">({ type: "run.record", run_id: run.id, result: run.result });
  return run;
}

describe("evidence applicability (M1-13)", () => {
  it("[V2] reserves distinct run ids before asynchronous verification finishes and after restart", async () => {
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => { release = resolve; });
    const plugin: VerificationPlugin = {
      name: "paused", version: "1",
      capabilities: () => ({ languages: [], methods: ["test"], properties: [], requires: { tools: [] } }),
      guidance: () => ({ docs: [], templates: [] }),
      detect: async () => { await gate; return { available: false, versions: {}, reason: "test probe complete" }; },
      inputs: async () => ({ targets: [], files: [] }),
      run: async () => { throw new Error("unavailable tool must not run"); },
      parse: () => { throw new Error("unavailable tool has no output"); },
    };
    const rt = Runtime.open(root, { plugins: [plugin] });
    const spec = rt.store.createRecord("spec", { title: "S", claims: [{ id: "C", statement: "C", native: [{ plugin: "paused", ref: "C" }] }] }, "S");
    const input = { plugin: "paused", claims: [{ spec: spec.id, claim: "C" }] };
    const first = rt.runs.start(input);
    const second = rt.runs.start(input);
    expect(first.run_id).not.toBe(second.run_id);
    expect(rt.store.listRunIds()).toEqual([]);
    const reopened = Runtime.open(root);
    expect([first.run_id, second.run_id]).not.toContain(reopened.store.allocateId("run"));
    release();
    await Promise.all([rt.runs.wait(first.run_id, 5_000), rt.runs.wait(second.run_id, 5_000)]);
    expect(rt.store.listRunIds()).toEqual([first.run_id, second.run_id]);
  });

  it("[V1] a run stops applying when the spec, the requirement, or an input file changes, and the trace shows no current evidence", () => {
    const rt = Runtime.open(root);
    const req = rt.store.createRecord("requirement", { title: "R" }, "r\n");
    const spec = rt.store.createRecord(
      "spec",
      { title: "S", links: { specifies: [req.id] }, claims: [{ id: "C1", statement: "s", requirement: req.id, native: [{ plugin: "fake", ref: "x" }] }] },
      "s\n",
    );
    const input = path.join("src", "impl.txt");
    fs.mkdirSync(path.join(root, "src"), { recursive: true });
    fs.writeFileSync(path.join(root, input), "v1");
    const run = fakeRun(rt, spec.id, req.id, input);

    expect(rt.runs.applicability(run).applicable).toBe(true);
    expect(rt.trace.for(req.id).specs[0]!.claims[0]!.current?.run_id).toBe(run.id);

    // spec body edited through the runtime -> claim_spec mismatch
    const s1 = rt.store.readRecord(spec.id);
    rt.store.writeRecord(spec.id, {}, "s v2\n", { rev: s1.rev, sha256: s1.sha256 });
    let app = rt.runs.applicability(run);
    expect(app.applicable).toBe(false);
    expect(app.mismatches.map((m) => m.kind)).toEqual(["claim_spec"]);
    expect(rt.trace.for(req.id).specs[0]!.claims[0]!.current).toBeNull();
    expect(rt.status.view().runs_not_applicable.map((r) => r.run_id)).toEqual([run.id]);

    // a fresh run against the current versions becomes the current evidence again
    const run2 = fakeRun(rt, spec.id, req.id, input);
    expect(rt.trace.for(req.id).specs[0]!.claims[0]!.current?.run_id).toBe(run2.id);

    // requirement edited externally (rev untouched) -> claim_requirement mismatch
    const reqAbs = path.join(root, "prepare", "requirements", `${req.id}.md`);
    fs.appendFileSync(reqAbs, "\n外部編集\n");
    app = rt.runs.applicability(run2);
    expect(app.mismatches.map((m) => m.kind)).toEqual(["claim_requirement"]);

    // input file changed -> target mismatch; restoring the content restores applicability
    const run3 = fakeRun(rt, spec.id, req.id, input);
    fs.writeFileSync(path.join(root, input), "v2");
    expect(rt.runs.applicability(run3).mismatches.map((m) => m.kind)).toEqual(["target"]);
    fs.writeFileSync(path.join(root, input), "v1");
    expect(rt.runs.applicability(run3).applicable).toBe(true);
    const metadata = "pnpm-lock.yaml";
    fs.writeFileSync(path.join(root, metadata), "lock-v1");
    run3.inputs.files.push({ path: metadata, sha256: rt.store.fileVersion(metadata)!, role: "config" });
    fs.writeFileSync(path.join(root, metadata), "lock-v2");
    expect(rt.runs.applicability(run3).mismatches).toEqual([expect.objectContaining({ kind: "input", ref: metadata })]);
    fs.unlinkSync(path.join(root, metadata));
    expect(rt.runs.applicability(run3).mismatches).toEqual([expect.objectContaining({ kind: "input", ref: metadata, current: null })]);
  });
});
