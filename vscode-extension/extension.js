const path = require("node:path");
const { LanguageClient, TransportKind } = require("vscode-languageclient/node");

let client;

function activate(context) {
  const repoRoot = path.join(__dirname, "..");
  const serverModule = path.join(repoRoot, "dist", "server.mjs");

  const serverOptions = {
    run: {
      command: "node",
      args: [serverModule],
      options: { cwd: repoRoot },
      transport: TransportKind.stdio,
    },
    debug: {
      command: "node",
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
