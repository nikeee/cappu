// Standalone stdio entry point for the bundled language server: this is what
// dist/server.mjs (run by the vscode test extension) executes. The cli
// (`cappu lsp`) stays the configurable entry; here the config is picked up
// from the working directory, and a broken cappu.json must not prevent the
// server from starting (the LSP session is more useful than the error).

import { loadConfig } from "./config.ts";
import { startServer } from "./server.ts";

let config;
try {
  config = loadConfig(undefined);
} catch (e) {
  process.stderr.write(`cappu: ignoring config: ${(e as Error).message}\n`);
}
startServer(config);
