const path = require("node:path");
const { LanguageClient, TransportKind } = require("vscode-languageclient/node");

let client;

function activate(context) {
  const repoRoot = path.join(__dirname, "..");
  // Run the TypeScript source directly via the repo's tsx: a bundled
  // dist/server.mjs silently goes stale whenever the source changes, which is
  // exactly the failure mode a test client must not have.
  const tsx = path.join(repoRoot, "node_modules", ".bin", "tsx");
  const serverMain = path.join(repoRoot, "src", "services", "serverMain.ts");

  const run = {
    command: tsx,
    args: [serverMain],
    options: { cwd: repoRoot },
    transport: TransportKind.stdio,
  };
  const serverOptions = { run, debug: run };

  const clientOptions = {
    documentSelector: [
      { scheme: "file", language: "java" },
      // cappu.json is synced for the dependency code lenses (VS Code may
      // classify it as json or jsonc depending on settings).
      { scheme: "file", language: "json", pattern: "**/cappu.json" },
      { scheme: "file", language: "jsonc", pattern: "**/cappu.json" },
    ],
  };

  client = new LanguageClient("javalsp", "javalsp", serverOptions, clientOptions);
  client.start();
  context.subscriptions.push(client);
}

function deactivate() {
  return client?.stop();
}

module.exports = { activate, deactivate };
