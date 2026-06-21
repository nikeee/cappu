// Start a Debug Adapter Protocol server: one DAP connection bound to one debug
// session, over stdio by default or any reader/writer pair (TCP from the CLI).
// Mirrors src/services/server.ts (the LSP entry).
//
// Port reference for togo/internal/dapserver/server.go.

import type { Readable, Writable } from "node:stream";

import type { CappuConfig } from "../../config.ts";
import { DebugSession } from "./debugSession.ts";
import { DapConnection } from "./transport.ts";

export function startDapServer(
  config: CappuConfig,
  transport?: { reader: Readable; writer: Writable },
): Promise<void> {
  const reader = transport?.reader ?? process.stdin;
  const writer = transport?.writer ?? process.stdout;
  const conn = new DapConnection(reader, writer);
  new DebugSession(conn, config);
  return conn.run();
}
