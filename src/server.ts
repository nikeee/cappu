#!/home/nikeee/.npm-bin/bin/tsx

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
  type Hover,
  type InitializeParams,
  type InitializeResult,
  type Location,
  MarkupKind,
  type TextEdit,
  TextDocuments,
  TextDocumentSyncKind,
  type WorkspaceEdit,
} from "vscode-languageserver/node";
import { TextDocument } from "vscode-languageserver-textdocument";

import { createChecker } from "./checker.ts";
import { type CompletionItem, getCompletions } from "./completions.ts";
import { getDocumentSymbols } from "./documentSymbols.ts";
import { loadJdkStub } from "./jdkStub.ts";
import {
  computeLineStarts,
  getLineAndCharacterOfPosition,
  getPositionOfLineAndCharacter,
} from "./lineMap.ts";
import { getIdentifierAtPosition } from "./nodeAtPosition.ts";
import { createProgram } from "./program.ts";
import { findReferences, getDeclarationNameNode, getSourceFileOfNode } from "./resolver.ts";
import {
  DiagnosticCategory,
  type Diagnostic as JavaDiagnostic,
  type Identifier,
  type Node,
  type SourceFile,
} from "./types.ts";
import { getHoverText } from "./hover.ts";
import { skipTrivia } from "./utilities.ts";
import { loadJavaFiles, uriToPath } from "./workspace.ts";

// Communicate over stdio (the standard transport for editor language clients).
const connection = createConnection(process.stdin, process.stdout);
const documents = new TextDocuments(TextDocument);
const program = createProgram();
loadJdkStub(program);
const checker = createChecker(program);

connection.onInitialize((params: InitializeParams): InitializeResult => {
  // Scan workspace folders for .java files so cross-file resolution works
  // before any file is opened. Open documents later override these.
  const roots =
    params.workspaceFolders?.map(f => f.uri) ?? (params.rootUri ? [params.rootUri] : []);
  for (const root of roots) {
    try {
      for (const { uri, text } of loadJavaFiles(uriToPath(root))) {
        program.addProjectFile(uri, text);
      }
    } catch {
      // non-file root or unreadable directory: ignore
    }
  }
  return {
    capabilities: {
      textDocumentSync: TextDocumentSyncKind.Incremental,
      documentSymbolProvider: true,
      definitionProvider: true,
      referencesProvider: true,
      hoverProvider: true,
      completionProvider: { triggerCharacters: ["."] },
      renameProvider: true,
    },
  };
});

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
  const diagnostics = [
    ...sourceFile.parseDiagnostics,
    ...(sourceFile.bindDiagnostics ?? []),
    ...checker.getSemanticDiagnostics(sourceFile),
  ].map(d => toLspDiagnostic(d, lineStarts));
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

function locationOf(node: Node): Location {
  const file = getSourceFileOfNode(node);
  const lineStarts = computeLineStarts(file.text);
  // node.pos includes leading trivia; advance to the token's real start so the
  // highlighted range covers only the symbol name.
  const start = skipTrivia(file.text, node.pos);
  return {
    uri: file.fileName,
    range: {
      start: getLineAndCharacterOfPosition(lineStarts, start),
      end: getLineAndCharacterOfPosition(lineStarts, node.end),
    },
  };
}

function identifierAt(
  uri: string,
  position: { line: number; character: number },
): Identifier | undefined {
  const sourceFile = program.getSourceFile(uri);
  if (!sourceFile) return undefined;
  const offset = getPositionOfLineAndCharacter(
    computeLineStarts(sourceFile.text),
    position.line,
    position.character,
  );
  return getIdentifierAtPosition(sourceFile, offset) as Identifier | undefined;
}

connection.onReferences((params): Location[] | null => {
  const identifier = identifierAt(params.textDocument.uri, params.position);
  if (!identifier) return null;
  const symbol = checker.resolveName(identifier);
  if (!symbol) return null;
  return findReferences(symbol, program).map(locationOf);
});

connection.onRenameRequest((params): WorkspaceEdit | null => {
  const identifier = identifierAt(params.textDocument.uri, params.position);
  if (!identifier) return null;
  const symbol = checker.resolveName(identifier);
  if (!symbol) return null;
  const changes: Record<string, TextEdit[]> = {};
  for (const node of findReferences(symbol, program)) {
    const location = locationOf(node);
    (changes[location.uri] ??= []).push({ range: location.range, newText: params.newName });
  }
  return Object.keys(changes).length > 0 ? { changes } : null;
});

connection.onDefinition((params): Definition | null => {
  const identifier = identifierAt(params.textDocument.uri, params.position);
  if (!identifier) return null;
  const symbol = checker.resolveName(identifier);
  if (!symbol) return null;
  const nameNode = getDeclarationNameNode(symbol);
  return nameNode ? locationOf(nameNode) : null;
});

connection.onCompletion((params): CompletionItem[] => {
  const sourceFile = program.getSourceFile(params.textDocument.uri);
  if (!sourceFile) return [];
  const offset = getPositionOfLineAndCharacter(
    computeLineStarts(sourceFile.text),
    params.position.line,
    params.position.character,
  );
  return getCompletions(program, checker, sourceFile, offset);
});

connection.onHover((params): Hover | null => {
  const identifier = identifierAt(params.textDocument.uri, params.position);
  if (!identifier) return null;
  const symbol = checker.resolveName(identifier);
  if (!symbol) return null;
  return {
    contents: {
      kind: MarkupKind.Markdown,
      value: "```java\n" + getHoverText(checker, symbol) + "\n```",
    },
  };
});

documents.listen(connection);
connection.listen();
