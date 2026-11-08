import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import http from "node:http";
import { expect, it } from "vitest";
import { Runtime } from "./runtime.js";
import { Server } from "./server.js";

it("[P3] refuses new work and an already-open request body after accepting shutdown", async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "prepare-shutdown-"));
  const rt = Runtime.open(root);
  const server = new Server(rt, { onShutdown: async () => {} });
  const info = await server.listen();
  try {
    const req = http.request(`${info.url}/api/runs`, { method: "POST", headers: { "content-type": "application/json" } });
    const response = new Promise<number | undefined>((resolve, reject) => {
      req.on("response", (res) => { res.resume(); res.on("end", () => resolve(res.statusCode)); });
      req.on("error", reject);
    });
    await new Promise<void>((resolve) => req.write('{"plugin":', () => resolve()));
    await fetch(`${info.url}/api/identity`);
    const stopped = await fetch(`${info.url}/api/shutdown`, { method: "POST", headers: { authorization: `Bearer ${info.shutdown_token}` } });
    expect(stopped.status).toBe(200);
    req.end('"vitest","claims":[]}');
    expect(await response).toBe(409);
    const fresh = await fetch(`${info.url}/api/runs`, { method: "POST", headers: { "content-type": "application/json" }, body: '{"plugin":"vitest","claims":[]}' });
    expect(fresh.status).toBe(409);
    expect(rt.runs.jobsList()).toEqual([]);
  } finally { await server.close(); fs.rmSync(root, { recursive: true, force: true }); }
});
