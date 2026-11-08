import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { afterEach, beforeEach, expect, it } from "vitest";
import { z } from "zod";

const exec = promisify(execFile);
const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
const artifact = z.object({ tarball: z.string() }).parse(JSON.parse(fs.readFileSync(path.join(repo, "dist/artifact.json"), "utf8")));
let base: string;
let root: string;
let installed: string;
const cli = () => path.join(installed, "bin/prepare-dev.mjs");
const run = async (...args: string[]) => (await exec(process.execPath, [cli(), ...args, "--root", root], { cwd: base, timeout: 90_000, maxBuffer: 5_000_000 })).stdout.trim();
const data = async (...args: string[]): Promise<unknown> => JSON.parse(await run(...args));
const write = (name: string, value: string) => { const file = path.join(root, name); fs.mkdirSync(path.dirname(file), { recursive: true }); fs.writeFileSync(file, value); return file; };

beforeEach(async () => {
  base = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "prepare-distribution-")));
  root = path.join(base, "target project");
  installed = path.join(base, "package");
  fs.mkdirSync(root);
  await exec("tar", ["-xzf", artifact.tarball, "-C", base]);
});
afterEach(async () => {
  try { await run("stop"); } finally { fs.rmSync(base, { recursive: true, force: true }); }
});

it("[P1] runs the relocated distribution through decisions, a real Lean verification and trace", async () => {
  expect(await data("version", "--json")).toMatchObject({ version: "0.1.0", root: installed, mode: "package" });
  const bundled = fs.readFileSync(cli(), "utf8");
  expect(bundled.includes(repo)).toBe(false);
  expect(fs.existsSync(path.join(installed, "node_modules"))).toBe(false);
  expect(await run("help")).toContain("--prompt-file");
  const init = z.object({ ui: z.string() }).parse(await data("init"));
  const html = await (await fetch(init.ui)).text();
  expect(html).toContain("<html");
  expect(html).not.toContain("@json-render");
  const requirement = z.object({ id: z.string(), rev: z.number(), sha256: z.string() }).parse(await data("record", "create", "--kind", "requirement", "--title", "予約人数の上限", "--body", "受付人数は定員以下"));
  expect(requirement.id).toBe("REQ-0001");
  await expect(run("record", "put", requirement.id, "--body", "変更")).rejects.toThrow(/expected-rev/);
  await run("record", "put", requirement.id, "--expected-rev", String(requirement.rev), "--expected-sha256", requirement.sha256, "--body", "受付人数は定員以下。自然数を扱う。");
  await expect(run("record", "put", requirement.id, "--expected-rev", String(requirement.rev), "--expected-sha256", requirement.sha256, "--body", "古い編集")).rejects.toThrow(/changed|conflict/);
  const prompt = write("question.md", "# 受付の上限\n\n定員を4人にする案です。混雑と人数のどちらを優先しますか？\n\n| 案 | 影響 |\n| - | - |\n| 4人 | 待ち時間を短くする |\n");
  const options = write("options.json", JSON.stringify([{ key: "four", label: "4人まで", summary: "混雑を抑える", recommended: true, reason: "待ち時間を優先" }]));
  expect(await data("q", "create", "--subject", "REQ-0001", "--prompt-file", prompt, "--options-file", options)).toMatchObject({ question: { id: "Q-0001" } });
  expect((await fetch(`${init.ui}/api/questions/Q-0001/ui`)).status).toBe(404);
  expect(await data("q", "answer", "Q-0001", "--choice", "four")).toMatchObject({ answer: { answer_id: "ANS-0001-1" } });
  expect(await data("q", "wait", "Q-0001", "--timeout", "100")).toMatchObject({ status: "answered" });
  await run("q", "ack", "Q-0001", "ANS-0001-1");
  expect(await data("q", "apply", "Q-0001", "ANS-0001-1", "--title", "4人にする", "--body", "混雑を抑える")).toMatchObject({ status: "applied", decision_id: "DEC-0001" });
  expect(await data("q", "apply", "Q-0001", "ANS-0001-1", "--title", "4人にする")).toMatchObject({ status: "already_applied" });
  const fm = write("spec.json", JSON.stringify({ status: "accepted", links: { specifies: ["REQ-0001"] }, claims: [{ id: "C1", statement: "受付人数は定員以下", requirement: "REQ-0001", native: [{ plugin: "lean4", ref: "Reservation.bounded" }] }] }));
  await run("record", "create", "--kind", "spec", "--title", "受付人数", "--fm-file", fm, "--body", "入力は自然数、受付人数はmin(requested,capacity)");
  write("model/lean-toolchain", fs.readFileSync(path.join(repo, "model/lean-toolchain"), "utf8"));
  write("model/lakefile.toml", 'name = "reservation_model"\ndefaultTargets = ["Reservation"]\n[[lean_lib]]\nname = "Reservation"\n');
  write("model/Reservation.lean", "namespace Reservation\ndef accepted (requested capacity : Nat) := min requested capacity\ntheorem bounded (requested capacity : Nat) : accepted requested capacity ≤ capacity := Nat.min_le_right _ _\nend Reservation\n");
  expect(await run("guidance", "lean4")).toContain("native");
  const verified = await data("verify", "run", "--plugin", "lean4", "--claim", "SPEC-0001/C1", "--target", "model/Reservation.lean", "--config-json", '{"project_dir":"model"}', "--wait");
  expect(verified).toMatchObject({ status: "done", record: { result: "verified", method: "proof" }, applicability: { applicable: true } });
  expect(await data("trace", "REQ-0001")).toMatchObject({ decisions: [expect.objectContaining({ committed: true })], specs: [expect.objectContaining({ id: "SPEC-0001" })] });
}, 120_000);

