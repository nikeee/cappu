// `cappu lsp`: start the language server over stdio, or - with --port - over
// the first accepted TCP connection. The server stack is imported lazily so
// the other commands never load the LSP transport.

import { once } from "node:events";
import type { Socket } from "node:net";

import type { CappuConfig } from "../config.ts";

export async function runLsp(config: CappuConfig, portArg: string | undefined): Promise<void> {
  if (portArg !== undefined) {
    const port = Number(portArg);
    if (!Number.isInteger(port) || port < 0 || port > 65535) {
      process.stderr.write(`cappu: invalid port '${portArg}'\n`);
      process.exit(2);
    }
    // Socket mode: listen, hand the first accepted connection to the server
    // (configured before the lazy import, since server.ts creates its
    // JSON-RPC connection at module load), exit when it disconnects.
    const { createServer } = await import("node:net");
    const tcp = createServer().listen(port);
    await once(tcp, "listening");
    const address = tcp.address();
    const bound = typeof address === "object" && address ? address.port : port;
    process.stderr.write(`cappu lsp listening on port ${bound}\n`);

    const [socket] = (await once(tcp, "connection")) as [Socket];
    tcp.close(); // one session per process, like other socket-mode servers
    socket.once("close", () => process.exit(0));
    const { setTransport } = await import("../services/serverTransport.ts");
    setTransport(socket, socket);
  }
  // startServer() begins reading the transport and keeps the process alive.
  const { startServer } = await import("../services/server.ts");
  startServer(config);
}
