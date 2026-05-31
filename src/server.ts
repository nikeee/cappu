// Minimal Java language server: parses + binds on every change and publishes
// syntax diagnostics, and answers documentSymbol (outline) requests. The LSP
// protocol/transport is handled by vscode-languageserver; everything semantic
// comes from this project's scanner/parser/binder.
//
// Run with: node --import tsx src/server.ts  (the client speaks JSON-RPC over
// stdio). See package.json "lsp" script.

import {
  createConnection,
  type Diagnostic as LspDiagnostic,
  DiagnosticSeverity,
  type DocumentSymbol,
  type InitializeResult,
  TextDocuments,
  TextDocumentSyncKind,
} from "vscode-languageserver/node";
import { TextDocument } from "vscode-languageserver-textdocument";

import { bindSourceFile } from "./binder.ts";
import { getDocumentSymbols } from "./documentSymbols.ts";
import { computeLineStarts, getLineAndCharacterOfPosition } from "./lineMap.ts";
import { parseSourceFile } from "./parser.ts";
import { DiagnosticCategory, type Diagnostic as JavaDiagnostic } from "./types.ts";

// Communicate over stdio (the standard transport for editor language clients).
const connection = createConnection(process.stdin, process.stdout);
const documents = new TextDocuments(TextDocument);

connection.onInitialize(
  (): InitializeResult => ({
    capabilities: {
      textDocumentSync: TextDocumentSyncKind.Incremental,
      documentSymbolProvider: true,
    },
  }),
);

function toSeverity(category: DiagnosticCategory): DiagnosticSeverity {
  switch (category) {
    case DiagnosticCategory.Error:
      return DiagnosticSeverity.Error;
    case DiagnosticCategory.Warning:
      return DiagnosticSeverity.Warning;
    case DiagnosticCategory.Suggestion:
      return DiagnosticSeverity.Hint;
    default:
      return DiagnosticSeverity.Information;
  }
}

function toLspDiagnostic(d: JavaDiagnostic, lineStarts: readonly number[]): LspDiagnostic {
  return {
    severity: toSeverity(d.category),
    range: {
      start: getLineAndCharacterOfPosition(lineStarts, d.pos),
      end: getLineAndCharacterOfPosition(lineStarts, d.end),
    },
    message: d.messageText,
    source: "javalsp",
    code: d.code,
  };
}

function validate(document: TextDocument): void {
  const text = document.getText();
  const lineStarts = computeLineStarts(text);
  const sourceFile = parseSourceFile(document.uri, text);
  bindSourceFile(sourceFile);
  const diagnostics = [...sourceFile.parseDiagnostics, ...(sourceFile.bindDiagnostics ?? [])].map(
    d => toLspDiagnostic(d, lineStarts),
  );
  connection.sendDiagnostics({ uri: document.uri, diagnostics });
}

documents.onDidChangeContent(change => validate(change.document));

connection.onDocumentSymbol((params): DocumentSymbol[] => {
  const document = documents.get(params.textDocument.uri);
  if (!document) return [];
  const text = document.getText();
  const lineStarts = computeLineStarts(text);
  const sourceFile = parseSourceFile(document.uri, text);
  return getDocumentSymbols(sourceFile, lineStarts);
});

documents.listen(connection);
connection.listen();
