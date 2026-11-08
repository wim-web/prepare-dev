import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";
import { readDaemonInfo } from "./client.js";
import { toolPaths } from "./installation.js";

let root: string;
let client: Client;

function text(r: unknown): unknown {
  const c = (r as { content: Array<{ type: string; text: string }> }).content[0]!;
  return JSON.parse(c.text);
}

beforeAll(async () => {
  root = fs.mkdtempSync(path.join(os.tmpdir(), "prepare-dev-mcp-"));
  const repo = toolPaths().identity.root;
  const transport = new StdioClientTransport({
    command: path.join(repo, "node_modules", ".bin", "tsx"),
    args: [path.join(repo, "packages", "cli", "src", "index.ts"), "mcp", "--root", root, "--host", "test"],
    cwd: repo,
    stderr: "pipe",
  });
  client = new Client({ name: "prepare-dev-test", version: "0.0.0" });
  await client.connect(transport);
}, 60_000);

afterAll(async () => {
  await client.close();
  const info = readDaemonInfo(root);
  if (info) process.kill(info.pid, "SIGTERM");
  fs.rmSync(root, { recursive: true, force: true });
});

describe("MCP entry (stdio) over the daemon", () => {
  it("exposes the tools and runs the question flow", async () => {
    const tools = (await client.listTools()).tools.map((t) => t.name).sort();
    expect(tools).toEqual(
      ["question_ack", "question_apply", "question_cancel", "question_close", "question_create", "question_pending", "question_wait", "record_get", "record_list", "record_put", "review_bundle", "status", "trace", "verify_get", "verify_run", "verify_wait"].sort(),
    );

    const rec = text(await client.callTool({ name: "record_put", arguments: { kind: "requirement", title: "要求", body: "本文" } })) as { id: string; rev: number; sha256: string };
    expect(rec.id).toBe("REQ-0001");

    const created = text(await client.callTool({ name: "question_create", arguments: { kind: "single_choice", prompt: "?", subjects: [rec.id], options: [{ key: "a", label: "A", recommended: true }] } })) as {
      question: { id: string };
      url: string;
    };
    expect(created.question.id).toBe("Q-0001");
    expect(created.url).toMatch(/\/q\/Q-0001$/);

    const w1 = text(await client.callTool({ name: "question_wait", arguments: { question_id: "Q-0001", timeout_ms: 200 } })) as { status: string };
    expect(w1.status).toBe("pending");

    // the human answers through the HTTP API (what the UI does)
    const info = readDaemonInfo(root)!;
    const res = await fetch(`${info.url}/api/questions/Q-0001/answers`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ choice: ["a"] }) });
    expect(res.status).toBe(201);

    const w2 = text(await client.callTool({ name: "question_wait", arguments: { question_id: "Q-0001", timeout_ms: 1000 } })) as { status: string; answers: Array<{ answer_id: string }> };
    expect(w2.status).toBe("answered");
    const aid = w2.answers[0]!.answer_id;

    const pending = text(await client.callTool({ name: "question_pending", arguments: {} })) as Array<{ answer_id: string }>;
    expect(pending.map((p) => p.answer_id)).toEqual([aid]);

    await client.callTool({ name: "question_ack", arguments: { question_id: "Q-0001", answer_id: aid } });
    const pending2 = text(await client.callTool({ name: "question_pending", arguments: {} })) as unknown[];
    expect(pending2.length).toBe(1);

    const applied = text(await client.callTool({ name: "question_apply", arguments: { question_id: "Q-0001", answer_id: aid, decision: { title: "A にする", body: "理由", chosen: "a" } } })) as { status: string; decision_id: string };
    expect(applied.status).toBe("applied");

    // record_put cannot forge an answer-derived decision
    const forged = await client.callTool({ name: "record_put", arguments: { kind: "decision", title: "偽", body: "", frontmatter: { answer: aid } } });
    expect(forged.isError).toBe(true);

    const tr = text(await client.callTool({ name: "trace", arguments: { id: rec.id } })) as { decisions: Array<{ id: string; committed: boolean }> };
    expect(tr.decisions).toEqual([expect.objectContaining({ id: applied.decision_id, committed: true })]);

    const st = text(await client.callTool({ name: "status", arguments: {} })) as { pending: unknown[] };
    expect(st.pending).toEqual([]);
  }, 60_000);
});
