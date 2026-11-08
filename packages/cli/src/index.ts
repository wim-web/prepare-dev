#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";
import { spawn } from "node:child_process";
import { Runtime, Server, type ApplyStage } from "@prepare-dev/runtime";
import { ApiClient, ApiError, ensureDaemon, serveStdio, stopDaemon, toolPaths, canonicalRoot } from "@prepare-dev/mcp";
import { lean4Plugin } from "@prepare-dev/plugin-lean4";
import { vitestPlugin } from "@prepare-dev/plugin-vitest";
import { acquireLease } from "./lease.js";
import { copySkills } from "./skills.js";

interface Args {
  _: string[];
  flags: Record<string, string | boolean | string[]>;
}

function parseArgs(argv: string[]): Args {
  const out: Args = { _: [], flags: {} };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i]!;
    if (a.startsWith("--")) {
      let key = a.slice(2);
      let val: string | boolean = true;
      const eq = key.indexOf("=");
      if (eq >= 0) {
        val = key.slice(eq + 1);
        key = key.slice(0, eq);
      } else if (i + 1 < argv.length && !argv[i + 1]!.startsWith("--")) {
        val = argv[++i]!;
      }
      const prev = out.flags[key];
      if (prev === undefined) out.flags[key] = val;
      else if (Array.isArray(prev)) prev.push(String(val));
      else out.flags[key] = [String(prev), String(val)];
    } else out._.push(a);
  }
  return out;
}

const str = (v: string | boolean | string[] | undefined, dflt?: string): string | undefined =>
  v === undefined ? dflt : Array.isArray(v) ? v[v.length - 1] : typeof v === "boolean" ? dflt : v;
const list = (v: string | boolean | string[] | undefined): string[] => (v === undefined || typeof v === "boolean" ? [] : Array.isArray(v) ? v : [v]);

function rootOf(args: Args): string {
  return path.resolve(str(args.flags.root) ?? process.env.PREPARE_DEV_ROOT ?? process.cwd());
}

function print(data: unknown): void {
  process.stdout.write(`${typeof data === "string" ? data : JSON.stringify(data, null, 2)}\n`);
}

function die(msg: string, code = 1): never {
  process.stderr.write(`error: ${msg}\n`);
  process.exit(code);
}

function readBody(args: Args): string {
  const file = str(args.flags["body-file"]);
  if (file) return fs.readFileSync(file, "utf8");
  return str(args.flags.body) ?? "";
}

async function cmdServe(args: Args): Promise<void> {
  const root = canonicalRoot(rootOf(args));
  if (args.flags.detach) return print(await ensureDaemon(root));
  const paths = toolPaths();
  if (!fs.existsSync(paths.ui)) die(`UI is missing: ${paths.ui}; rebuild or reinstall prepare-dev`);
  const release = await acquireLease(root);
  try {
    const rt = Runtime.open(root, { plugins: [lean4Plugin, vitestPlugin] });
    const crashAfter = (str(args.flags["crash-after"]) ?? process.env.PREPARE_DEV_CRASH_AFTER ?? null) as ApplyStage | null;
    const stop = async (): Promise<void> => { await server.close(); await release(); process.exit(0); };
    const server = new Server(rt, { port: Number(str(args.flags.port) ?? 0), uiHtml: paths.ui, crashAfter, tool: paths.identity, onShutdown: stop });
    print({ ...await server.listen(), recovery: rt.recovery, plugins: rt.runs.pluginNames() });
    process.once("SIGINT", () => void stop());
    process.once("SIGTERM", () => void stop());
  } catch (error) { await release(); throw error; }
}

async function cmdInit(args: Args): Promise<void> {
  const root = rootOf(args);
  fs.mkdirSync(root, { recursive: true });
  const stateDir = path.join(root, ".prepare-dev");
  fs.mkdirSync(stateDir, { recursive: true });
  const cfgFile = path.join(stateDir, "config.json");
  if (!fs.existsSync(cfgFile)) {
    try { fs.writeFileSync(cfgFile, JSON.stringify({ records_dir: "prepare", wait: { default_timeout_ms: 50_000 }, plugins: {} }, null, 2) + "\n", { flag: "wx" }); }
    catch (error) { if (!(error instanceof Error && "code" in error && error.code === "EEXIST")) throw error; }
  }
  const paths = toolPaths();
  const destinations = args.flags["no-skills"] ? [] : args.flags["skills-dir"]
    ? list(args.flags["skills-dir"]).map((dir) => path.resolve(root, dir))
    : [path.join(root, ".agents/skills"), path.join(root, ".claude/skills")];
  const skills = copySkills(paths.skills, destinations, root);
  const info = await ensureDaemon(root);
  print({ root: info.root, initialized: true, skills, ui: info.url, tool: paths.identity });
}

