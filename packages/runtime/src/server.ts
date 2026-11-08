import fs from "node:fs";
import http from "node:http";
import path from "node:path";
import { DecisionDraft, QuestionKind, QuestionOption, nowIso } from "@prepare-dev/core";
import { z } from "zod";
import { RuntimeError, SimulatedCrash } from "./errors.js";
import type { ApplyStage } from "./questions.js";
import type { Runtime } from "./runtime.js";
import { questionSpec } from "./ui-spec.js";
import { writeFileAtomic } from "./store.js";

export interface ServerOptions {
  port?: number;
  host?: string;
  /** Path to the built single-file UI (packages/ui/dist/index.html). */
  uiHtml?: string;
  /** For end-to-end crash tests: exit the process right after this apply stage. */
  crashAfter?: ApplyStage | null;
}

export interface DaemonInfo {
  pid: number;
  port: number;
  url: string;
  root: string;
  started_at: string;
}

const CreateRecordBody = z.object({
  kind: z.string(),
  title: z.string(),
  frontmatter: z.record(z.string(), z.unknown()).default({}),
  body: z.string().default(""),
});

const UpdateRecordBody = z.object({
  frontmatter: z.record(z.string(), z.unknown()).default({}),
  body: z.string().optional(),
  expected_rev: z.number().int(),
  expected_sha256: z.string(),
});

const CreateQuestionBody = z.object({
  kind: QuestionKind,
  prompt: z.string(),
  subjects: z.array(z.string()),
  options: z.array(QuestionOption).default([]),
  allow_free_text: z.boolean().default(true),
  ui: z.unknown().optional(),
  created_by: z.object({ host: z.string().optional(), session: z.string().optional() }).optional(),
});

const AnswerBody = z.object({
  choice: z.union([z.array(z.string()), z.string(), z.null()]).optional(),
  text: z.string().nullable().optional(),
});

const ApplyBody = z.object({
  decision: DecisionDraft,
  expected_question_rev: z.number().int().optional(),
});

const CloseBody = z.object({ reason: z.string() });

const RunBody = z.object({
  plugin: z.string(),
  claims: z.array(z.object({ spec: z.string(), claim: z.string() })),
  targets: z.array(z.string()).default([]),
  config: z.record(z.string(), z.unknown()).default({}),
});

type Handler = (req: http.IncomingMessage, res: http.ServerResponse, params: Record<string, string>, url: URL) => Promise<void> | void;

interface Route {
  method: string;
  pattern: RegExp;
  keys: string[];
  handler: Handler;
}

export class Server {
  private readonly routes: Route[] = [];
  private readonly sseClients = new Set<http.ServerResponse>();
  private server: http.Server | null = null;
  private heartbeat: NodeJS.Timeout | null = null;
  readonly daemonFile: string;
  info: DaemonInfo | null = null;

  constructor(
    readonly runtime: Runtime,
    readonly options: ServerOptions = {},
  ) {
    this.daemonFile = path.join(runtime.store.stateDir, "daemon.json");
    this.defineRoutes();
    const forward = (name: string) => (payload: unknown) => this.broadcast(name, payload);
    runtime.store.emitter.on("event", forward("event"));
    runtime.store.emitter.on("question", forward("question"));
    runtime.store.emitter.on("job", forward("job"));
    runtime.store.emitter.on("run", forward("run"));
  }

  // ---------- routing ----------

  private add(method: string, pattern: string, handler: Handler): void {
    const keys: string[] = [];
    const re = new RegExp(
      `^${pattern.replace(/:([a-zA-Z_]+)/g, (_m, k: string) => {
        keys.push(k);
        return "([^/]+)";
      })}/?$`,
    );
    this.routes.push({ method, pattern: re, keys, handler });
  }

