import react from "@vitejs/plugin-react";
import { defineConfig, type ProxyOptions } from "vite";
import { viteSingleFile } from "vite-plugin-singlefile";
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

export default defineConfig({
  plugins: [react(), viteSingleFile(), {
    name: "distribution-licenses",
    writeBundle() {
      fs.writeFileSync(path.resolve("dist/modules.json"), JSON.stringify([...this.getModuleIds()]));
    },
  }],
  build: { outDir: "dist", emptyOutDir: true },
  server: {
    proxy: {
      "/api": proxy,
      "/events": proxy,
    },
  },
});
