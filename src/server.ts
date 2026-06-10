// Java language server. The LSP protocol/transport is handled by
// vscode-languageserver; everything semantic comes from this project's
// scanner/parser/binder via the Program (which caches parse+bind per document
// version). Currently serves syntax diagnostics and documentSymbol (outline).
//
// Run with: node --run lsp  (the client speaks JSON-RPC over stdio).

import { readFileSync } from "node:fs";

import { TextDocument } from "vscode-languageserver-textdocument";
import {
  type CodeAction,
  type CodeActionParams,
  type CodeLens,
  createConnection,
  type Definition,
  type Diagnostic as LspDiagnostic,
  DiagnosticSeverity,
  DidChangeWatchedFilesNotification,
  type DocumentHighlight,
  DocumentHighlightKind,
  FileChangeType,
  type FoldingRange,
  type DocumentSymbol,
  type Hover,
  ErrorCodes,
  type InitializeParams,
  type InitializeResult,
  type InlayHint,
  InlayHintKind,
  type Location,
  MarkupKind,
  type Range,
  ResponseError,
  type SemanticTokens,
  SemanticTokensBuilder,
  type SignatureHelp,
  type SignatureInformation,
  type SymbolInformation,
  type TextEdit,
  TextDocuments,
  TextDocumentSyncKind,
  type WorkspaceEdit,
} from "vscode-languageserver/node";

import { createChecker } from "./checker.ts";
import { type ArrayType, type ClassType, TypeKind } from "./checkerTypes.ts";
import { getCodeActions } from "./codeActions.ts";
import { getCodeLenses } from "./codeLens.ts";
import { loadConfiguredPaths } from "./compiler.ts";
import { type CompletionItem, getCompletions } from "./completions.ts";
import type { CappuConfig } from "./config.ts";
import { getDocumentSymbols } from "./documentSymbols.ts";
import { enclosingCall, getHoverText } from "./hover.ts";
import { DEFAULT_INLAY_HINTS, getInlayHints, type InlayHintsSettings } from "./inlayHints.ts";
import { loadJdkStub } from "./jdkStub.ts";
import {
  computeLineStarts,
  getLineAndCharacterOfPosition,
  getPositionOfLineAndCharacter,
} from "./lineMap.ts";
import { getIdentifierAtPosition, getNodeAtPosition } from "./nodeAtPosition.ts";
import { forEachChild } from "./parser.ts";
import { createProgram } from "./program.ts";
import { findReferences, getDeclarationNameNode, getSourceFileOfNode } from "./resolver.ts";
import { getSemanticTokens, TOKEN_MODIFIERS, TOKEN_TYPES } from "./semanticTokens.ts";
import { declarationName, findMethodImplementations, getSubtypeIndex } from "./subtypes.ts";
import {
  type CallExpression,
  type AssignmentExpression,
  DiagnosticCategory,
  type Diagnostic as JavaDiagnostic,
  type Identifier,
  type MethodDeclaration,
  type Node,
  type PrefixUnaryExpression,
  type SourceFile,
  type Symbol,
  SymbolFlags,
  SyntaxKind,
} from "./types.ts";
import { isValidIdentifier, skipTrivia } from "./utilities.ts";
import { isSyntheticUri, loadJavaFiles, uriToPath } from "./workspace.ts";

// Communicate over stdio (the standard transport for editor language clients).
const connection = createConnection(process.stdin, process.stdout);
const documents = new TextDocuments(TextDocument);
const program = createProgram();
loadJdkStub(program);
const checker = createChecker(program);

// Inlay-hint configuration: seeded from initializationOptions.inlayHints and
// updatable via workspace/didChangeConfiguration ({ javalsp: { inlayHints } }).
let inlayHintSettings: InlayHintsSettings = { ...DEFAULT_INLAY_HINTS };
function applyInlayHintSettings(raw: unknown): void {
  if (typeof raw !== "object" || raw === null) return;
  const o = raw as { parameterNames?: unknown; varTypes?: unknown };
  if (typeof o.parameterNames === "boolean") inlayHintSettings.parameterNames = o.parameterNames;
  if (typeof o.varTypes === "boolean") inlayHintSettings.varTypes = o.varTypes;
}