  private defineRoutes(): void {
    const rt = this.runtime;
    const q = rt.questions;

    this.add("GET", "/api/status", (_req, res) => json(res, 200, rt.status.view()));

    this.add("GET", "/api/records", (_req, res, _p, url) => {
      const kind = url.searchParams.get("kind") ?? undefined;
      const list = rt.store.listRecords(kind as never).filter((r) => r.kind !== "decision" || rt.trace.isPublic(r));
      json(
        res,
        200,
        list.map((r) => ({ id: r.id, kind: r.kind, title: r.frontmatter.title, status: r.frontmatter.status, rev: r.rev, sha256: r.sha256, path: r.path })),
      );
    });

    this.add("GET", "/api/records/:id", (_req, res, p) => {
      const r = rt.store.readRecord(p.id!);
      if (r.kind === "decision" && !rt.trace.isPublic(r)) throw new RuntimeError("not_found", `decision ${p.id} is provisional`);
      json(res, 200, r);
    });

    this.add("POST", "/api/records", async (req, res) => {
      const body = CreateRecordBody.parse(await readJson(req));
      forbidAnswerDecision(body.frontmatter, body.kind);
      const r = rt.store.createRecord(body.kind as never, { ...body.frontmatter, title: body.title }, body.body);
      json(res, 201, r);
    });

    this.add("PUT", "/api/records/:id", async (req, res, p) => {
      const body = UpdateRecordBody.parse(await readJson(req));
      const cur = rt.store.readRecord(p.id!);
      forbidAnswerDecision({ ...cur.frontmatter, ...body.frontmatter }, cur.kind);
      if (cur.frontmatter.txid) throw new RuntimeError("forbidden", "decisions created from answers are confirmed through question_apply only");
      const r = rt.store.writeRecord(p.id!, body.frontmatter, body.body ?? cur.body, { rev: body.expected_rev, sha256: body.expected_sha256 });
      json(res, 200, r);
    });

    this.add("GET", "/api/questions", (_req, res, _p, url) => {
      const needs = url.searchParams.get("needs_answer") === "1";
      let list = q.list();
      if (needs) list = list.filter((x) => x.state !== "cancelled" && !x.answers.some((a) => !a.closed && !a.stale));
      json(res, 200, list);
    });

    this.add("GET", "/api/pending", (_req, res) => json(res, 200, q.pending()));

    this.add("POST", "/api/questions", async (req, res) => {
      const body = CreateQuestionBody.parse(await readJson(req));
      const created = q.create(body);
      json(res, 201, { question: created, url: this.questionUrl(created.id) });
    });

    this.add("GET", "/api/questions/:id", (_req, res, p) => json(res, 200, q.read(p.id!)));

    this.add("GET", "/api/questions/:id/ui", (_req, res, p) => json(res, 200, questionSpec(q.read(p.id!))));

    this.add("POST", "/api/questions/:id/answers", async (req, res, p) => {
      const body = AnswerBody.parse(await readJson(req));
      const choice = body.choice === undefined || body.choice === null ? null : Array.isArray(body.choice) ? body.choice : [body.choice];
      const r = q.answer(p.id!, { choice, text: body.text ?? null });
      json(res, 201, r);
    });

    this.add("GET", "/api/questions/:id/wait", async (req, res, p, url) => {
      const timeout = Number(url.searchParams.get("timeout_ms") ?? rt.store.config.wait.default_timeout_ms);
      const ac = new AbortController();
      req.on("close", () => ac.abort());
      const r = await q.wait(p.id!, Math.max(0, Math.min(timeout, 3_600_000)), ac.signal);
      json(res, 200, r);
    });

    this.add("POST", "/api/questions/:id/answers/:aid/ack", (_req, res, p) => json(res, 200, q.ack(p.id!, p.aid!)));

    this.add("POST", "/api/questions/:id/answers/:aid/apply", async (req, res, p) => {
      const body = ApplyBody.parse(await readJson(req));
      const crashAfter = this.options.crashAfter ?? (process.env.PREPARE_DEV_CRASH_AFTER as ApplyStage | undefined) ?? null;
      const hooks = crashAfter
        ? {
            afterStage: (stage: ApplyStage) => {
              if (stage === crashAfter) {
                // Simulate the daemon dying right after this persistence stage.
                process.stderr.write(`[prepare-dev] simulated crash after stage ${stage}\n`);
                process.exit(42);
                throw new SimulatedCrash(stage);
              }
            },
          }
        : undefined;
      const r = q.apply(p.id!, p.aid!, body, hooks);
      json(res, 200, r);
    });

    this.add("POST", "/api/questions/:id/answers/:aid/close", async (req, res, p) => {
      const body = CloseBody.parse(await readJson(req));
      json(res, 200, q.close(p.id!, p.aid!, body.reason));
    });

    this.add("GET", "/api/runs", (_req, res) =>
      json(
        res,
        200,
        rt.runs.list().map((r) => ({ ...r, applicability: rt.runs.applicability(r) })),
      ),
    );

    this.add("POST", "/api/runs", async (req, res) => {
      const body = RunBody.parse(await readJson(req));
      json(res, 202, rt.runs.start(body));
    });

    this.add("GET", "/api/runs/:id", (_req, res, p) => {
      const r = rt.runs.get(p.id!);
      json(res, 200, { ...r, applicability: rt.runs.applicability(r) });
    });

    this.add("GET", "/api/runs/:id/wait", async (_req, res, p, url) => {
      const timeout = Number(url.searchParams.get("timeout_ms") ?? 50_000);
      const r = await rt.runs.wait(p.id!, Math.max(0, Math.min(timeout, 3_600_000)));
      json(res, 200, { ...r, applicability: r.record ? rt.runs.applicability(r.record) : null });
    });

    this.add("GET", "/api/trace/:id", (_req, res, p) => json(res, 200, rt.trace.for(p.id!)));

    this.add("GET", "/api/plugins", (_req, res) => json(res, 200, rt.runs.pluginNames()));

    this.add("GET", "/events", (_req, res) => this.sse(res));

    this.add("GET", "/", (_req, res) => this.ui(res));
    this.add("GET", "/q/:id", (_req, res) => this.ui(res));
  }

