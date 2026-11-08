import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { z } from "zod";
import { ApiClient, ApiError } from "./client.js";

export { ApiClient, ApiError, ensureDaemon, readDaemonInfo, toolRepoRoot } from "./client.js";

type ToolResult = { content: Array<{ type: "text"; text: string }>; isError?: boolean };

function ok(data: unknown): ToolResult {
  return { content: [{ type: "text", text: JSON.stringify(data, null, 2) }] };
}

function fail(e: unknown): ToolResult {
  if (e instanceof ApiError) {
    return { isError: true, content: [{ type: "text", text: JSON.stringify({ error: e.code, message: e.message, details: e.details }, null, 2) }] };
  }
  return { isError: true, content: [{ type: "text", text: JSON.stringify({ error: "internal", message: e instanceof Error ? e.message : String(e) }) }] };
}

async function guard(fn: () => Promise<unknown>): Promise<ToolResult> {
  try {
    return ok(await fn());
  } catch (e) {
    return fail(e);
  }
}

/** MCP server exposing the prepare-dev runtime. All state changes go through the daemon's HTTP API. */
export function createMcpServer(client: ApiClient, host = "unknown"): McpServer {
  const server = new McpServer({ name: "prepare-dev", version: "0.0.1" });
  const q = (p: string): string => `/api/questions/${encodeURIComponent(p)}`;

  server.registerTool(
    "status",
    {
      description:
        "Project overview: unprocessed answers (regardless of ack), decisions whose reviewed versions no longer match, verification runs that no longer apply to the current versions, running jobs.",
      inputSchema: {},
    },
    () => guard(() => client.get("/api/status")),
  );

  server.registerTool(
    "record_list",
    { description: "List records (requirements, decisions, specs, ...). Provisional decisions are hidden.", inputSchema: { kind: z.string().optional() } },
    ({ kind }) => guard(() => client.get(`/api/records${kind ? `?kind=${encodeURIComponent(kind)}` : ""}`)),
  );

  server.registerTool(
    "record_get",
    { description: "Read one record with its current rev and sha256 (needed for record_put).", inputSchema: { id: z.string() } },
    ({ id }) => guard(() => client.get(`/api/records/${encodeURIComponent(id)}`)),
  );

  server.registerTool(
    "record_put",
    {
      description:
        "Create (omit id) or update a markdown record. Updates must pass expected_rev and expected_sha256 from record_get; they are checked against the actual file. Decisions derived from an answer cannot be written here: use question_apply.",
      inputSchema: {
        id: z.string().optional(),
        kind: z.string().optional(),
        title: z.string().optional(),
        frontmatter: z.record(z.string(), z.unknown()).optional(),
        body: z.string().optional(),
        expected_rev: z.number().int().optional(),
        expected_sha256: z.string().optional(),
      },
    },
    (input) =>
      guard(async () => {
        if (!input.id) {
          if (!input.kind || !input.title) throw new Error("kind and title are required to create a record");
          return client.post("/api/records", { kind: input.kind, title: input.title, frontmatter: input.frontmatter ?? {}, body: input.body ?? "" });
        }
        if (input.expected_rev === undefined || !input.expected_sha256) throw new Error("expected_rev and expected_sha256 are required to update");
        const fm = { ...(input.frontmatter ?? {}), ...(input.title ? { title: input.title } : {}) };
        return client.put(`/api/records/${encodeURIComponent(input.id)}`, {
          frontmatter: fm,
          body: input.body,
          expected_rev: input.expected_rev,
          expected_sha256: input.expected_sha256,
        });
      }),
  );

  server.registerTool(
    "question_create",
    {
      description:
        "Ask the human a question through the local UI. Prepare candidates and a recommendation first. subjects are record ids whose current versions the answer will be bound to. Returns question_id and the UI url.",
      inputSchema: {
        kind: z.enum(["single_choice", "multi_choice", "free_text", "review"]),
        prompt: z.string(),
        subjects: z.array(z.string()).min(1),
        options: z
          .array(z.object({ key: z.string(), label: z.string(), summary: z.string().optional(), recommended: z.boolean().optional(), reason: z.string().optional() }))
          .default([]),
        allow_free_text: z.boolean().default(true),
        ui: z.unknown().optional(),
      },
    },
    (input) => guard(() => client.post("/api/questions", { ...input, created_by: { host } })),
  );

  server.registerTool(
    "question_wait",
    {
      description:
        "Block until the question has an unprocessed answer or timeout_ms elapses (returns status pending; call again). Answers are persisted before being returned, so a dropped call loses nothing: see question_pending.",
      inputSchema: { question_id: z.string(), timeout_ms: z.number().int().min(0).max(3_600_000).optional() },
    },
    ({ question_id, timeout_ms }) =>
      guard(() => client.get(`${q(question_id)}/wait${timeout_ms !== undefined ? `?timeout_ms=${timeout_ms}` : ""}`, (timeout_ms ?? 60_000) + 10_000)),
  );

  server.registerTool(
    "question_pending",
    {
      description:
        "All unprocessed answers: neither confirmed by question_apply nor closed, whether or not they were acknowledged. Stale answers (subject changed since the question) are included for re-confirmation. Use this after a restart.",
      inputSchema: {},
    },
    () => guard(() => client.get("/api/pending")),
  );

  server.registerTool(
    "question_ack",
    { description: "Record that you received an answer. This does not remove it from question_pending.", inputSchema: { question_id: z.string(), answer_id: z.string() } },
    ({ question_id, answer_id }) => guard(() => client.post(`${q(question_id)}/answers/${encodeURIComponent(answer_id)}/ack`, {})),
  );

  server.registerTool(
    "question_apply",
    {
      description:
        "Confirm an answer as a decision (DEC record) in one logical commit. Rejected if the subjects changed since the question (stale) or the answer was closed. Retrying with the same answer_id returns the existing decision.",
      inputSchema: {
        question_id: z.string(),
        answer_id: z.string(),
        decision: z.object({ title: z.string(), body: z.string(), chosen: z.string().optional(), alternatives: z.array(z.string()).default([]) }),
        expected_question_rev: z.number().int().optional(),
      },
    },
    ({ question_id, answer_id, decision, expected_question_rev }) =>
      guard(() => client.post(`${q(question_id)}/answers/${encodeURIComponent(answer_id)}/apply`, { decision, expected_question_rev })),
  );

  server.registerTool(
    "question_close",
    { description: "Mark an answer as handled without applying it (reason required).", inputSchema: { question_id: z.string(), answer_id: z.string(), reason: z.string() } },
    ({ question_id, answer_id, reason }) => guard(() => client.post(`${q(question_id)}/answers/${encodeURIComponent(answer_id)}/close`, { reason })),
  );

  server.registerTool(
    "verify_run",
    {
      description:
        "Start a verification job with a plugin (e.g. lean4, vitest). claims are {spec, claim} pairs whose spec frontmatter has a native ref for the plugin. Returns run_id; the result is produced by the tool's actual output, never by this text.",
      inputSchema: {
        plugin: z.string(),
        claims: z.array(z.object({ spec: z.string(), claim: z.string() })).min(1),
        targets: z.array(z.string()).default([]),
        config: z.record(z.string(), z.unknown()).default({}),
      },
    },
    (input) => guard(() => client.post("/api/runs", input)),
  );

  server.registerTool(
    "verify_wait",
    { description: "Wait for a run to finish (or timeout_ms) and return the evidence record with its applicability to the current versions.", inputSchema: { run_id: z.string(), timeout_ms: z.number().int().optional() } },
    ({ run_id, timeout_ms }) => guard(() => client.get(`/api/runs/${encodeURIComponent(run_id)}/wait${timeout_ms !== undefined ? `?timeout_ms=${timeout_ms}` : ""}`, (timeout_ms ?? 50_000) + 10_000)),
  );

  server.registerTool(
    "verify_get",
    { description: "Read a verification run record.", inputSchema: { run_id: z.string() } },
    ({ run_id }) => guard(() => client.get(`/api/runs/${encodeURIComponent(run_id)}`)),
  );

  server.registerTool(
    "trace",
    {
      description: "From a requirement or spec id: its questions, confirmed decisions (with whether reviewed versions still match), specs, claims and verification runs with applicability.",
      inputSchema: { id: z.string() },
    },
    ({ id }) => guard(() => client.get(`/api/trace/${encodeURIComponent(id)}`)),
  );

  return server;
}

export async function serveStdio(root: string, host?: string): Promise<void> {
  const client = await ApiClient.connect(root);
  const server = createMcpServer(client, host);
  const transport = new StdioServerTransport();
  await server.connect(transport);
}
