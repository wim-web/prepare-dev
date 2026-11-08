import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { VerifyJob } from "@prepare-dev/core";
import { vitestPlugin } from "./index.js";

let root: string;
beforeEach(() => { root = fs.mkdtempSync(path.join(os.tmpdir(), "prepare-vitest-inputs-")); });
afterEach(() => { fs.rmSync(root, { recursive: true, force: true }); });

function write(file: string, body: string): void {
  const abs = path.join(root, file);
  fs.mkdirSync(path.dirname(abs), { recursive: true });
  fs.writeFileSync(abs, body);
}

describe("verification inputs", () => {
  it("[V1] includes dependency metadata and transitive imports of extra implementation and config files", async () => {
    write("package.json", '{"name":"fixture","type":"module"}');
    write("pnpm-lock.yaml", "lockfileVersion: '9.0'\n");
    write("pnpm-workspace.yaml", "packages: [packages/*]\n");
    write("packages/lib/package.json", '{"name":"@fixture/lib","main":"./src/index.ts"}');
    write("packages/lib/src/index.ts", "export const lib = 1;");
    write("entry.test.ts", "export const test = 1;");
    write("extra.ts", 'import {\n deep\n} from "./deep.js"; import {\n lib\n} from "@fixture/lib"; export { deep, lib };');
    write("deep.ts", 'export { leaf } from "./leaf.js"; export const deep = 1;');
    write("leaf.ts", "export const leaf = 1;");
    write("vitest.config.ts", 'import {\n config\n} from "./config.js"; export default config;');
    write("config.ts", "export const config = {};");
    const job: VerifyJob = { root, run_id: "RUN-test", plugin: "vitest", claims: [], targets: ["entry.test.ts"], config: { impl_files: ["extra.ts"] } };
    const before = await vitestPlugin.inputs(job);
    const paths = before.files.map((f) => f.path);
    expect(paths).toEqual(expect.arrayContaining(["package.json", "pnpm-lock.yaml", "pnpm-workspace.yaml", "packages/lib/package.json", "packages/lib/src/index.ts", "extra.ts", "deep.ts", "leaf.ts", "vitest.config.ts", "config.ts"]));
    expect(new Set(paths).size).toBe(paths.length);
    for (const file of ["package.json", "pnpm-lock.yaml", "packages/lib/package.json", "leaf.ts", "config.ts"]) {
      const initial = fs.readFileSync(path.join(root, file), "utf8");
      write(file, initial + "\n");
      const after = await vitestPlugin.inputs(job);
      expect(after.files.find((f) => f.path === file)?.sha256, file).not.toBe(before.files.find((f) => f.path === file)?.sha256);
      write(file, initial);
    }
  });

  it("[V1] includes ancestor lockfiles for a nested project", async () => {
    write("package.json", '{"name":"root"}');
    write("yarn.lock", "# lock\n");
    write("apps/site/package.json", '{"name":"site"}');
    write("apps/site/test.ts", "export const value = 1;");
    const inputs = await vitestPlugin.inputs({ root, run_id: "RUN-test", plugin: "vitest", claims: [], targets: ["apps/site/test.ts"], config: { project_dir: "apps/site" } });
    expect(inputs.files.map((f) => f.path)).toEqual(expect.arrayContaining(["package.json", "yarn.lock", "apps/site/package.json"]));
  });
});
