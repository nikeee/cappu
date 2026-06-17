// Java language server. The LSP protocol/transport is handled by
// vscode-languageserver; everything semantic comes from this project's
// scanner/parser/binder via the Program (which caches parse+bind per document
// version). Serves diagnostics plus the navigation/editing features listed in
// the initialize capabilities (hover, completion, references, code lenses,
// inlay hints, semantic tokens, ...).
//
// Run with: node --run lsp  (the client speaks JSON-RPC over stdio).

import { readFileSync } from "node:fs";

import { TextDocument } from "vscode-languageserver-textdocument";
import {
  type CodeAction,
  DiagnosticTag,
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

import { createChecker } from "../compiler/checker.ts";
import { type ArrayType, type ClassType, TypeKind } from "../compiler/checkerTypes.ts";
import { getCodeActions } from "./codeActions.ts";
import { getCodeLenses } from "./codeLens.ts";
import { dependencyLenses } from "./dependencyLens.ts";
import { loadConfiguredPaths, missingConfiguredPaths } from "../compiler/compiler.ts";
import { type CompletionItem, getCompletions } from "./completions.ts";
import { type CappuConfig, DEFAULT_CONFIG_NAME, DEFAULT_PACKAGE_SOURCES } from "../config.ts";
import { latestVersion, MavenRepositorySource, type PackageSource } from "../packages/index.ts";
import { getDocumentSymbols } from "./documentSymbols.ts";
import { enclosingCall, getHoverText } from "./hover.ts";
import { DEFAULT_INLAY_HINTS, getInlayHints, type InlayHintsSettings } from "./inlayHints.ts";
import { loadJdkStub } from "../compiler/jdkStub.ts";
import {
  type Character,
  computeLineStarts,
  getLineAndCharacterOfPosition,
  getPositionOfLineAndCharacter,
  type Line,
} from "../compiler/lineMap.ts";
import { getIdentifierAtPosition, getNodeAtPosition } from "./nodeAtPosition.ts";
import { forEachChild } from "../compiler/parser.ts";
import { createProgram } from "../compiler/program.ts";
import {
  findReferences,
  getDeclarationNameNode,
  getSourceFileOfNode,
} from "../compiler/resolver.ts";
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
} from "../compiler/types.ts";
import { isValidIdentifier, skipTrivia } from "../compiler/utilities.ts";
import { type Uri, isSyntheticUri, loadJavaFiles, uriToPath } from "../workspace.ts";

/** The stream pair the server speaks JSON-RPC over (default: stdio). */
export interface Transport {
  reader: NodeJS.ReadableStream;
  writer: NodeJS.WritableStream;
}

/**
 * Create the JSON-RPC connection over `transport`, register every provider
 * and start listening. All server state lives in this closure; nothing
 * happens at module load (the CLI passes an accepted TCP socket for --port).
 */
