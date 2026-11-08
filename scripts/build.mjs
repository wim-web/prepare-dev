import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { createHash } from "node:crypto";
import { build } from "esbuild";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const out = path.join(root, "dist/package");
const pkg = JSON.parse(await fs.readFile(path.join(root, "package.json"), "utf8"));
await fs.rm(out, { recursive: true, force: true });
await fs.mkdir(path.join(out, "bin"), { recursive: true });
const result = await build({
  absWorkingDir: root, entryPoints: ["packages/cli/src/index.ts"], outfile: path.join(out, "bin/prepare-dev.mjs"),
  bundle: true, platform: "node", format: "esm", target: "node26", metafile: true, legalComments: "inline",
  banner: { js: 'import {createRequire as __createRequire} from "node:module"; import {fileURLToPath as __fileURLToPath} from "node:url"; import {dirname as __pathDirname} from "node:path"; const require=__createRequire(import.meta.url); const __filename=__fileURLToPath(import.meta.url); const __dirname=__pathDirname(__filename);' },
});
await fs.chmod(path.join(out, "bin/prepare-dev.mjs"), 0o755);
await fs.mkdir(path.join(out, "ui"));
await fs.copyFile(path.join(root, "packages/ui/dist/index.html"), path.join(out, "ui/index.html"));
await fs.cp(path.join(root, "skills"), path.join(out, "skills"), { recursive: true });
await fs.mkdir(path.join(out, "guidance"));
for (const name of ["lean4", "vitest"]) await fs.copyFile(path.join(root, `plugins/${name}/guidance/${name}.md`), path.join(out, `guidance/${name}.md`));

const uiModules = JSON.parse(await fs.readFile(path.join(root, "packages/ui/dist/modules.json"), "utf8"));
const packages = new Map();
for (const input of [...Object.keys(result.metafile.inputs), ...uiModules]) {
  const file = path.resolve(root, input.split("?")[0]);
  if (!file.includes(`${path.sep}node_modules${path.sep}`)) continue;
  let dir = path.dirname(file);
  while (dir !== path.dirname(dir)) {
    try {
      const meta = JSON.parse(await fs.readFile(path.join(dir, "package.json"), "utf8"));
      if (meta.name && meta.version) packages.set(`${meta.name}@${meta.version}`, { dir, meta });
      break;
    } catch { dir = path.dirname(dir); }
  }
}
const notices = [];
for (const [name, { dir, meta }] of [...packages].sort(([a], [b]) => a.localeCompare(b))) {
  const licenses = (await fs.readdir(dir)).filter((name) => /^(licen[cs]e|copying|notice)([.\-_]|$)/i.test(name));
  notices.push(`${name}\nDeclared license: ${JSON.stringify(meta.license ?? "see package license")}\n${(await Promise.all(licenses.map((file) => fs.readFile(path.join(dir, file), "utf8")))).join("\n")}`);
}
await fs.writeFile(path.join(out, "THIRD_PARTY_NOTICES.txt"), notices.join("\n\n--------------------\n\n"));
await fs.copyFile(path.join(root, "docs/distribution-readme.md"), path.join(out, "README.md"));
await fs.writeFile(path.join(out, "package.json"), JSON.stringify({
  name: "prepare-dev", version: pkg.version, private: true, type: "module", description: "Local requirements, decisions and verification for coding agents",
  engines: { node: ">=26" }, bin: { "prepare-dev": "bin/prepare-dev.mjs" },
  files: ["bin", "ui", "skills", "guidance", "prepare-dev-distribution.json", "THIRD_PARTY_NOTICES.txt", "README.md"],
}, null, 2) + "\n");
const hash = createHash("sha256");
async function hashTree(dir) {
  for (const entry of (await fs.readdir(dir, { withFileTypes: true })).sort((a, b) => a.name.localeCompare(b.name))) {
    const file = path.join(dir, entry.name);
    hash.update(path.relative(out, file));
    if (entry.isDirectory()) await hashTree(file); else hash.update(await fs.readFile(file));
  }
}
await hashTree(out);
const metadata = { version: pkg.version, build: hash.digest("hex") };
await fs.writeFile(path.join(out, "prepare-dev-distribution.json"), JSON.stringify(metadata, null, 2) + "\n");
console.log(JSON.stringify({ directory: out, ...metadata }));