async function client(args: Args): Promise<ApiClient> {
  return ApiClient.connect(rootOf(args), { start: args.flags["no-start"] ? false : true });
}

async function cmdStatus(args: Args): Promise<void> {
  const c = await client(args);
  const s = await c.get<Record<string, unknown>>("/api/status");
  if (args.flags.json) return print(s);
  const pending = s.pending as Array<Record<string, unknown>>;
  const findings = (s.open_findings ?? []) as Array<{ review_id: string; finding_id: string; severity: string; subjects: string[]; summary: string }>;
  const waiting = (s.waiting_questions ?? []) as Array<{ question_id: string; subjects: string[]; subjects_current: boolean }>;
  const bySeverity = (sev: string) => findings.filter((f) => f.severity === sev).length;
  const problems = (s.problems ?? []) as Array<{ path: string; message: string }>;
  const lines = [
    `root: ${s.root}`,
    `pending answers: ${pending.length}`,
    ...pending.map((p) => `  ${p.question_id} ${p.answer_id} stale=${p.stale} received=${p.received} choice=${JSON.stringify(p.choice)}`),
    `inflight: ${(s.inflight as unknown[]).length}`,
    `stale decisions: ${(s.stale_decisions as unknown[]).map((d) => (d as { id: string }).id).join(", ") || "none"}`,
    `runs current: ${(s.runs_current as unknown[]).length}, not applicable to current versions: ${(s.runs_not_applicable as unknown[]).length}`,
    ...(s.runs_not_applicable as Array<Record<string, unknown>>).map((r) => `  ${r.run_id} ${r.result} (${(r.mismatches as unknown[]).length} mismatches)`),
    `jobs: ${(s.jobs as unknown[]).length}`,
    `questions waiting for an answer: ${waiting.length}`,
    ...waiting.map((q) => `  ${q.question_id} (${q.subjects.join(", ")})${q.subjects_current ? "" : " subjects changed since asked"}`),
    `open review findings: ${findings.length} (blocker ${bySeverity("blocker")}, major ${bySeverity("major")}, nit ${bySeverity("nit")})`,
    ...findings.map((f) => `  ${f.review_id}/${f.finding_id} ${f.severity} [${f.subjects.join(", ")}] ${f.summary.length > 70 ? `${f.summary.slice(0, 70)}…` : f.summary}`),
    ...(problems.length ? [`problems: ${problems.length}`, ...problems.map((p) => `  ${p.path}: ${p.message}`)] : []),
  ];
  print(lines.join("\n"));
}

async function cmdQ(args: Args): Promise<void> {
  const c = await client(args);
  const [sub, id, aid] = args._.slice(1);
  switch (sub) {
    case "create": {
      const options = args.flags["options-file"]
        ? JSON.parse(fs.readFileSync(str(args.flags["options-file"])!, "utf8")) as unknown[]
        : args.flags["options-json"]
        ? (JSON.parse(str(args.flags["options-json"])!) as unknown[])
        : list(args.flags.option).map((o) => {
            const [key, rest = ""] = o.split("=", 2);
            const [label, flag] = rest.split(":");
            return { key: key!, label: label || key!, recommended: flag === "recommended" };
          });
      return print(
        await c.post("/api/questions", {
          kind: str(args.flags.kind) ?? "single_choice",
          prompt: args.flags["prompt-file"] ? fs.readFileSync(str(args.flags["prompt-file"])!, "utf8") : str(args.flags.prompt) ?? die("--prompt or --prompt-file is required"),
          subjects: list(args.flags.subject),
          options,
          allow_free_text: !args.flags["no-free-text"],
          created_by: { host: "cli" },
        }),
      );
    }
    case "list":
      return print(await c.get("/api/questions"));
    case "show":
      return print(await c.get(`/api/questions/${id}`));
    case "answer":
      return print(await c.post(`/api/questions/${id}/answers`, { choice: list(args.flags.choice), text: str(args.flags.text) ?? null }));
    case "wait": {
      const t = Number(str(args.flags.timeout) ?? 50_000);
      return print(await c.get(`/api/questions/${id}/wait?timeout_ms=${t}`, t + 10_000));
    }
    case "pending":
      return print(await c.get("/api/pending"));
    case "ack":
      return print(await c.post(`/api/questions/${id}/answers/${aid}/ack`, {}));
    case "apply":
      return print(
        await c.post(`/api/questions/${id}/answers/${aid}/apply`, {
          decision: {
            title: str(args.flags.title) ?? die("--title is required"),
            body: readBody(args),
            chosen: str(args.flags.chosen),
            alternatives: list(args.flags.alternative),
          },
          expected_question_rev: args.flags["expected-rev"] ? Number(str(args.flags["expected-rev"])) : undefined,
        }),
      );
    case "close":
      return print(await c.post(`/api/questions/${id}/answers/${aid}/close`, { reason: str(args.flags.reason) ?? die("--reason is required") }));
    case "cancel":
      return print(
        await c.post(`/api/questions/${id}/cancel`, { reason: str(args.flags.reason) ?? die("--reason is required"), replaced_by: str(args.flags["replaced-by"]) ?? null }),
      );
    default:
      die("usage: q create|list|show|answer|wait|pending|ack|apply|close|cancel");
  }
}

