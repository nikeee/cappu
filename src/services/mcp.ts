// MCP tool handlers: a thin, name-addressed, JSON-returning layer over the
// engine (Program + Checker). Handlers are pure over the current Program state;
// disk freshness and transport live in mcpServer.ts. Locations are 1-based and
// use filesystem paths so agents can act on them directly.

import type { DocumentSymbol } from "vscode-languageserver-types";

import type { Checker } from "../compiler/checker.ts";
import { computeLineStarts, getLineAndCharacterOfPosition } from "../compiler/lineMap.ts";
import type { Program } from "../compiler/program.ts";
import {
  findReferences,
  getDeclarationNameNode,
  getDirectSuperTypeSymbols,
  getSourceFileOfNode,
} from "../compiler/resolver.ts";
import {
  DiagnosticCategory,
  type Diagnostic,
  type Identifier,
  type MethodDeclaration,
  type Node,
  type Symbol,
  SymbolFlags,
} from "../compiler/types.ts";
import { isValidIdentifier, skipTrivia } from "../compiler/utilities.ts";
import { isSyntheticUri, pathToUri, type Uri, uriToPath } from "../workspace.ts";
import { getDocumentSymbols } from "./documentSymbols.ts";
import { enclosingCall, getHoverText, symbolKindWord } from "./hover.ts";
import { resolveSymbolRef } from "./mcpResolve.ts";
import { findMethodImplementations, getSubtypeIndex } from "./subtypes.ts";

export interface McpLocation {
  file: string;
  line: number;
  column: number;
  endLine: number;
  endColumn: number;
}

export interface McpDiagnostic {
  file: string;
  severity: "error" | "warning" | "hint" | "info";
  code: number;
  message: string;
  line: number;
  column: number;
  endLine: number;
  endColumn: number;
}

export interface McpDeprecatedUse {
  file: string;
  line: number;
  column: number;
  endLine: number;
  endColumn: number;
  /** The referenced name. */
  name: string;
  /** What was used: a deprecated method, type or field. */
  kind: "method" | "type" | "field";
  /** @Deprecated(since=...), when given. */
  since?: string;
  /** @Deprecated(forRemoval=true). */
  forRemoval: boolean;
  /** A human-readable summary. */
  message: string;
}

function severityOf(category: DiagnosticCategory): McpDiagnostic["severity"] {
  switch (category) {
    case DiagnosticCategory.Error:
      return "error";
    case DiagnosticCategory.Warning:
      return "warning";
    case DiagnosticCategory.Suggestion:
      return "hint";
    default:
      return "info";
  }
}

// Synthetic stub uris (jdk:///, classpath:///) are not real files; keep them
// verbatim. Everything else is a file:// uri we surface as a plain path.
function displayFile(uri: string): string {
  return isSyntheticUri(uri) ? uri : uriToPath(uri as Uri);
}

// node.pos includes leading trivia; advance to the token's real start so the
// reported location points at the name, mirroring server.ts:rangeOf.
export function nodeLocation(node: Node): McpLocation {
  const file = getSourceFileOfNode(node);
  const lineStarts = computeLineStarts(file.text);
  const start = getLineAndCharacterOfPosition(lineStarts, skipTrivia(file.text, node.pos));
  const end = getLineAndCharacterOfPosition(lineStarts, node.end);
  return {
    file: displayFile(file.fileName),
    line: start.line + 1,
    column: start.character + 1,
    endLine: end.line + 1,
    endColumn: end.character + 1,
  };
}

function formatDiagnostic(uri: Uri, d: Diagnostic, lineStarts: readonly number[]): McpDiagnostic {
  const start = getLineAndCharacterOfPosition(lineStarts, d.pos);
  const end = getLineAndCharacterOfPosition(lineStarts, d.end);
  return {
    file: displayFile(uri),
    severity: severityOf(d.category),
    code: d.code,
    message: d.messageText,
    line: start.line + 1,
    column: start.character + 1,
    endLine: end.line + 1,
    endColumn: end.character + 1,
  };
}

