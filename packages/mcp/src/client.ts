import { spawn } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export interface DaemonInfo {
  pid: number;
  port: number;
  url: string;
  root: string;
  started_at: string;
}

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

/** Repository root of prepare-dev itself (where the CLI lives), resolved from this file. */
export function toolRepoRoot(): string {
  return path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..", "..");
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

const sleep = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

/** Connect to the project's daemon, starting it detached if it is not running. */
export async function ensureDaemon(root: string, opts: { start?: boolean; timeoutMs?: number } = {}): Promise<DaemonInfo> {
  const existing = readDaemonInfo(root);
  if (existing) return existing;
  if (opts.start === false) throw new Error(`no daemon running for ${root} (run: prepare-dev serve)`);
  const repo = toolRepoRoot();
  const tsx = path.join(repo, "node_modules", ".bin", "tsx");
  const cli = path.join(repo, "packages", "cli", "src", "index.ts");
  const logDir = path.join(root, ".prepare-dev");
  fs.mkdirSync(logDir, { recursive: true });
  const log = fs.openSync(path.join(logDir, "daemon.log"), "a");
  const child = spawn(tsx, [cli, "serve", "--root", root], {
    cwd: repo,
    detached: true,
    stdio: ["ignore", log, log],
    env: { ...process.env, PREPARE_DEV_DETACHED: "1" },
  });
  child.unref();
  const deadline = Date.now() + (opts.timeoutMs ?? 15_000);
  while (Date.now() < deadline) {
    await sleep(150);
    const info = readDaemonInfo(root);
    if (info) return info;
  }
  throw new Error(`daemon did not start within ${opts.timeoutMs ?? 15_000}ms (see ${path.join(logDir, "daemon.log")})`);
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
