import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const output = execFileSync("npm", ["pack", "--ignore-scripts", "--json", "--pack-destination", path.join(root, "dist")], {
  cwd: path.join(root, "dist/package"), encoding: "utf8", env: { ...process.env, npm_config_cache: path.join(root, ".prepare-dev/tmp/npm-cache") },
});
const [packed] = JSON.parse(output);
const tarball = path.join(root, "dist", packed.filename);
const sha256 = createHash("sha256").update(await fs.readFile(tarball)).digest("hex");
await fs.writeFile(path.join(root, "dist/artifact.json"), JSON.stringify({ tarball, sha256 }, null, 2) + "\n");
console.log(JSON.stringify({ tarball, sha256, size: packed.size }));
