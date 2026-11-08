import net from "node:net";
import { createHash } from "node:crypto";

/** An OS-owned lease acquired before opening the project's persistent store. */
export async function acquireLease(root: string): Promise<() => Promise<void>> {
  const port = 20_000 + createHash("sha256").update(root).digest().readUInt32BE(0) % 20_000;
  const lease = net.createServer((socket) => socket.end());
  await new Promise<void>((resolve, reject) => {
    lease.once("error", (error) => reject(new Error(`runtime lease port ${port} is occupied for ${root}; another daemon is starting/running, or a local service conflicts`, { cause: error })));
    lease.listen({ host: "127.0.0.1", port, exclusive: true }, resolve);
  });
  return () => new Promise<void>((resolve, reject) => lease.close((error) => error ? reject(error) : resolve()));
}