  questionUrl(id: string): string {
    return `${this.info?.url ?? "http://127.0.0.1"}/q/${id}`;
  }

  // ---------- SSE ----------

  private sse(res: http.ServerResponse): void {
    res.writeHead(200, {
      "content-type": "text/event-stream",
      "cache-control": "no-cache",
      connection: "keep-alive",
      "access-control-allow-origin": "*",
    });
    res.write(`event: hello\ndata: ${JSON.stringify({ ts: nowIso() })}\n\n`);
    this.sseClients.add(res);
    res.on("close", () => this.sseClients.delete(res));
  }

  private broadcast(name: string, payload: unknown): void {
    const msg = `event: ${name}\ndata: ${JSON.stringify(payload)}\n\n`;
    for (const c of this.sseClients) c.write(msg);
  }

  // ---------- UI ----------

  private ui(res: http.ServerResponse): void {
    const file = this.options.uiHtml;
    if (file && fs.existsSync(file)) {
      res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
      res.end(fs.readFileSync(file));
      return;
    }
    res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
    res.end(FALLBACK_HTML);
  }

  // ---------- lifecycle ----------

  async listen(): Promise<DaemonInfo> {
    const host = this.options.host ?? "127.0.0.1";
    const server = http.createServer((req, res) => void this.handle(req, res));
    this.server = server;
    await new Promise<void>((resolve, reject) => {
      server.once("error", reject);
      server.listen(this.options.port ?? 0, host, () => resolve());
    });
    const addr = server.address();
    const port = typeof addr === "object" && addr ? addr.port : 0;
    this.info = { pid: process.pid, port, url: `http://${host}:${port}`, root: this.runtime.root, started_at: nowIso() };
    writeFileAtomic(this.daemonFile, `${JSON.stringify(this.info, null, 2)}\n`);
    this.heartbeat = setInterval(() => this.broadcast("ping", { ts: nowIso() }), 15_000);
    this.heartbeat.unref();
    return this.info;
  }

  async close(): Promise<void> {
    if (this.heartbeat) clearInterval(this.heartbeat);
    for (const c of this.sseClients) c.end();
    this.sseClients.clear();
    try {
      const cur = JSON.parse(fs.readFileSync(this.daemonFile, "utf8")) as DaemonInfo;
      if (cur.pid === process.pid) fs.rmSync(this.daemonFile);
    } catch {
      // ignore
    }
    await new Promise<void>((resolve) => this.server?.close(() => resolve()));
  }

