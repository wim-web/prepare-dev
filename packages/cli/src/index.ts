#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";
import { spawn } from "node:child_process";
import { Runtime, Server, type ApplyStage } from "@prepare-dev/runtime";
import { ApiClient, ApiError, ensureDaemon, readDaemonInfo, serveStdio, toolRepoRoot } from "@prepare-dev/mcp";
import { lean4Plugin } from "@prepare-dev/plugin-lean4";
import { vitestPlugin } from "@prepare-dev/plugin-vitest";

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

function uiHtmlPath(): string {
  return path.join(toolRepoRoot(), "packages", "ui", "dist", "index.html");
}

async function cmdServe(args: Args): Promise<void> {
  const root = rootOf(args);
  if (args.flags.detach) {
    await ensureDaemon(root);
    print(readDaemonInfo(root));
    return;
  }
  const existing = readDaemonInfo(root);
  if (existing) die(`a daemon is already running for ${root} (pid ${existing.pid}, ${existing.url})`);
  const rt = Runtime.open(root, { plugins: [lean4Plugin, vitestPlugin] });
  const crashAfter = (str(args.flags["crash-after"]) ?? process.env.PREPARE_DEV_CRASH_AFTER ?? null) as ApplyStage | null;
  const server = new Server(rt, { port: Number(str(args.flags.port) ?? 0), uiHtml: uiHtmlPath(), crashAfter });
  const info = await server.listen();
  print({ ...info, recovery: rt.recovery, plugins: rt.runs.pluginNames() });
  const stop = async (): Promise<void> => {
    await server.close();
    process.exit(0);
  };
  process.on("SIGINT", () => void stop());
  process.on("SIGTERM", () => void stop());
}

async function cmdInit(args: Args): Promise<void> {
  const root = rootOf(args);
  const repo = toolRepoRoot();
  const tsx = path.join(repo, "node_modules", ".bin", "tsx");
  const cli = path.join(repo, "packages", "cli", "src", "index.ts");
  const stateDir = path.join(root, ".prepare-dev");
  fs.mkdirSync(stateDir, { recursive: true });
  const cfgFile = path.join(stateDir, "config.json");
  if (!fs.existsSync(cfgFile)) fs.writeFileSync(cfgFile, `${JSON.stringify({ records_dir: "prepare", wait: { default_timeout_ms: 50_000 }, plugins: {} }, null, 2)}\n`);
  Runtime.open(root); // creates prepare/ and events.jsonl
  const written: string[] = [];
  const mcpJson = path.join(root, ".mcp.json");
  const entry = { type: "stdio", command: tsx, args: [cli, "mcp", "--root", root, "--host", "claude-code"] };
  if (!fs.existsSync(mcpJson)) {
    fs.writeFileSync(mcpJson, `${JSON.stringify({ mcpServers: { "prepare-dev": entry } }, null, 2)}\n`);
    written.push(".mcp.json");
  }
  const codexDir = path.join(root, ".codex");
  const codexCfg = path.join(codexDir, "config.toml");
  const block = [
    "",
    "[mcp_servers.prepare-dev]",
    `command = ${JSON.stringify(tsx)}`,
    `args = [${[cli, "mcp", "--root", root, "--host", "codex"].map((s) => JSON.stringify(s)).join(", ")}]`,
    "startup_timeout_sec = 30",
    "tool_timeout_sec = 3600",
    "",
  ].join("\n");
  const existing = fs.existsSync(codexCfg) ? fs.readFileSync(codexCfg, "utf8") : "";
  if (!existing.includes("[mcp_servers.prepare-dev]")) {
    fs.mkdirSync(codexDir, { recursive: true });
    fs.appendFileSync(codexCfg, block);
    written.push(".codex/config.toml");
  }
  // skills: link the shared skill into the target repo for Codex (.agents/skills) and Claude Code (.claude/skills)
  const skillSrc = path.join(repo, "skills", "prepare-dev");
  const agentsSkills = path.join(root, ".agents", "skills");
  const skillLink = path.join(agentsSkills, "prepare-dev");
  if (!fs.existsSync(skillLink)) {
    fs.mkdirSync(agentsSkills, { recursive: true });
    fs.symlinkSync(skillSrc, skillLink); // absolute: the tool repo lives at a fixed path, like the MCP command
    written.push(".agents/skills/prepare-dev");
  }
  const claudeSkills = path.join(root, ".claude", "skills");
  if (!fs.existsSync(claudeSkills)) {
    fs.mkdirSync(path.dirname(claudeSkills), { recursive: true });
    fs.symlinkSync(path.join("..", ".agents", "skills"), claudeSkills);
    written.push(".claude/skills");
  }
  print({ root, written, mcp: { claude_code: entry, codex: block.trim() } });
}

