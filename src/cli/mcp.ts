// `cappu mcp`: start the MCP server for agents over stdio. The semantic engine
// is loaded from the project config (classPath + sourcePaths), mirroring
// `cappu lsp`; the transport keeps the process alive.

import type { CappuConfig } from "../config.ts";
import { startMcpServer } from "../services/mcpServer.ts";

export async function runMcp(config: CappuConfig): Promise<void> {
  await startMcpServer(config);
}
