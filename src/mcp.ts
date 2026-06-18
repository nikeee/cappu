// MCP tool handlers: a thin, name-addressed, JSON-returning layer over the
// engine (Program + Checker). Handlers are pure over the current Program state;
// disk freshness and transport live in mcpServer.ts. Locations are 1-based and
// use filesystem paths so agents can act on them directly.

import type { DocumentSymbol } from "vscode-languageserver-types";

import type { Checker } from "./checker.ts";
import { getDocumentSymbols } from "./documentSymbols.ts";
import { computeLineStarts, getLineAndCharacterOfPosition } from "./lineMap.ts";
import type { Program } from "./program.ts";
import { getSourceFileOfNode } from "./resolver.ts";
import { DiagnosticCategory, type Diagnostic, type Node } from "./types.ts";
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

export interface McpTools {
  diagnostics(args: { files?: string[] }): { diagnostics: McpDiagnostic[] };
  outline(args: { file: string }): { symbols: DocumentSymbol[] };
  searchSymbols(args: { query: string }): { matches: string[] };
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

  return { diagnostics, outline, searchSymbols };
}
