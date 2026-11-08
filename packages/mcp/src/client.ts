import { spawn } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { z } from "zod";
import { ToolIdentity } from "@prepare-dev/core";
import { canonicalRoot, toolPaths } from "./installation.js";

const DaemonFile = z.object({
  pid: z.number().int().positive(), port: z.number().int().min(1).max(65535),
  url: z.string().url(), root: z.string(), started_at: z.string(),
  tool: ToolIdentity.optional(), shutdown_token: z.string().optional(),
});
export type DaemonInfo = z.infer<typeof DaemonFile>;
const LiveIdentity = z.object({ pid: z.number(), root: z.string(), started_at: z.string(), tool: ToolIdentity.nullable() });

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly details: unknown,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export function readDaemonInfo(root: string): DaemonInfo | null {
  const file = path.join(root, ".prepare-dev", "daemon.json");
  try {
    const info = DaemonFile.parse(JSON.parse(fs.readFileSync(file, "utf8")));
    if (canonicalRoot(info.root) !== canonicalRoot(root) || info.url !== `http://127.0.0.1:${info.port}`) return null;
    return info;
  } catch { return null; }
}

async function probe(info: DaemonInfo): Promise<z.infer<typeof LiveIdentity> | null> {
  try {
    const response = await fetch(`${info.url}/api/identity`, { signal: AbortSignal.timeout(1_500) });
    if (!response.ok) return null;
    const live = LiveIdentity.parse(await response.json());
    return live.pid === info.pid && live.root === info.root && live.started_at === info.started_at ? live : null;
  } catch { return null; }
}

export async function stopDaemon(root: string): Promise<boolean> {
  const info = readDaemonInfo(root);
  if (!info) return false;
  if (!await probe(info) || !info.shutdown_token) throw new Error("cannot verify this daemon; stop it from the version that started it");
  const response = await fetch(`${info.url}/api/shutdown`, { method: "POST", headers: { authorization: `Bearer ${info.shutdown_token}` }, signal: AbortSignal.timeout(5_000) });
  if (!response.ok) throw new Error(`daemon refused to stop: ${await response.text()}`);
  for (let i = 0; i < 50; i++) {
    if (!await probe(info)) return true;
    await sleep(100);
  }
  throw new Error("daemon did not stop in 5 seconds");
}

const sleep = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

/** Connect to the project's daemon, starting it detached if it is not running. */
export async function ensureDaemon(root: string, opts: { start?: boolean; timeoutMs?: number } = {}): Promise<DaemonInfo> {
  root = canonicalRoot(root);
  const paths = toolPaths();
  const check = async (): Promise<DaemonInfo | null> => {
    const info = readDaemonInfo(root);
    if (!info) return null;
    const live = await probe(info);
    if (!live) {
      try { process.kill(info.pid, 0); } catch { return null; }
      throw new Error(`existing daemon cannot be identified; stop it from its original version before using ${paths.identity.root}`);
    }
    if (!live.tool || live.tool.build !== paths.identity.build || live.tool.version !== paths.identity.version || live.tool.root !== paths.identity.root) {
      throw new Error(`daemon belongs to another build or installation; run prepare-dev stop --root ${JSON.stringify(root)}, then retry`);
    }
    return info;
  };
  const existing = await check();
  if (existing) return existing;
  if (opts.start === false) throw new Error(`no daemon running for ${root} (run: prepare-dev serve)`);
  const logDir = path.join(root, ".prepare-dev");
  fs.mkdirSync(logDir, { recursive: true });
  const log = fs.openSync(path.join(logDir, "daemon.log"), "a");
  const child = spawn(process.execPath, [...paths.cliArgs, "serve", "--root", root], {
    cwd: paths.identity.root, detached: true, stdio: ["ignore", log, log],
    env: { ...process.env, PREPARE_DEV_DETACHED: "1" },
  });
  fs.closeSync(log);
  let spawnError: Error | null = null;
  child.once("error", (error) => { spawnError = error; });
  child.unref();
  const deadline = Date.now() + (opts.timeoutMs ?? 15_000);
  while (Date.now() < deadline) {
    if (spawnError) throw spawnError;
    await sleep(100);
    const info = await check();
    if (info) return info;
  }
  throw new Error(`daemon did not start (see ${path.join(logDir, "daemon.log")})`);
}

export class ApiClient {
  constructor(readonly baseUrl: string) {}

  static async connect(root: string, opts?: { start?: boolean }): Promise<ApiClient> {
    const info = await ensureDaemon(root, opts);
    return new ApiClient(info.url);
  }

  async request<T>(method: string, pathname: string, body?: unknown, timeoutMs = 3_600_000): Promise<T> {
    const ac = new AbortController();
    const timer = setTimeout(() => ac.abort(), timeoutMs);
    try {
      const res = await fetch(`${this.baseUrl}${pathname}`, {
        method,
        headers: body !== undefined ? { "content-type": "application/json" } : {},
        body: body !== undefined ? JSON.stringify(body) : undefined,
        signal: ac.signal,
      });
      const text = await res.text();
      const data = text ? (JSON.parse(text) as unknown) : null;
      if (!res.ok) {
        const e = (data ?? {}) as { error?: string; message?: string; details?: unknown };
        throw new ApiError(res.status, e.error ?? "error", e.message ?? res.statusText, e.details);
      }
      return data as T;
    } finally {
      clearTimeout(timer);
    }
  }

  get<T>(pathname: string, timeoutMs?: number): Promise<T> {
    return this.request<T>("GET", pathname, undefined, timeoutMs);
  }

  post<T>(pathname: string, body: unknown): Promise<T> {
    return this.request<T>("POST", pathname, body);
  }

  put<T>(pathname: string, body: unknown): Promise<T> {
    return this.request<T>("PUT", pathname, body);
  }
}