  private async handle(req: http.IncomingMessage, res: http.ServerResponse): Promise<void> {
    const url = new URL(req.url ?? "/", "http://localhost");
    if (req.method === "OPTIONS") {
      res.writeHead(204, corsHeaders());
      res.end();
      return;
    }
    for (const r of this.routes) {
      if (r.method !== req.method) continue;
      const m = r.pattern.exec(url.pathname);
      if (!m) continue;
      const params: Record<string, string> = {};
      r.keys.forEach((k, i) => (params[k] = decodeURIComponent(m[i + 1]!)));
      try {
        await r.handler(req, res, params, url);
      } catch (e) {
        this.error(res, e);
      }
      return;
    }
    json(res, 404, { error: "not_found", message: `no route ${req.method} ${url.pathname}` });
  }

  private error(res: http.ServerResponse, e: unknown): void {
    if (e instanceof RuntimeError) {
      json(res, e.httpStatus, { error: e.code, message: e.message, details: e.details });
    } else if (e instanceof z.ZodError) {
      json(res, 400, { error: "invalid", message: "request body is invalid", details: { issues: e.issues } });
    } else {
      json(res, 500, { error: "internal", message: e instanceof Error ? e.message : String(e) });
    }
  }
}

export function readDaemonInfo(root: string): DaemonInfo | null {
  const file = path.join(root, ".prepare-dev", "daemon.json");
  if (!fs.existsSync(file)) return null;
  try {
    const info = JSON.parse(fs.readFileSync(file, "utf8")) as DaemonInfo;
    process.kill(info.pid, 0);
    return info;
  } catch {
    return null;
  }
}

function corsHeaders(): Record<string, string> {
  return {
    "access-control-allow-origin": "*",
    "access-control-allow-methods": "GET,POST,PUT,OPTIONS",
    "access-control-allow-headers": "content-type",
  };
}

function json(res: http.ServerResponse, status: number, body: unknown): void {
  res.writeHead(status, { "content-type": "application/json; charset=utf-8", ...corsHeaders() });
  res.end(JSON.stringify(body));
}

async function readJson(req: http.IncomingMessage): Promise<unknown> {
  const chunks: Buffer[] = [];
  let size = 0;
  for await (const chunk of req) {
    size += (chunk as Buffer).length;
    if (size > 4 * 1024 * 1024) throw new RuntimeError("invalid", "request body too large");
    chunks.push(chunk as Buffer);
  }
  const text = Buffer.concat(chunks).toString("utf8");
  if (!text.trim()) return {};
  try {
    return JSON.parse(text);
  } catch {
    throw new RuntimeError("invalid", "request body is not valid JSON");
  }
}

function forbidAnswerDecision(fm: Record<string, unknown>, kind: string): void {
  if (kind === "decision" && (fm.answer || fm.txid || fm.question)) {
    throw new RuntimeError("forbidden", "decisions derived from an answer are confirmed through question_apply only");
  }
}

const FALLBACK_HTML = `<!doctype html>
<html lang="ja"><head><meta charset="utf-8"><title>prepare-dev</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>body{font-family:system-ui;margin:2rem;max-width:48rem}fieldset{margin:1rem 0;padding:1rem}label{display:block;margin:.25rem 0}</style></head>
<body><h1>prepare-dev（簡易UI）</h1><p>ビルド済みの UI が見つからないため、簡易フォームを表示しています。</p><div id="app">読み込み中…</div>
<script>
async function load(){const qs=await (await fetch('/api/questions?needs_answer=1')).json();const app=document.getElementById('app');app.innerHTML='';
if(!qs.length){app.textContent='回答待ちの質問はありません。';return}
for(const q of qs){const f=document.createElement('form');f.innerHTML='<fieldset><legend>'+q.id+'</legend><p>'+q.prompt+'</p>'+q.options.map(o=>'<label><input type="'+(q.kind==='multi_choice'?'checkbox':'radio')+'" name="c" value="'+o.key+'"> '+o.label+(o.recommended?'（推奨）':'')+(o.reason?' — '+o.reason:'')+'</label>').join('')+'<label>補足 <textarea name="t"></textarea></label><button>回答を送る</button></fieldset>';
f.onsubmit=async e=>{e.preventDefault();const fd=new FormData(f);const choice=fd.getAll('c');const text=fd.get('t')||null;const r=await fetch('/api/questions/'+q.id+'/answers',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({choice,text})});alert(r.ok?'送信しました':'失敗: '+await r.text());load()};app.appendChild(f)}}
load();new EventSource('/events').addEventListener('question',load);
</script></body></html>`;