async function cmdRecord(args: Args): Promise<void> {
  const c = await client(args);
  const [sub, id] = args._.slice(1);
  const fm: Record<string, unknown> = {};
  const fmFile = str(args.flags["fm-file"]);
  if (fmFile) {
    const parsed = JSON.parse(fs.readFileSync(fmFile, "utf8")) as unknown;
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) die("--fm-file must contain a JSON object");
    Object.assign(fm, parsed as Record<string, unknown>);
  }
  for (const kv of list(args.flags.fm)) {
    const eq = kv.indexOf("=");
    const k = eq < 0 ? kv : kv.slice(0, eq);
    const v = eq < 0 ? "" : kv.slice(eq + 1);
    if (!k) continue;
    fm[k] = v && (v.startsWith("[") || v.startsWith("{")) ? (JSON.parse(v) as unknown) : v;
  }
  switch (sub) {
    case "list":
      return print(await c.get(`/api/records${args.flags.kind ? `?kind=${str(args.flags.kind)}` : ""}`));
    case "get":
      return print(await c.get(`/api/records/${id}`));
    case "create":
      return print(await c.post("/api/records", { kind: str(args.flags.kind) ?? die("--kind"), title: str(args.flags.title) ?? die("--title"), frontmatter: fm, body: readBody(args) }));
    case "put": {
      const expectedRev = Number(str(args.flags["expected-rev"]) ?? die("record put requires --expected-rev and --expected-sha256 from record get"));
      const expectedSha = str(args.flags["expected-sha256"]) ?? die("record put requires --expected-rev and --expected-sha256 from record get");
      const cur = await c.get<{ body: string }>(`/api/records/${id}`);
      const body = args.flags["body-file"] || args.flags.body ? readBody(args) : cur.body;
      return print(await c.put(`/api/records/${id}`, { frontmatter: fm, body, expected_rev: expectedRev, expected_sha256: expectedSha }));
    }
    default:
      die("usage: record list|get|create|put");
  }
}

async function cmdVerify(args: Args): Promise<void> {
  const c = await client(args);
  const [sub, id] = args._.slice(1);
  switch (sub) {
    case "run": {
      const claims = list(args.flags.claim).map((s) => {
        const [spec, claim] = s.split("/");
        if (!spec || !claim) die(`--claim must be SPEC-ID/CLAIM, got ${s}`);
        return { spec, claim };
      });
      const config = args.flags["config-json"] ? (JSON.parse(str(args.flags["config-json"])!) as Record<string, unknown>) : {};
      const job = await c.post<{ run_id: string }>("/api/runs", { plugin: str(args.flags.plugin) ?? die("--plugin"), claims, targets: list(args.flags.target), config });
      if (!args.flags.wait) return print(job);
      const t = Number(str(args.flags.timeout) ?? 600_000);
      return print(await c.get(`/api/runs/${job.run_id}/wait?timeout_ms=${t}`, t + 10_000));
    }
    case "wait": {
      const t = Number(str(args.flags.timeout) ?? 600_000);
      return print(await c.get(`/api/runs/${id}/wait?timeout_ms=${t}`, t + 10_000));
    }
    case "get":
      return print(await c.get(`/api/runs/${id}`));
    case "list":
      return print(await c.get("/api/runs"));
    default:
      die("usage: verify run|wait|get|list");
  }
}

