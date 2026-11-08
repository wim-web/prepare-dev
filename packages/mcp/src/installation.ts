import fs from "node:fs";
import path from "node:path";
import { createHash } from "node:crypto";
import { createRequire } from "node:module";
import { fileURLToPath, pathToFileURL } from "node:url";
import { z } from "zod";
import type { ToolIdentity } from "@prepare-dev/core";
const Manifest = z.object({ version: z.string(), build: z.string() });

export function canonicalRoot(root: string): string {
  return fs.realpathSync(path.resolve(root));
}

function sourceDigest(root: string): string {
  const hash = createHash("sha256");
  const walk = (dir: string): void => {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
      if (["node_modules", "dist"].includes(entry.name)) continue;
      const file = path.join(dir, entry.name);
      if (entry.isDirectory()) walk(file);
      else if (/\.(ts|tsx|json|css)$/.test(entry.name) && !entry.name.includes(".test.")) hash.update(path.relative(root, file)).update(fs.readFileSync(file));
    }
  };
  walk(path.join(root, "packages"));
  walk(path.join(root, "plugins"));
  return hash.digest("hex");
}

export function toolPaths(): { identity: ToolIdentity; cliArgs: string[]; ui: string; skills: string; guidance: string } {
  const dir = path.dirname(fileURLToPath(import.meta.url));
  const packageRoot = path.resolve(dir, "..");
  const marker = path.join(packageRoot, "prepare-dev-distribution.json");
  if (fs.existsSync(marker)) {
    const manifest = Manifest.parse(JSON.parse(fs.readFileSync(marker, "utf8")));
    return {
      identity: { ...manifest, root: canonicalRoot(packageRoot), mode: "package" },
      cliArgs: [path.join(packageRoot, "bin", "prepare-dev.mjs")],
      ui: path.join(packageRoot, "ui", "index.html"), skills: path.join(packageRoot, "skills"), guidance: path.join(packageRoot, "guidance"),
    };
  }
  const root = canonicalRoot(path.resolve(dir, "../../.."));
  const version = z.object({ version: z.string() }).parse(JSON.parse(fs.readFileSync(path.join(root, "package.json"), "utf8"))).version;
  return {
    identity: { version, build: sourceDigest(root), root, mode: "source" },
    cliArgs: ["--import", pathToFileURL(createRequire(import.meta.url).resolve("tsx")).href, path.join(root, "packages/cli/src/index.ts")],
    ui: path.join(root, "packages/ui/dist/index.html"), skills: path.join(root, "skills"), guidance: path.join(root, "plugins"),
  };
}
