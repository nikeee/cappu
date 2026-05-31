const path = require("node:path");
const { LanguageClient, TransportKind } = require("vscode-languageclient/node");

let client;

function activate(context) {
  // Repo root is one level up from this extension folder.
  const repoRoot = path.join(__dirname, "..");
  const tsx = path.join(repoRoot, "node_modules", ".bin", "tsx");
  const serverModule = path.join(repoRoot, "src", "server.ts");

  // Run the LSP server via tsx, speaking JSON-RPC over stdio.
  const serverOptions = {
    run: {
      command: tsx,
      args: [serverModule],
      options: { cwd: repoRoot },
      transport: TransportKind.stdio,
    },
    debug: {
      command: tsx,
      args: [serverModule],
      options: { cwd: repoRoot },
      transport: TransportKind.stdio,
    },
  };

  const clientOptions = {
    documentSelector: [{ scheme: "file", language: "java" }],
  };

  client = new LanguageClient("javalsp", "javalsp", serverOptions, clientOptions);
  client.start();
  context.subscriptions.push(client);
}

function deactivate() {
  return client?.stop();
}

module.exports = { activate, deactivate };
