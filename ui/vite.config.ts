import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin, type ProxyOptions } from "vite";
import { viteSingleFile } from "vite-plugin-singlefile";
import { createHash } from "node:crypto";
import fs from "node:fs";
import path from "node:path";

// Dev server only: forward API + SSE calls to a running daemon (`prepare-dev serve`).
const daemon = process.env.PREPARE_DEV_URL ?? "http://127.0.0.1:7777";
const proxy: ProxyOptions = {
  target: daemon, changeOrigin: true,
  configure(server) {
    server.on("proxyReq", (outgoing, incoming) => {
      if (incoming.headers.origin === `http://${incoming.headers.host}`) outgoing.setHeader("origin", new URL(daemon).origin);
    });
  },
};

const sha256 = (data: string | Buffer) => createHash("sha256").update(data).digest("hex");

// The files the build is made from, as paths relative to ui/: the five top-level files
// below and everything under src/, except tests (they do not change the build) and
// hidden files (.DS_Store). internal/uibuild computes the same digest in Go; a new build
// input outside src/ must be added to both lists.
function sourceFiles(dir: string): string[] {
  const files = ["index.html", "package.json", "pnpm-lock.yaml", "tsconfig.json", "vite.config.ts"].filter((f) => fs.existsSync(path.join(dir, f)));
  const walk = (rel: string) => {
    for (const e of fs.readdirSync(path.join(dir, rel), { withFileTypes: true })) {
      const r = `${rel}/${e.name}`;
      if (e.name.startsWith(".")) continue;
      if (e.isDirectory()) walk(r);
      else if (e.isFile() && !/\.test\.[cm]?[jt]sx?$/.test(e.name)) files.push(r);
    }
  };
  walk("src");
  return files.sort();
}

export function sourceDigest(dir: string): string {
  return sha256(sourceFiles(dir).map((f) => `${f}\n${sha256(fs.readFileSync(path.join(dir, f)))}\n`).join(""));
}

// Writes build/THIRD_PARTY_NOTICES.txt (the licenses of the npm packages bundled into
// index.html) and build/build.json: the digest of the sources of this build and the
// sha256 of each output file, which internal/uibuild checks.
function distributionFiles(): Plugin {
  return {
    name: "prepare-dev-distribution-files",
    writeBundle(options) {
      const out = options.dir ?? path.resolve("build");
      const packages = new Map<string, { dir: string; meta: { name: string; version: string; license?: unknown } }>();
      for (const id of this.getModuleIds()) {
        const file = path.resolve(id.split("?")[0]!.replace(/^\0/, ""));
        if (!file.includes(`${path.sep}node_modules${path.sep}`)) continue;
        for (let dir = path.dirname(file); dir !== path.dirname(dir); dir = path.dirname(dir)) {
          const manifest = path.join(dir, "package.json");
          if (!fs.existsSync(manifest)) continue;
          const meta = JSON.parse(fs.readFileSync(manifest, "utf8"));
          if (meta.name && meta.version) {
            packages.set(`${meta.name}@${meta.version}`, { dir, meta });
            break;
          }
        }
      }
      const notices = [...packages].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)).map(([name, { dir, meta }]) => {
        const licenses = fs.readdirSync(dir).filter((f) => /^(licen[cs]e|copying|notice)([.\-_]|$)/i.test(f)).sort();
        return `${name}\nDeclared license: ${JSON.stringify(meta.license ?? "see package license")}\n${licenses.map((f) => fs.readFileSync(path.join(dir, f), "utf8")).join("\n")}`;
      });
      fs.writeFileSync(path.join(out, "THIRD_PARTY_NOTICES.txt"), notices.join("\n\n--------------------\n\n") + "\n");
      const files: Record<string, string> = {};
      for (const f of ["THIRD_PARTY_NOTICES.txt", "index.html"]) files[f] = sha256(fs.readFileSync(path.join(out, f)));
      fs.writeFileSync(path.join(out, "build.json"), JSON.stringify({ source: sourceDigest(path.resolve(".")), files }, null, 2) + "\n");
    },
  };
}

export default defineConfig({
  plugins: [react(), viteSingleFile(), distributionFiles()],
  build: { outDir: "build", emptyOutDir: true },
  server: {
    proxy: {
      "/api": proxy,
      "/events": proxy,
    },
  },
});