export interface McpMatch {
  kind: string;
  label: string;
  signature?: string;
  documentation?: string;
  definition?: McpLocation;
}

export interface McpMember extends McpMatch {
  /** True when the member comes from a supertype, not the queried type itself. */
  inherited: boolean;
}

export interface McpEdit extends McpLocation {
  newText: string;
}

export interface McpTools {
  diagnostics(args: { files?: string[] }): { diagnostics: McpDiagnostic[] };
  deprecatedUses(args: { files?: string[] }): { deprecatedUses: McpDeprecatedUse[] };
  outline(args: { file: string }): { symbols: DocumentSymbol[] };
  searchSymbols(args: { query: string }): { matches: string[] };
  describeSymbol(args: { ref: string }): { matches: McpMatch[] };
  findDefinition(args: { ref: string }): { definitions: McpLocation[] };
  findReferences(args: { ref: string }): {
    references: McpLocation[];
    ambiguous?: boolean;
    candidates?: number;
  };
  findImplementations(args: { ref: string }): {
    implementations: McpMatch[];
    ambiguous?: boolean;
    candidates?: number;
  };
  listMembers(args: { ref: string }): {
    members: McpMember[];
    ambiguous?: boolean;
    candidates?: number;
  };
  findCallers(args: { ref: string }): {
    callers: McpLocation[];
    ambiguous?: boolean;
    candidates?: number;
  };
  typeHierarchy(args: { ref: string }): {
    supertypes: McpMatch[];
    subtypes: McpMatch[];
    ambiguous?: boolean;
    candidates?: number;
  };
  resolveImport(args: { name: string }): { imports: string[] };
  renameSymbol(args: { ref: string; newName: string }): {
    edits: McpEdit[];
    error?: string;
    ambiguous?: boolean;
    candidates?: number;
  };
}

