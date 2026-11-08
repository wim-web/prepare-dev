import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";
import { viteSingleFile } from "vite-plugin-singlefile";

// Dev server only: forward API + SSE calls to a running daemon (`prepare-dev serve`).
const daemon = process.env.PREPARE_DEV_URL ?? "http://127.0.0.1:7777";

export default defineConfig({
  plugins: [react(), viteSingleFile()],
  build: { outDir: "dist", emptyOutDir: true },
  server: {
    proxy: {
      "/api": { target: daemon, changeOrigin: true },
      "/events": { target: daemon, changeOrigin: true },
    },
  },
});