connection.onInitialize((params: InitializeParams): InitializeResult => {
  applyInlayHintSettings(
    (params.initializationOptions as { inlayHints?: unknown } | undefined)?.inlayHints,
  );
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
      signatureHelpProvider: { triggerCharacters: ["(", ","] },
      renameProvider: { prepareProvider: true },
      codeActionProvider: true,
      workspaceSymbolProvider: true,
      inlayHintProvider: true,
      documentHighlightProvider: true,
      foldingRangeProvider: true,
      typeDefinitionProvider: true,
      semanticTokensProvider: {
        legend: { tokenTypes: [...TOKEN_TYPES], tokenModifiers: [...TOKEN_MODIFIERS] },
        full: true,
      },
      codeLensProvider: { resolveProvider: false },
      implementationProvider: true,
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

// Watch the workspace for .java files created/changed/deleted outside the
// editor (git operations, codegen): without this the project model scanned at
// initialize goes silently stale. Registered dynamically once the client is
// ready; clients without the capability simply never send the events.
connection.onInitialized(() => {
  void connection.client
    .register(DidChangeWatchedFilesNotification.type, {
      watchers: [{ globPattern: "**/*.java" }],
    })
    .catch(() => {
      // The client does not support dynamic file-watcher registration.
    });
});

connection.onDidChangeWatchedFiles(params => {
  for (const event of params.changes) {
    if (event.type === FileChangeType.Deleted) {
      program.removeProjectFile(event.uri);
      continue;
    }
    try {
      // Created or Changed: (re-)read from disk. An open editor document still
      // wins inside the Program; updating the disk copy keeps didClose honest.
      program.addProjectFile(event.uri, readFileSync(uriToPath(event.uri), "utf8"));
    } catch {
      program.removeProjectFile(event.uri); // unreadable: treat as gone
    }
  }
  // Cross-file resolution may have changed for everything that is open.
  for (const uri of program.getOpenUris()) {
    const sourceFile = program.getSourceFile(uri);
    if (sourceFile) validate(uri, sourceFile);
  }
});

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

function rangeOf(node: Node): Range {
  const file = getSourceFileOfNode(node);
  const lineStarts = computeLineStarts(file.text);
  // node.pos includes leading trivia; advance to the token's real start so the
  // highlighted range covers only the symbol name.
  const start = skipTrivia(file.text, node.pos);
  return {
    start: getLineAndCharacterOfPosition(lineStarts, start),
    end: getLineAndCharacterOfPosition(lineStarts, node.end),
  };
}

function locationOf(node: Node): Location {
  return { uri: getSourceFileOfNode(node).fileName, range: rangeOf(node) };
}

// A symbol declared in the synthetic JDK stub cannot be edited.
function isStubSymbol(symbol: Symbol): boolean {
  const declaration = getDeclarationNameNode(symbol);
  return !!declaration && getSourceFileOfNode(declaration).fileName.startsWith("jdk:///");
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
  return findReferences(symbol, program, checker.resolveName).map(locationOf);
});

// Validate the cursor position before the editor shows its rename box: returns
// the identifier range if it names a renameable (non-JDK) symbol, else null.
connection.onPrepareRename((params): Range | null => {
  const identifier = identifierAt(params.textDocument.uri, params.position);
  if (!identifier) return null;
  const symbol = checker.resolveName(identifier);
  if (!symbol || isStubSymbol(symbol)) return null;
  return rangeOf(identifier);
});

connection.onRenameRequest((params): WorkspaceEdit | null => {
  const identifier = identifierAt(params.textDocument.uri, params.position);
  if (!identifier) return null;
  const symbol = checker.resolveName(identifier);
  if (!symbol) return null;
  if (isStubSymbol(symbol)) {
    throw new ResponseError(
      ErrorCodes.InvalidRequest,
      "Cannot rename a symbol defined by the JDK.",
    );
  }
  if (!isValidIdentifier(params.newName)) {
    throw new ResponseError(
      ErrorCodes.InvalidParams,
      `'${params.newName}' is not a valid Java identifier.`,
    );
  }
  // resolveName also matches member accesses (a.field), so field/method uses
  // across the workspace are renamed, not just lexical occurrences.
  const changes: Record<string, TextEdit[]> = {};
  for (const node of findReferences(symbol, program, checker.resolveName)) {
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

connection.onCodeAction((params: CodeActionParams): CodeAction[] => {
  const sourceFile = program.getSourceFile(params.textDocument.uri);
  if (!sourceFile) return [];
  const lineStarts = computeLineStarts(sourceFile.text);
  const start = getPositionOfLineAndCharacter(
    lineStarts,
    params.range.start.line,
    params.range.start.character,
  );
  const end = getPositionOfLineAndCharacter(
    lineStarts,
    params.range.end.line,
    params.range.end.character,
  );
  return getCodeActions(program, checker, sourceFile, start, end).map(action => ({
    title: action.title,
    kind: action.kind,
    edit: {
      changes: {
        [params.textDocument.uri]: action.changes.map(c => ({
          range: {
            start: getLineAndCharacterOfPosition(lineStarts, c.start),
            end: getLineAndCharacterOfPosition(lineStarts, c.end),
          },
          newText: c.newText,
        })),
      },
    },
  }));
});

// The innermost call whose argument list contains the offset (the cursor sits
// after the callee, between the parentheses), for signature help.
function callAt(
  uri: string,
  position: { line: number; character: number },
): CallExpression | undefined {
  const sourceFile = program.getSourceFile(uri);
  if (!sourceFile) return undefined;
  const offset = getPositionOfLineAndCharacter(
    computeLineStarts(sourceFile.text),
    position.line,
    position.character,
  );
  // The cursor right after `(` in an unclosed call sits at the recovered call's
  // end, which getNodeAtPosition treats as outside; retry one position left,
  // like getIdentifierAtPosition does.
  for (const at of [offset, offset - 1]) {
    let node: Node | undefined = getNodeAtPosition(sourceFile, at);
    for (; node; node = node.parent) {
      if (node.kind !== SyntaxKind.CallExpression) continue;
      const call = node as CallExpression;
      if (offset > call.expression.end && offset <= call.end) return call;
    }
  }
  return undefined;
}

connection.onSignatureHelp((params): SignatureHelp | null => {
  const call = callAt(params.textDocument.uri, params.position);
  if (!call) return null;
  const candidates = checker.resolveCallCandidates(call);
  if (candidates.length === 0) return null;

  const signatures: SignatureInformation[] = candidates.flatMap(decl => {
    const label = checker.signatureOfDeclaration(decl);
    if (!label) return [];
    const doc = checker.getDocumentationOfNode(decl);
    return [
      {
        label,
        parameters: checker.parameterLabelsOf(decl).map(p => ({ label: p })),
        ...(doc ? { documentation: doc } : {}),
      },
    ];
  });
  if (signatures.length === 0) return null;

  const resolved = checker.resolveCall(call);
  const activeSignature = Math.max(
    0,
    candidates.findIndex(d => d === resolved),
  );
  // The argument the cursor is in: count the arguments that end before it.
  const sourceFile = program.getSourceFile(params.textDocument.uri)!;
  const offset = getPositionOfLineAndCharacter(
    computeLineStarts(sourceFile.text),
    params.position.line,
    params.position.character,
  );
  const activeParameter = call.arguments.filter(a => a.end < offset).length;
  return { signatures, activeSignature, activeParameter };
});

// Workspace-wide symbol search (workspace/symbol): every declaration in every
// project file whose name contains the query (case-insensitive). The per-file
// outlines are flattened to SymbolInformation; jdk:/// stub files are skipped
// (the client cannot open them).
connection.onWorkspaceSymbol((params): SymbolInformation[] => {
  const query = params.query.toLowerCase();
  if (!query) return []; // an empty query would dump every declaration
  const results: SymbolInformation[] = [];
  for (const uri of program.getAllUris()) {
    if (isSyntheticUri(uri)) continue;
    const sourceFile = program.getSourceFile(uri);
    if (!sourceFile) continue;
    const lineStarts = computeLineStarts(sourceFile.text);
    const flatten = (symbols: DocumentSymbol[], container?: string): void => {
      for (const s of symbols) {
        if (s.name.toLowerCase().includes(query)) {
          results.push({
            name: s.name,
            kind: s.kind,
            location: { uri, range: s.range },
            ...(container ? { containerName: container } : {}),
          });
        }
        if (s.children) flatten(s.children, s.name);
        if (results.length >= 256) return; // cap a too-broad query
      }
    };
    flatten(getDocumentSymbols(sourceFile, lineStarts));
    if (results.length >= 256) break;
  }
  return results;
});

// In-file occurrences of the symbol under the cursor; an assignment target or
// an in/decrement operand counts as a write.
connection.onDocumentHighlight((params): DocumentHighlight[] | null => {
  const identifier = identifierAt(params.textDocument.uri, params.position);
  if (!identifier) return null;
  const symbol = checker.resolveName(identifier);
  if (!symbol) return null;
  const isWrite = (node: Node): boolean => {
    const parent = node.parent;
    if (!parent) return false;
    if (
      parent.kind === SyntaxKind.AssignmentExpression &&
      (parent as AssignmentExpression).left === node
    ) {
      return true;
    }
    return (
      parent.kind === SyntaxKind.PostfixUnaryExpression ||
      (parent.kind === SyntaxKind.PrefixUnaryExpression &&
        ((parent as PrefixUnaryExpression).operator === SyntaxKind.PlusPlusToken ||
          (parent as PrefixUnaryExpression).operator === SyntaxKind.MinusMinusToken))
    );
  };
  return findReferences(symbol, program, checker.resolveName)
    .filter(node => getSourceFileOfNode(node).fileName === params.textDocument.uri)
    .map(node => ({
      range: rangeOf(node),
      kind: isWrite(node) ? DocumentHighlightKind.Write : DocumentHighlightKind.Read,
    }));
});

// Foldable regions: type/method/constructor/initializer bodies (keeping the
// closing-brace line visible) and the import list.
connection.onFoldingRanges((params): FoldingRange[] | null => {
  const sourceFile = program.getSourceFile(params.textDocument.uri);
  if (!sourceFile) return null;
  const lineStarts = computeLineStarts(sourceFile.text);
  const lineAt = (offset: number): number => getLineAndCharacterOfPosition(lineStarts, offset).line;
  const ranges: FoldingRange[] = [];
  const FOLDABLE = new Set<SyntaxKind>([
    SyntaxKind.ClassDeclaration,
    SyntaxKind.InterfaceDeclaration,
    SyntaxKind.EnumDeclaration,
    SyntaxKind.RecordDeclaration,
    SyntaxKind.AnnotationTypeDeclaration,
    SyntaxKind.MethodDeclaration,
    SyntaxKind.ConstructorDeclaration,
    SyntaxKind.CompactConstructorDeclaration,
    SyntaxKind.InitializerBlock,
  ]);
  const visit = (node: Node): void => {
    if (FOLDABLE.has(node.kind)) {
      const startLine = lineAt(skipTrivia(sourceFile.text, node.pos));
      const endLine = lineAt(node.end) - 1; // keep the closing brace visible
      if (endLine > startLine) ranges.push({ startLine, endLine });
    }
    forEachChild(node, child => {
      visit(child);
      return undefined;
    });
  };
  visit(sourceFile);
  if (sourceFile.imports.length > 1) {
    const first = sourceFile.imports[0]!;
    const last = sourceFile.imports[sourceFile.imports.length - 1]!;
    const startLine = lineAt(skipTrivia(sourceFile.text, first.pos));
    const endLine = lineAt(last.end);
    if (endLine > startLine) ranges.push({ startLine, endLine, kind: "imports" });
  }
  return ranges;
});

// Go to the declaration of the expression's TYPE (the class of a variable,
// not the variable itself).
connection.onTypeDefinition((params): Definition | null => {
  const identifier = identifierAt(params.textDocument.uri, params.position);
  if (!identifier) return null;
  const type = checker.getTypeOfExpression(identifier);
  const classSymbol =
    type.kind === TypeKind.Class
      ? (type as ClassType).symbol
      : type.kind === TypeKind.Array && (type as ArrayType).elementType.kind === TypeKind.Class
        ? ((type as ArrayType).elementType as ClassType).symbol
        : undefined;
  if (!classSymbol) return null;
  const name = getDeclarationNameNode(classSymbol);
  return name ? locationOf(name) : null;
});

// Implementations of the interface/abstract class or method under the cursor:
// transitive subtypes from the subtype index, or the concrete method bodies
// matching an abstract method by name and arity.
connection.onImplementation((params): Definition | null => {
  const identifier = identifierAt(params.textDocument.uri, params.position);
  if (!identifier) return null;
  const symbol = checker.resolveName(identifier);
  if (!symbol) return null;
  if (symbol.flags & (SymbolFlags.Class | SymbolFlags.Interface)) {
    const locations = getSubtypeIndex(program)
      .allSubtypesOf(symbol)
      .map(declarationName)
      .filter((n): n is Identifier => n !== undefined)
      .map(locationOf);
    return locations.length > 0 ? locations : null;
  }
  if (symbol.flags & SymbolFlags.Method) {
    const locations = (symbol.declarations ?? [])
      .filter(d => d.kind === SyntaxKind.MethodDeclaration)
      .flatMap(d => findMethodImplementations(d as MethodDeclaration, program))
      .map(m => locationOf(m.name));
    return locations.length > 0 ? locations : null;
  }
  return null;
});

// A "N references" lens above every type and method declaration. The command is
// the editor.action.showReferences convention (VS Code peeks the locations);
// clients without it still render the count as plain text.
connection.onCodeLens((params): CodeLens[] => {
  const sourceFile = program.getSourceFile(params.textDocument.uri);
  if (!sourceFile) return [];
  return getCodeLenses(program, checker, sourceFile).map(entry => {
    const range = rangeOf(entry.name);
    const n = entry.sites.length;
    const noun = entry.kind === "references" ? "reference" : "implementation";
    return {
      range,
      command: {
        title: `${n} ${noun}${n === 1 ? "" : "s"}`,
        command: "editor.action.showReferences",
        arguments: [params.textDocument.uri, range.start, entry.sites.map(locationOf)],
      },
    };
  });
});

connection.languages.semanticTokens.on((params): SemanticTokens => {
  const sourceFile = program.getSourceFile(params.textDocument.uri);
  if (!sourceFile) return { data: [] };
  const lineStarts = computeLineStarts(sourceFile.text);
  const builder = new SemanticTokensBuilder();
  for (const t of getSemanticTokens(checker, sourceFile)) {
    const { line, character } = getLineAndCharacterOfPosition(lineStarts, t.offset);
    builder.push(line, character, t.length, t.tokenType, t.tokenModifiers);
  }
  return builder.build();
});

connection.onDidChangeConfiguration(params => {
  applyInlayHintSettings(
    (params.settings as { javalsp?: { inlayHints?: unknown } } | undefined)?.javalsp?.inlayHints,
  );
});

connection.languages.inlayHint.on((params): InlayHint[] => {
  const sourceFile = program.getSourceFile(params.textDocument.uri);
  if (!sourceFile) return [];
  const lineStarts = computeLineStarts(sourceFile.text);
  const start = getPositionOfLineAndCharacter(
    lineStarts,
    params.range.start.line,
    params.range.start.character,
  );
  const end = getPositionOfLineAndCharacter(
    lineStarts,
    params.range.end.line,
    params.range.end.character,
  );
  return getInlayHints(checker, sourceFile, start, end, inlayHintSettings).map(h => ({
    position: getLineAndCharacterOfPosition(lineStarts, h.offset),
    label: h.label,
    kind: h.kind === "parameter" ? InlayHintKind.Parameter : InlayHintKind.Type,
    // `count: <arg>` reads with a gap after the name; `x: String` sticks to x.
    ...(h.kind === "parameter" ? { paddingRight: true } : {}),
  }));
});

connection.onHover((params): Hover | null => {
  const identifier = identifierAt(params.textDocument.uri, params.position);
  if (!identifier) return null;
  const symbol = checker.resolveName(identifier);
  if (!symbol) return null;

  // getHoverText renders the instantiated overload for a call use; the Javadoc
  // still comes from the specific resolved overload's declaration.
  const text = getHoverText(checker, symbol, identifier);
  const call = enclosingCall(identifier);
  const overload = call ? checker.resolveCall(call) : undefined;
  const doc = overload
    ? checker.getDocumentationOfNode(overload)
    : checker.getDocumentation(symbol);

  let value = "```java\n" + text + "\n```";
  if (doc) value += "\n\n" + doc;
  return { contents: { kind: MarkupKind.Markdown, value } };
});

/**
 * Begin serving: attach the document manager and start reading JSON-RPC from
 * stdin. The handlers above are registered at module load, but nothing is read
 * until this is called, so importing this module has no observable effect.
 *
 * A cappu.config.json contributes the classpath/source paths (types resolve
 * but are not workspace files) and the lspOptions base settings; client
 * initializationOptions and didChangeConfiguration still override the latter.
 */
export function startServer(config?: CappuConfig): void {
  if (config) {
    applyInlayHintSettings(config.lspOptions.inlayHints);
    loadConfiguredPaths(program, config);
  }
  documents.listen(connection);
  connection.listen();
}