export function createMcpTools(program: Program, checker: Checker): McpTools {
  function diagnostics(args: { files?: string[] }): { diagnostics: McpDiagnostic[] } {
    const uris = args.files?.length ? args.files.map(pathToUri) : program.getAllUris();
    const out: McpDiagnostic[] = [];
    for (const uri of uris) {
      const sourceFile = program.getSourceFile(uri);
      if (!sourceFile) continue;
      const lineStarts = computeLineStarts(sourceFile.text);
      const all = [
        ...sourceFile.parseDiagnostics,
        ...(sourceFile.bindDiagnostics ?? []),
        ...checker.getSemanticDiagnostics(sourceFile),
      ];
      for (const d of all) out.push(formatDiagnostic(uri, d, lineStarts));
    }
    return { diagnostics: out };
  }

  // Every use of a @Deprecated method or type across the given files (or the
  // whole project), with the declaration's since/forRemoval for triage.
  function deprecatedUses(args: { files?: string[] }): { deprecatedUses: McpDeprecatedUse[] } {
    const uris = args.files?.length ? args.files.map(pathToUri) : program.getAllUris();
    const out: McpDeprecatedUse[] = [];
    for (const uri of uris) {
      const sourceFile = program.getSourceFile(uri);
      if (!sourceFile) continue;
      const lineStarts = computeLineStarts(sourceFile.text);
      for (const u of checker.getDeprecatedUses(sourceFile)) {
        const start = getLineAndCharacterOfPosition(lineStarts, u.pos);
        const end = getLineAndCharacterOfPosition(lineStarts, u.end);
        const kindWord = { method: "Method", type: "Type", field: "Field" }[u.kind];
        const message = `${kindWord} '${u.name}' is deprecated${
          u.since ? ` (since ${u.since})` : ""
        }${u.forRemoval ? "; marked for removal" : ""}.`;
        out.push({
          file: displayFile(uri),
          line: start.line + 1,
          column: start.character + 1,
          endLine: end.line + 1,
          endColumn: end.character + 1,
          name: u.name,
          kind: u.kind,
          ...(u.since !== undefined ? { since: u.since } : {}),
          forRemoval: u.forRemoval,
          message,
        });
      }
    }
    return { deprecatedUses: out };
  }

  function outline(args: { file: string }): { symbols: DocumentSymbol[] } {
    const sourceFile = program.getSourceFile(pathToUri(args.file));
    if (!sourceFile) return { symbols: [] };
    return { symbols: getDocumentSymbols(sourceFile, computeLineStarts(sourceFile.text)) };
  }

  function searchSymbols(args: { query: string }): { matches: string[] } {
    const q = args.query.toLowerCase();
    const matches = program
      .getGlobalIndex()
      .getAllTypeFqns()
      .filter(fqn => fqn.toLowerCase().includes(q));
    return { matches };
  }

  function describe(symbol: Symbol): McpMatch {
    const declaration = getDeclarationNameNode(symbol);
    const signature = checker.signatureOfSymbol(symbol);
    const documentation = checker.getDocumentation(symbol);
    return {
      kind: symbolKindWord(symbol.flags),
      label: getHoverText(checker, symbol),
      ...(signature ? { signature } : {}),
      ...(documentation ? { documentation } : {}),
      ...(declaration ? { definition: nodeLocation(declaration) } : {}),
    };
  }

  function describeSymbol(args: { ref: string }): { matches: McpMatch[] } {
    const symbols = resolveSymbolRef(args.ref, program.getGlobalIndex());
    return { matches: symbols.map(describe) };
  }

  function findDefinition(args: { ref: string }): { definitions: McpLocation[] } {
    const symbols = resolveSymbolRef(args.ref, program.getGlobalIndex());
    const definitions: McpLocation[] = [];
    for (const symbol of symbols) {
      const declaration = getDeclarationNameNode(symbol);
      if (declaration) definitions.push(nodeLocation(declaration));
    }
    return { definitions };
  }

  function findReferencesTool(args: { ref: string }): {
    references: McpLocation[];
    ambiguous?: boolean;
    candidates?: number;
  } {
    const symbols = resolveSymbolRef(args.ref, program.getGlobalIndex());
    if (symbols.length === 0) return { references: [] };
    if (symbols.length > 1) {
      return { references: [], ambiguous: true, candidates: symbols.length };
    }
    const references = findReferences(symbols[0], program, checker.resolveName).map(nodeLocation);
    return { references };
  }

  // For a type: its transitive subtypes (who implements/extends it). For a
  // method: the concrete overrides in those subtypes.
  function findImplementations(args: { ref: string }): {
    implementations: McpMatch[];
    ambiguous?: boolean;
    candidates?: number;
  } {
    const symbols = resolveSymbolRef(args.ref, program.getGlobalIndex());
    if (symbols.length === 0) return { implementations: [] };
    if (symbols.length > 1) {
      return { implementations: [], ambiguous: true, candidates: symbols.length };
    }
    const symbol = symbols[0];
    const impls: Symbol[] = [];
    if (symbol.flags & (SymbolFlags.Method | SymbolFlags.Constructor)) {
      const declaration = (symbol.valueDeclaration ?? symbol.declarations?.[0]) as
        | MethodDeclaration
        | undefined;
      if (declaration) {
        for (const override of findMethodImplementations(declaration, program)) {
          if (override.symbol) impls.push(override.symbol);
        }
      }
    } else {
      impls.push(...getSubtypeIndex(program).allSubtypesOf(symbol));
    }
    return { implementations: impls.map(describe) };
  }

  // The transitive supertype symbols of a type (extends/implements, walked up),
  // nearest first, deduped.
  function supertypesOf(typeSymbol: Symbol): Symbol[] {
    const out: Symbol[] = [];
    const seen = new Set<Symbol>([typeSymbol]);
    const queue = getDirectSuperTypeSymbols(typeSymbol, program);
    while (queue.length > 0) {
      const next = queue.shift()!;
      if (seen.has(next)) continue;
      seen.add(next);
      out.push(next);
      queue.push(...getDirectSuperTypeSymbols(next, program));
    }
    return out;
  }

  function listMembers(args: { ref: string }): {
    members: McpMember[];
    ambiguous?: boolean;
    candidates?: number;
  } {
    const symbols = resolveSymbolRef(args.ref, program.getGlobalIndex());
    if (symbols.length === 0) return { members: [] };
    if (symbols.length > 1) {
      return { members: [], ambiguous: true, candidates: symbols.length };
    }
    const members: McpMember[] = [];
    const seenNames = new Set<string>();
    const addFrom = (type: Symbol, inherited: boolean): void => {
      for (const [name, member] of type.members ?? []) {
        if (seenNames.has(name)) continue; // a closer declaration shadows it
        seenNames.add(name);
        members.push({ ...describe(member), inherited });
      }
    };
    addFrom(symbols[0], false);
    for (const superType of supertypesOf(symbols[0])) addFrom(superType, true);
    return { members };
  }

  function findCallers(args: { ref: string }): {
    callers: McpLocation[];
    ambiguous?: boolean;
    candidates?: number;
  } {
    const symbols = resolveSymbolRef(args.ref, program.getGlobalIndex());
    if (symbols.length === 0) return { callers: [] };
    if (symbols.length > 1) {
      return { callers: [], ambiguous: true, candidates: symbols.length };
    }
    // A reference is a caller when it is the callee identifier of a call.
    const callers = findReferences(symbols[0], program, checker.resolveName)
      .filter(node => enclosingCall(node as Identifier) !== undefined)
      .map(nodeLocation);
    return { callers };
  }

  function typeHierarchy(args: { ref: string }): {
    supertypes: McpMatch[];
    subtypes: McpMatch[];
    ambiguous?: boolean;
    candidates?: number;
  } {
    const symbols = resolveSymbolRef(args.ref, program.getGlobalIndex());
    if (symbols.length === 0) return { supertypes: [], subtypes: [] };
    if (symbols.length > 1) {
      return { supertypes: [], subtypes: [], ambiguous: true, candidates: symbols.length };
    }
    return {
      supertypes: supertypesOf(symbols[0]).map(describe),
      subtypes: getSubtypeIndex(program).allSubtypesOf(symbols[0]).map(describe),
    };
  }

  // Import candidates for an unqualified type name (the FQNs a `import` could
  // name); default-package types have no dotted name and are dropped.
  function resolveImport(args: { name: string }): { imports: string[] } {
    const imports = program
      .getGlobalIndex()
      .findFqnsBySimpleName(args.name)
      .filter(fqn => fqn.includes("."));
    return { imports };
  }

  function isStubSymbol(symbol: Symbol): boolean {
    const declaration = getDeclarationNameNode(symbol);
    return !!declaration && isSyntheticUri(getSourceFileOfNode(declaration).fileName);
  }

  // The edits a workspace rename would make - one per reference - returned for
  // the agent to apply itself (this tool never writes). JDK symbols and invalid
  // identifiers are refused, matching the LSP rename provider.
  function renameSymbol(args: { ref: string; newName: string }): {
    edits: McpEdit[];
    error?: string;
    ambiguous?: boolean;
    candidates?: number;
  } {
    if (!isValidIdentifier(args.newName)) {
      return { edits: [], error: `'${args.newName}' is not a valid Java identifier.` };
    }
    const symbols = resolveSymbolRef(args.ref, program.getGlobalIndex());
    if (symbols.length === 0) return { edits: [] };
    if (symbols.length > 1) {
      return { edits: [], ambiguous: true, candidates: symbols.length };
    }
    if (isStubSymbol(symbols[0])) {
      return { edits: [], error: "Cannot rename a symbol defined by the JDK." };
    }
    const edits = findReferences(symbols[0], program, checker.resolveName).map(node => ({
      ...nodeLocation(node),
      newText: args.newName,
    }));
    return { edits };
  }

  return {
    diagnostics,
    deprecatedUses,
    outline,
    searchSymbols,
    describeSymbol,
    findDefinition,
    findReferences: findReferencesTool,
    findImplementations,
    listMembers,
    findCallers,
    typeHierarchy,
    resolveImport,
    renameSymbol,
  };
}