async function client(args: Args): Promise<ApiClient> {
  return ApiClient.connect(rootOf(args), { start: args.flags["no-start"] ? false : true });
}

async function cmdStatus(args: Args): Promise<void> {
  const c = await client(args);
  const s = await c.get<Record<string, unknown>>("/api/status");
  if (args.flags.json) return print(s);
  const pending = s.pending as Array<Record<string, unknown>>;
  const lines = [
    `root: ${s.root}`,
    `pending answers: ${pending.length}`,
    ...pending.map((p) => `  ${p.question_id} ${p.answer_id} stale=${p.stale} received=${p.received} choice=${JSON.stringify(p.choice)}`),
    `inflight: ${(s.inflight as unknown[]).length}`,
    `stale decisions: ${(s.stale_decisions as unknown[]).map((d) => (d as { id: string }).id).join(", ") || "none"}`,
    `runs current: ${(s.runs_current as unknown[]).length}, not applicable to current versions: ${(s.runs_not_applicable as unknown[]).length}`,
    ...(s.runs_not_applicable as Array<Record<string, unknown>>).map((r) => `  ${r.run_id} ${r.result} (${(r.mismatches as unknown[]).length} mismatches)`),
    `jobs: ${(s.jobs as unknown[]).length}`,
  ];
  print(lines.join("\n"));
}

async function cmdQ(args: Args): Promise<void> {
  const c = await client(args);
  const [sub, id, aid] = args._.slice(1);
  switch (sub) {
    case "create": {
      const options = list(args.flags.option).map((o) => {
        const [key, rest = ""] = o.split("=", 2);
        const [label, flag] = rest.split(":");
        return { key: key!, label: label || key!, recommended: flag === "recommended" };
      });
      return print(
        await c.post("/api/questions", {
          kind: str(args.flags.kind) ?? "single_choice",
          prompt: str(args.flags.prompt) ?? die("--prompt is required"),
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
    case "ui":
      return print(await c.get(`/api/questions/${id}/ui`));
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
    default:
      die("usage: q create|list|show|ui|answer|wait|pending|ack|apply|close");
  }
}

async function cmdRecord(args: Args): Promise<void> {
  const c = await client(args);
  const [sub, id] = args._.slice(1);
  const fm: Record<string, unknown> = {};
  for (const kv of list(args.flags.fm)) {
    const [k, v] = kv.split("=", 2);
    if (!k) continue;
    fm[k] = v && v.startsWith("[") ? (JSON.parse(v) as unknown) : v;
  }
  switch (sub) {
    case "list":
      return print(await c.get(`/api/records${args.flags.kind ? `?kind=${str(args.flags.kind)}` : ""}`));
    case "get":
      return print(await c.get(`/api/records/${id}`));
    case "create":
      return print(await c.post("/api/records", { kind: str(args.flags.kind) ?? die("--kind"), title: str(args.flags.title) ?? die("--title"), frontmatter: fm, body: readBody(args) }));
    case "put": {
      const cur = await c.get<{ rev: number; sha256: string; body: string }>(`/api/records/${id}`);
      const expectedRev = args.flags["expected-rev"] ? Number(str(args.flags["expected-rev"])) : cur.rev;
      const expectedSha = str(args.flags["expected-sha256"]) ?? cur.sha256;
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
      case "ui":
        return await cmdUi(args);
      case "stop": {
        const info = readDaemonInfo(rootOf(args));
        if (!info) return print("no daemon running");
        process.kill(info.pid, "SIGTERM");
        return print(`stopped pid ${info.pid}`);
      }
      default:
        die("usage: prepare-dev serve|init|mcp|status|pending|q|record|verify|trace|ui|stop [--root dir]");
    }
  } catch (e) {
    if (e instanceof ApiError) die(`${e.code}: ${e.message}${e.details ? ` ${JSON.stringify(e.details)}` : ""}`);
    throw e;
  }
}

void main();