it("[P2] copies skills without MCP setup and preserves user edits on repeated init", async () => {
  await run("init");
  const skill = path.join(root, ".agents/skills/prepare-dev/SKILL.md");
  expect(fs.statSync(skill).isFile()).toBe(true);
  expect(fs.lstatSync(path.dirname(skill)).isSymbolicLink()).toBe(false);
  expect(fs.existsSync(path.join(root, ".mcp.json"))).toBe(false);
  expect(fs.existsSync(path.join(root, ".codex/config.toml"))).toBe(false);
  expect(fs.readFileSync(skill, "utf8")).not.toContain(repo);
  expect(await data("init")).toMatchObject({ skills: { copied: [], conflicts: [] } });
  fs.appendFileSync(skill, "\nuser edit\n");
  write(".mcp.json", '{"keep":true}');
  write(".codex/config.toml", "# user configuration\n");
  expect(await data("init")).toMatchObject({ skills: { conflicts: expect.arrayContaining([path.dirname(skill)]) } });
  expect(fs.readFileSync(skill, "utf8")).toContain("user edit");
  expect(fs.readFileSync(path.join(root, ".mcp.json"), "utf8")).toBe('{"keep":true}');
  expect(fs.readFileSync(path.join(root, ".codex/config.toml"), "utf8")).toBe("# user configuration\n");
  fs.rmSync(path.join(root, ".claude"), { recursive: true });
  const outside = path.join(base, "outside"); fs.mkdirSync(outside);
  fs.symlinkSync(outside, path.join(root, ".claude"));
  expect(await data("init")).toMatchObject({ skills: { conflicts: expect.arrayContaining([path.join(root, ".claude/skills")]) } });
  expect(fs.readdirSync(outside)).toEqual([]);
});

it("[P3] shares one daemon under concurrent startup and rejects another installation until stopped", async () => {
  const statuses = await Promise.all(Array.from({ length: 4 }, () => data("status", "--json")));
  for (const s of statuses) expect(s).toMatchObject({ root, counts: { records: 0 } });
  const info = z.object({ url: z.string(), pid: z.number() }).parse(JSON.parse(fs.readFileSync(path.join(root, ".prepare-dev/daemon.json"), "utf8")));
  expect((await fetch(`${info.url}/api/status`, { headers: { origin: "https://example.invalid" } })).status).toBe(403);
  expect((await fetch(`${info.url}/api/shutdown`, { method: "POST" })).status).toBe(403);
  const moved = path.join(base, "moved package");
  fs.renameSync(installed, moved); installed = moved;
  await expect(run("status", "--json")).rejects.toThrow(/another build or installation/);
  await run("stop");
  expect(await data("status", "--json")).toMatchObject({ root });
  const current = z.object({ pid: z.number() }).parse(JSON.parse(fs.readFileSync(path.join(root, ".prepare-dev/daemon.json"), "utf8")));
  expect(current.pid).not.toBe(info.pid);
});
