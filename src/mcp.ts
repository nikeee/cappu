// MCP tool handlers: a thin, name-addressed, JSON-returning layer over the
// engine (Program + Checker). Handlers are pure over the current Program state;
// disk freshness and transport live in mcpServer.ts. Locations are 1-based and
// use filesystem paths so agents can act on them directly.

import type { DocumentSymbol } from "vscode-languageserver-types";

import type { Checker } from "./checker.ts";
import { getDocumentSymbols } from "./documentSymbols.ts";
import { getHoverText, symbolKindWord } from "./hover.ts";
import { computeLineStarts, getLineAndCharacterOfPosition } from "./lineMap.ts";
import { resolveSymbolRef } from "./mcpResolve.ts";
import type { Program } from "./program.ts";
import { findReferences, getDeclarationNameNode, getSourceFileOfNode } from "./resolver.ts";
import { DiagnosticCategory, type Diagnostic, type Node, type Symbol } from "./types.ts";
import { skipTrivia } from "./utilities.ts";
import { pathToUri, uriToPath } from "./workspace.ts";

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

// node.pos includes leading trivia; advance to the token's real start so the
// reported location points at the name, mirroring server.ts:rangeOf.
export function nodeLocation(node: Node): McpLocation {
  const file = getSourceFileOfNode(node);
  const lineStarts = computeLineStarts(file.text);
  const start = getLineAndCharacterOfPosition(lineStarts, skipTrivia(file.text, node.pos));
  const end = getLineAndCharacterOfPosition(lineStarts, node.end);
  return {
    file: uriToPath(file.fileName),
    line: start.line + 1,
    column: start.character + 1,
    endLine: end.line + 1,
    endColumn: end.character + 1,
  };
}

function formatDiagnostic(
  uri: string,
  d: Diagnostic,
  lineStarts: readonly number[],
): McpDiagnostic {
  const start = getLineAndCharacterOfPosition(lineStarts, d.pos);
  const end = getLineAndCharacterOfPosition(lineStarts, d.end);
  return {
    file: uriToPath(uri),
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

export interface McpTools {
  diagnostics(args: { files?: string[] }): { diagnostics: McpDiagnostic[] };
  outline(args: { file: string }): { symbols: DocumentSymbol[] };
  searchSymbols(args: { query: string }): { matches: string[] };
  describeSymbol(args: { ref: string }): { matches: McpMatch[] };
  findDefinition(args: { ref: string }): { definitions: McpLocation[] };
  findReferences(args: {
    ref: string;
  }): { references: McpLocation[]; ambiguous?: boolean; candidates?: number };
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

  function findReferencesTool(args: {
    ref: string;
  }): { references: McpLocation[]; ambiguous?: boolean; candidates?: number } {
    const symbols = resolveSymbolRef(args.ref, program.getGlobalIndex());
    if (symbols.length === 0) return { references: [] };
    if (symbols.length > 1) {
      return { references: [], ambiguous: true, candidates: symbols.length };
    }
    const references = findReferences(symbols[0], program, checker.resolveName).map(nodeLocation);
    return { references };
  }

  return {
    diagnostics,
    outline,
    searchSymbols,
    describeSymbol,
    findDefinition,
    findReferences: findReferencesTool,
  };
}
