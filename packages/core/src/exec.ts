import { spawn } from "node:child_process";
import os from "node:os";
import path from "node:path";

export interface ExecResult {
  code: number | null;
  signal: NodeJS.Signals | null;
  stdout: string;
  stderr: string;
  timedOut: boolean;
}

export interface ExecOptions {
  cwd?: string;
  env?: Record<string, string>;
  signal?: AbortSignal;
  timeoutMs?: number;
  onLine?: (stream: "stdout" | "stderr", line: string) => void;
}

/** PATH with the usual user tool locations appended (elan, cargo, local bin). */
export function toolPath(extra: string[] = []): string {
  const home = os.homedir();
  const parts = [process.env.PATH ?? "", path.join(home, ".elan", "bin"), path.join(home, ".cargo", "bin"), path.join(home, ".local", "bin"), ...extra];
  return parts.filter(Boolean).join(path.delimiter);
}

export function exec(cmd: string, args: string[], opts: ExecOptions = {}): Promise<ExecResult> {
  return new Promise((resolve, reject) => {
    const child = spawn(cmd, args, {
      cwd: opts.cwd,
      env: { ...process.env, PATH: toolPath(), ...(opts.env ?? {}) },
      stdio: ["ignore", "pipe", "pipe"],
    });
    let stdout = "";
    let stderr = "";
    let timedOut = false;
    const feed = (stream: "stdout" | "stderr") => {
      let buf = "";
      return (chunk: Buffer) => {
        const text = chunk.toString("utf8");
        if (stream === "stdout") stdout += text;
        else stderr += text;
        if (!opts.onLine) return;
        buf += text;
        let i: number;
        while ((i = buf.indexOf("\n")) >= 0) {
          opts.onLine(stream, buf.slice(0, i));
          buf = buf.slice(i + 1);
        }
      };
    };
    child.stdout.on("data", feed("stdout"));
    child.stderr.on("data", feed("stderr"));
    const timer = opts.timeoutMs
      ? setTimeout(() => {
          timedOut = true;
          child.kill("SIGKILL");
        }, opts.timeoutMs)
      : null;
    const onAbort = (): void => {
      child.kill("SIGTERM");
    };
    opts.signal?.addEventListener("abort", onAbort, { once: true });
    child.on("error", (e) => {
      if (timer) clearTimeout(timer);
      opts.signal?.removeEventListener("abort", onAbort);
      reject(e);
    });
    child.on("close", (code, signal) => {
      if (timer) clearTimeout(timer);
      opts.signal?.removeEventListener("abort", onAbort);
      resolve({ code, signal, stdout, stderr, timedOut });
    });
  });
}