async function cmdUi(args: Args): Promise<void> {
  const info = await ensureDaemon(rootOf(args));
  const id = args._[1];
  const url = id ? `${info.url}/q/${id}` : info.url;
  print(url);
  if (!args.flags["no-open"] && process.platform === "darwin") spawn("open", [url], { stdio: "ignore", detached: true }).unref();
}

async function main(): Promise<void> {
  const args = parseArgs(process.argv.slice(2));
  const cmd = args._[0];
  if (args.flags.version || cmd === "version") return print(args.flags.json ? toolPaths().identity : toolPaths().identity.version);
  if (args.flags.help || cmd === "help" || !cmd) return print(`prepare-dev — 意図から判断・仕様・検証へ

prepare-dev init [--root dir] [--no-skills] [--skills-dir dir]
prepare-dev status [--json] [--root dir]
prepare-dev ui [Q-ID] [--no-open] [--root dir]
prepare-dev record create --kind requirement|spec|design|discovery|review --title text --body-file file [--fm-file json]
prepare-dev record get ID
prepare-dev record put ID --expected-rev N --expected-sha256 HASH [--body-file file] [--fm-file json]
prepare-dev record list [--kind kind]
prepare-dev q create --subject REQ-ID --prompt-file question.md [--options-file options.json]
  --kind single_choice|multi_choice|free_text|review (default: single_choice) [--no-free-text]
prepare-dev q answer Q-ID [--choice key (repeatable)] [--text text]
prepare-dev q apply Q-ID ANSWER-ID --title text --body-file file [--chosen key] [--alternative text]
prepare-dev q show|answer|wait|pending|ack|apply|close|cancel ...
prepare-dev verify run --plugin vitest --claim SPEC-ID/CLAIM --target test.ts --wait
prepare-dev trace REQ-ID
prepare-dev serve [--detach] | stop
prepare-dev bundle RECORD-ID... [--stdout]
prepare-dev guidance lean4|vitest
prepare-dev mcp   (任意の接続経路)

全操作で --root dir を指定できます。省略時は PREPARE_DEV_ROOT または現在のディレクトリ。
init はMCP設定を変更しません。skillsの既存編集は保持し、差異を出力します。
検証器は対象プロジェクトへ別途導入してください。`);
  try {
    switch (cmd) {
      case "serve":
        return await cmdServe(args);
      case "init":
        return await cmdInit(args);
      case "mcp":
        return await serveStdio(rootOf(args), str(args.flags.host));
      case "status":
        return await cmdStatus(args);
      case "pending":
        return print(await (await client(args)).get("/api/pending"));
      case "q":
        return await cmdQ(args);
      case "record":
        return await cmdRecord(args);
      case "verify":
        return await cmdVerify(args);
      case "trace":
        return print(await (await client(args)).get(`/api/trace/${args._[1] ?? die("trace <id>")}`));
      case "bundle": {
        const ids = args._.slice(1);
        if (!ids.length) die("bundle <id...> [--stdout] [--no-expand] [--title t]");
        const qs = new URLSearchParams({ ids: ids.join(","), out: args.flags.stdout ? "0" : "1", expand: args.flags["no-expand"] ? "0" : "1" });
        if (args.flags.title) qs.set("title", str(args.flags.title)!);
        const b = await (await client(args)).get<{ inputs: unknown; markdown: string; path: string | null }>(`/api/bundle?${qs.toString()}`);
        return print(args.flags.stdout ? b.markdown : { inputs: b.inputs, path: b.path });
      }
      case "ui":
        return await cmdUi(args);
      case "stop":
        return print(await stopDaemon(rootOf(args)) ? "daemon stopped" : "no daemon running");
      case "guidance": {
        const name = args._[1];
        if (name !== "lean4" && name !== "vitest") die("guidance lean4|vitest");
        const paths = toolPaths();
        const file = paths.identity.mode === "package" ? path.join(paths.guidance, `${name}.md`) : path.join(paths.guidance, name, "guidance", `${name}.md`);
        return print(fs.readFileSync(file, "utf8"));
      }

      default:
        die("usage: prepare-dev serve|init|mcp|status|pending|q|record|verify|trace|ui|stop [--root dir]");
    }
  } catch (e) {
    if (e instanceof ApiError) die(`${e.code}: ${e.message}${e.details ? ` ${JSON.stringify(e.details)}` : ""}`);
    die(e instanceof Error ? e.message : String(e));
  }
}

void main();
