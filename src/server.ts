// Java language server. The LSP protocol/transport is handled by
// vscode-languageserver; everything semantic comes from this project's
// scanner/parser/binder via the Program (which caches parse+bind per document
// version). Currently serves syntax diagnostics and documentSymbol (outline).
//
// Run with: node --run lsp  (the client speaks JSON-RPC over stdio).

import {
  createConnection,
  type Definition,
  type Diagnostic as LspDiagnostic,
  DiagnosticSeverity,
  type DocumentSymbol,
  type InitializeResult,
  TextDocuments,
  TextDocumentSyncKind,
} from "vscode-languageserver/node";
import { TextDocument } from "vscode-languageserver-textdocument";

import { getDocumentSymbols } from "./documentSymbols.ts";
import {
  computeLineStarts,
  getLineAndCharacterOfPosition,
  getPositionOfLineAndCharacter,
} from "./lineMap.ts";
import { getIdentifierAtPosition } from "./nodeAtPosition.ts";
import { createProgram } from "./program.ts";
import { getDeclarationNameNode, getSourceFileOfNode, resolveIdentifier } from "./resolver.ts";
import {
  DiagnosticCategory,
  type Diagnostic as JavaDiagnostic,
  type Identifier,
  type SourceFile,
} from "./types.ts";

// Communicate over stdio (the standard transport for editor language clients).
const connection = createConnection(process.stdin, process.stdout);
const documents = new TextDocuments(TextDocument);
const program = createProgram();

connection.onInitialize(
  (): InitializeResult => ({
    capabilities: {
      textDocumentSync: TextDocumentSyncKind.Incremental,
      documentSymbolProvider: true,
      definitionProvider: true,
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

function validate(uri: string, sourceFile: SourceFile): void {
  const lineStarts = computeLineStarts(sourceFile.text);
  const diagnostics = [...sourceFile.parseDiagnostics, ...(sourceFile.bindDiagnostics ?? [])].map(
    d => toLspDiagnostic(d, lineStarts),
  );
  connection.sendDiagnostics({ uri, diagnostics });
}

// TextDocuments fires onDidChangeContent on both open and change.
documents.onDidChangeContent(change => {
  const { uri, version } = change.document;
  program.setOpenDocument(uri, change.document.getText(), version);
  const sourceFile = program.getSourceFile(uri);
  if (sourceFile) validate(uri, sourceFile);
});

documents.onDidClose(event => {
  program.closeDocument(event.document.uri);
  connection.sendDiagnostics({ uri: event.document.uri, diagnostics: [] });
});

connection.onDocumentSymbol((params): DocumentSymbol[] => {
  const sourceFile = program.getSourceFile(params.textDocument.uri);
  if (!sourceFile) return [];
  return getDocumentSymbols(sourceFile, computeLineStarts(sourceFile.text));
});

connection.onDefinition((params): Definition | null => {
  const sourceFile = program.getSourceFile(params.textDocument.uri);
  if (!sourceFile) return null;
  const offset = getPositionOfLineAndCharacter(
    computeLineStarts(sourceFile.text),
    params.position.line,
    params.position.character,
  );
  const identifier = getIdentifierAtPosition(sourceFile, offset);
  if (!identifier) return null;
  const symbol = resolveIdentifier(identifier as Identifier, program);
  if (!symbol) return null;
  const nameNode = getDeclarationNameNode(symbol);
  if (!nameNode) return null;
  const targetFile = getSourceFileOfNode(nameNode);
  const targetLineStarts = computeLineStarts(targetFile.text);
  return {
    uri: targetFile.fileName,
    range: {
      start: getLineAndCharacterOfPosition(targetLineStarts, nameNode.pos),
      end: getLineAndCharacterOfPosition(targetLineStarts, nameNode.end),
    },
  };
});

documents.listen(connection);
connection.listen();