export function startServer(
  config?: CappuConfig,
  transport: Transport = { reader: process.stdin, writer: process.stdout },
): void {
  const connection = createConnection(transport.reader, transport.writer);
  // The LSP protocol types carry DocumentUri as a plain string; brand it once
  // at the boundary.
  const asUri = (uri: string): Uri => uri as Uri;

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
        for (const { uri, text } of loadJavaFiles(uriToPath(asUri(root)))) {
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
      // editors fade out ranges tagged Unnecessary (unused imports)
      ...(d.code === 1305 ? { tags: [DiagnosticTag.Unnecessary] } : {}),
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
        program.removeProjectFile(asUri(event.uri));
        continue;
      }
      try {
        // Created or Changed: (re-)read from disk. An open editor document still
        // wins inside the Program; updating the disk copy keeps didClose honest.
        program.addProjectFile(asUri(event.uri), readFileSync(uriToPath(asUri(event.uri)), "utf8"));
      } catch {
        program.removeProjectFile(asUri(event.uri)); // unreadable: treat as gone
      }
    }
    // Cross-file resolution may have changed for everything that is open.
    for (const uri of program.getOpenUris()) {
      const sourceFile = program.getSourceFile(uri);
      if (sourceFile) validate(uri, sourceFile);
    }
  });

  // The client also syncs cappu.json (for the dependency code lenses); only
  // .java documents belong to the Java program model.
  const isJavaUri = (uri: string): boolean => uri.endsWith(".java");

  // TextDocuments fires onDidChangeContent on both open and change.
  documents.onDidChangeContent(change => {
    if (!isJavaUri(change.document.uri)) return;
    const { version } = change.document;
    const uri = asUri(change.document.uri);
    program.setOpenDocument(uri, change.document.getText(), version);
    const sourceFile = program.getSourceFile(uri);
    if (sourceFile) validate(uri, sourceFile);
  });

  documents.onDidClose(event => {
    if (!isJavaUri(event.document.uri)) return;
    program.closeDocument(asUri(event.document.uri));
    connection.sendDiagnostics({ uri: event.document.uri, diagnostics: [] });
  });

  connection.onDocumentSymbol((params): DocumentSymbol[] => {
    const sourceFile = program.getSourceFile(asUri(params.textDocument.uri));
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

  // The (sourceFile, offset) prologue every position-based handler starts with.
  function sourceAndOffset(
    uri: Uri,
    position: { line: number; character: number },
  ): { sourceFile: SourceFile; offset: number } | undefined {
    const sourceFile = program.getSourceFile(uri);
    if (!sourceFile) return undefined;
    const offset = getPositionOfLineAndCharacter(
      computeLineStarts(sourceFile.text),
      position.line as Line,
      position.character as Character,
    );
    return { sourceFile, offset };
  }

  function identifierAt(
    uri: Uri,
    position: { line: number; character: number },
  ): Identifier | undefined {
    const at = sourceAndOffset(uri, position);
    return at && (getIdentifierAtPosition(at.sourceFile, at.offset) as Identifier | undefined);
  }

  // The declaration of an expression's (inferred) type: its class, or an
  // array's element class. Powers go-to-type-definition and clicking `var`.
  function typeDefinitionOf(expr: Identifier): Definition | null {
    const type = checker.getTypeOfExpression(expr);
    const classSymbol =
      type.kind === TypeKind.Class
        ? (type as ClassType).symbol
        : type.kind === TypeKind.Array && (type as ArrayType).elementType.kind === TypeKind.Class
          ? ((type as ArrayType).elementType as ClassType).symbol
          : undefined;
    if (!classSymbol) return null;
    const name = getDeclarationNameNode(classSymbol);
    return name ? locationOf(name) : null;
  }

  // The variable name a `var` type node infers its type from: a single-declarator
  // local-variable statement, or a parameter (for-each / lambda) binding.
  function varInferredFrom(varType: Node): Identifier | undefined {
    const parent = varType.parent as
      | (Node & { name?: Identifier; declarators?: { name: Identifier }[] })
      | undefined;
    return parent?.name ?? parent?.declarators?.[0]?.name;
  }

  connection.onReferences((params): Location[] | null => {
    const identifier = identifierAt(asUri(params.textDocument.uri), params.position);
    if (!identifier) return null;
    const symbol = checker.resolveName(identifier);
    if (!symbol) return null;
    return findReferences(symbol, program, checker.resolveName).map(locationOf);
  });

  // Validate the cursor position before the editor shows its rename box: returns
  // the identifier range if it names a renameable (non-JDK) symbol, else null.
  connection.onPrepareRename((params): Range | null => {
    const identifier = identifierAt(asUri(params.textDocument.uri), params.position);
    if (!identifier) return null;
    const symbol = checker.resolveName(identifier);
    if (!symbol || isStubSymbol(symbol)) return null;
    return rangeOf(identifier);
  });

  connection.onRenameRequest((params): WorkspaceEdit | null => {
    const identifier = identifierAt(asUri(params.textDocument.uri), params.position);
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
    const at = sourceAndOffset(asUri(params.textDocument.uri), params.position);
    if (!at) return null;
    const identifier = getIdentifierAtPosition(at.sourceFile, at.offset) as Identifier | undefined;
    if (identifier) {
      const symbol = checker.resolveName(identifier);
      if (!symbol) return null;
      const nameNode = getDeclarationNameNode(symbol);
      return nameNode ? locationOf(nameNode) : null;
    }
    // On the `var` keyword: navigate to the inferred type's declaration.
    const node = getNodeAtPosition(at.sourceFile, at.offset);
    if (node?.kind === SyntaxKind.VarType) {
      const nameId = varInferredFrom(node);
      return nameId ? typeDefinitionOf(nameId) : null;
    }
    return null;
  });

  connection.onCompletion((params): CompletionItem[] => {
    const at = sourceAndOffset(asUri(params.textDocument.uri), params.position);
    return at ? getCompletions(program, checker, at.sourceFile, at.offset) : [];
  });

  connection.onCodeAction((params: CodeActionParams): CodeAction[] => {
    const sourceFile = program.getSourceFile(asUri(params.textDocument.uri));
    if (!sourceFile) return [];
    const lineStarts = computeLineStarts(sourceFile.text);
    const start = getPositionOfLineAndCharacter(
      lineStarts,
      params.range.start.line as Line,
      params.range.start.character as Character,
    );
    const end = getPositionOfLineAndCharacter(
      lineStarts,
      params.range.end.line as Line,
      params.range.end.character as Character,
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
    uri: Uri,
    position: { line: number; character: number },
  ): CallExpression | undefined {
    const at = sourceAndOffset(uri, position);
    if (!at) return undefined;
    const { sourceFile, offset } = at;
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
    const call = callAt(asUri(params.textDocument.uri), params.position);
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
    const { offset } = sourceAndOffset(asUri(params.textDocument.uri), params.position)!;
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
    const identifier = identifierAt(asUri(params.textDocument.uri), params.position);
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
    const sourceFile = program.getSourceFile(asUri(params.textDocument.uri));
    if (!sourceFile) return null;
    const lineStarts = computeLineStarts(sourceFile.text);
    const lineAt = (offset: number): number =>
      getLineAndCharacterOfPosition(lineStarts, offset).line;
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
      const last = sourceFile.imports.at(-1)!;
      const startLine = lineAt(skipTrivia(sourceFile.text, first.pos));
      const endLine = lineAt(last.end);
      if (endLine > startLine) ranges.push({ startLine, endLine, kind: "imports" });
    }
    return ranges;
  });

  // Go to the declaration of the expression's TYPE (the class of a variable,
  // not the variable itself).
  connection.onTypeDefinition((params): Definition | null => {
    const identifier = identifierAt(asUri(params.textDocument.uri), params.position);
    return identifier ? typeDefinitionOf(identifier) : null;
  });

  // Implementations of the interface/abstract class or method under the cursor:
  // transitive subtypes from the subtype index, or the concrete method bodies
  // matching an abstract method by name and arity.
  connection.onImplementation((params): Definition | null => {
    const identifier = identifierAt(asUri(params.textDocument.uri), params.position);
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

  // A "N references" lens above every type and method declaration. The command
  // is cappu.showReferences, which the vscode extension implements by converting
  // the LSP-shaped arguments (string uri, {line, character} positions) into
  // vscode.Uri/Position/Location and delegating to editor.action.showReferences
  // - handing that builtin raw JSON arguments fails silently (nikeee/cappu#10).
  // Clients without the command still render the count as plain text.
  // Package repositories for the cappu.json dependency lenses (startServer
  // swaps in the configured list). The newest-version lookups go to the
  // network, so results are cached briefly per group:artifact.
  let packageSourceUrls: readonly string[] = DEFAULT_PACKAGE_SOURCES;
  let packageSources: PackageSource[] | undefined;
  const latestCache = new Map<string, { value: string | undefined; at: number }>();
  const LATEST_TTL_MS = 5 * 60_000;
  async function cachedLatestVersion(
    groupId: string,
    artifactId: string,
  ): Promise<string | undefined> {
    const key = `${groupId}:${artifactId}`;
    const cached = latestCache.get(key);
    if (cached && Date.now() - cached.at < LATEST_TTL_MS) return cached.value;
    packageSources ??= packageSourceUrls.map(url => new MavenRepositorySource(url));
    let value: string | undefined;
    try {
      value = await latestVersion(groupId, artifactId, packageSources);
    } catch {
      value = undefined; // offline: no lenses rather than an error
    }
    latestCache.set(key, { value, at: Date.now() });
    return value;
  }

  // `cappu.json` documents get dependency lenses instead of Java ones.
  async function dependencyCodeLenses(uri: string): Promise<CodeLens[]> {
    const document = documents.get(uri);
    if (!document) return [];
    const lenses = await dependencyLenses(document.getText(), cachedLatestVersion);
    return lenses.map(({ entry, title }) => ({
      range: {
        start: { line: entry.line, character: entry.startCharacter },
        end: { line: entry.line, character: entry.endCharacter },
      },
      command: { title, command: "" },
    }));
  }

  connection.onCodeLens(async (params): Promise<CodeLens[]> => {
    if (params.textDocument.uri.endsWith(`/${DEFAULT_CONFIG_NAME}`)) {
      return dependencyCodeLenses(params.textDocument.uri);
    }
    const sourceFile = program.getSourceFile(asUri(params.textDocument.uri));
    if (!sourceFile) return [];
    return getCodeLenses(program, checker, sourceFile).map(entry => {
      const range = rangeOf(entry.name);
      const n = entry.sites.length;
      const noun = entry.kind === "references" ? "reference" : "implementation";
      return {
        range,
        command: {
          title: `${n} ${noun}${n === 1 ? "" : "s"}`,
          command: "cappu.showReferences",
          arguments: [params.textDocument.uri, range.start, entry.sites.map(locationOf)],
        },
      };
    });
  });

  connection.languages.semanticTokens.on((params): SemanticTokens => {
    const sourceFile = program.getSourceFile(asUri(params.textDocument.uri));
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
    const sourceFile = program.getSourceFile(asUri(params.textDocument.uri));
    if (!sourceFile) return [];
    const lineStarts = computeLineStarts(sourceFile.text);
    const start = getPositionOfLineAndCharacter(
      lineStarts,
      params.range.start.line as Line,
      params.range.start.character as Character,
    );
    const end = getPositionOfLineAndCharacter(
      lineStarts,
      params.range.end.line as Line,
      params.range.end.character as Character,
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
    const identifier = identifierAt(asUri(params.textDocument.uri), params.position);
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
   * A cappu.json contributes the classpath/source paths (types resolve
   * but are not workspace files) and the lspOptions base settings; client
   * initializationOptions and didChangeConfiguration still override the latter.
   */
  if (config) {
    packageSourceUrls = config.packageSources;
    applyInlayHintSettings(config.lspOptions.inlayHints);
    // A missing configured directory is treated as empty; only worth a warning
    // when an actual cappu.json configured it.
    for (const path of missingConfiguredPaths(config)) {
      connection.console.warn(`configured path not found (treated as empty): ${path}`);
    }
    loadConfiguredPaths(program, config);
  }
  documents.listen(connection);
  connection.listen();
}
