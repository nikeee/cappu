// `cappu dap`: start the debug adapter over stdio, or - with --port - over the
// first accepted TCP connection (one session per process). Mirrors src/cli/lsp.ts.

import { once } from "node:events";
import type { Socket } from "node:net";

import type { CappuConfig } from "../config.ts";
import { startDapServer } from "../services/dap/dapServer.ts";
import { listenDisposable } from "./disposableServer.ts";

export async function runDap(config: CappuConfig, portArg: string | undefined): Promise<void> {
  if (portArg !== undefined) {
    const port = Number(portArg);
    if (!Number.isInteger(port) || port < 0 || port > 65535) {
      process.stderr.write(`cappu: invalid port '${portArg}'\n`);
      process.exit(2);
    }
    let socket: Socket;
    {
      using tcp = listenDisposable(port);
      await once(tcp, "listening");
      const address = tcp.address();
      const bound = typeof address === "object" && address ? address.port : port;
      process.stderr.write(`cappu dap listening on port ${bound}\n`);
      [socket] = (await once(tcp, "connection")) as [Socket];
    }
    socket.once("close", () => process.exit(0));
    await startDapServer(config, { reader: socket, writer: socket });
    return;
  }
  await startDapServer(config);
}
