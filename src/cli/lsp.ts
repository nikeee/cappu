// `cappu lsp`: start the language server over stdio, or - with --port - over
// the first accepted TCP connection (the transport is passed into
// startServer; nothing speaks JSON-RPC at module load).

import { once } from "node:events";
import type { Socket } from "node:net";

import type { CappuConfig } from "../config.ts";
import { startServer } from "../services/server.ts";
import { listenDisposable } from "./disposableServer.ts";

export async function runLsp(config: CappuConfig, portArg: string | undefined): Promise<void> {
  if (portArg !== undefined) {
    const port = Number(portArg);
    if (!Number.isInteger(port) || port < 0 || port > 65535) {
      process.stderr.write(`cappu: invalid port '${portArg}'\n`);
      process.exit(2);
    }
    // Socket mode: listen, hand the first accepted connection to the server,
    // exit when it disconnects (one session per process).
    let socket: Socket;
    {
      using tcp = listenDisposable(port);
      await once(tcp, "listening");
      const address = tcp.address();
      const bound = typeof address === "object" && address ? address.port : port;
      process.stderr.write(`cappu lsp listening on port ${bound}\n`);
      [socket] = (await once(tcp, "connection")) as [Socket];
    }
    socket.once("close", () => process.exit(0));
    startServer(config, { reader: socket, writer: socket });
    return;
  }
  // stdio: startServer's default transport; it keeps the process alive.
  startServer(config);
}
