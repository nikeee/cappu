import path from "node:path";

import * as vscode from "vscode";
import { LanguageClient, TransportKind } from "vscode-languageclient/node";

let client;

export function activate(context) {
  const repoRoot = path.join(import.meta.dirname, "..");
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

  // The server's code lenses carry LSP-shaped arguments (string uri,
  // {line, character} positions). editor.action.showReferences silently does
  // nothing with those - it wants vscode.Uri/Position/Location - so the lens
  // command converts here, on the client.
  context.subscriptions.push(
    vscode.commands.registerCommand("cappu.showReferences", (uri, position, locations) => {
      const toRange = r =>
        new vscode.Range(r.start.line, r.start.character, r.end.line, r.end.character);
      return vscode.commands.executeCommand(
        "editor.action.showReferences",
        vscode.Uri.parse(uri),
        new vscode.Position(position.line, position.character),
        locations.map(l => new vscode.Location(vscode.Uri.parse(l.uri), toRange(l.range))),
      );
    }),
  );
}

export function deactivate() {
  return client?.stop();
}
