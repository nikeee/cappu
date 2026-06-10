// Code lenses: a reference count over every type and method declaration in a
// file. All counts are gathered in ONE pass over the workspace (resolving an
// identifier is memoized per node), instead of one reference-search per
// declaration; the LSP server turns entries into protocol CodeLens objects.

import type { Checker } from "./checker.ts";
import { forEachChild } from "./parser.ts";
import type { Program } from "./program.ts";
import {
  type Identifier,
  type Node,
  type SourceFile,
  type Symbol,
  SyntaxKind,
} from "./types.ts";

const LENS_DECLARATIONS = new Set<SyntaxKind>([
  SyntaxKind.ClassDeclaration,
  SyntaxKind.InterfaceDeclaration,
  SyntaxKind.EnumDeclaration,
  SyntaxKind.RecordDeclaration,
  SyntaxKind.AnnotationTypeDeclaration,
  SyntaxKind.MethodDeclaration,
]);

export interface CodeLensEntry {
  /** The declaration's name node (the lens anchors to its range). */
  readonly name: Identifier;
  /** Reference sites across the workspace, excluding the declaration itself. */
  readonly references: Node[];
}

function isDeclarationName(node: Identifier, symbol: Symbol): boolean {
  const parent = node.parent;
  return !!parent && parent.symbol === symbol && (parent as { name?: Node }).name === node;
}

export function getCodeLenses(
  program: Program,
  checker: Checker,
  sourceFile: SourceFile,
): CodeLensEntry[] {
  // The declarations in this file that get a lens, keyed by their symbol.
  const targets = new Map<Symbol, CodeLensEntry>();
  const collect = (node: Node): void => {
    if (LENS_DECLARATIONS.has(node.kind)) {
      const name = (node as { name?: Identifier }).name;
      if (name && node.symbol && !targets.has(node.symbol)) {
        targets.set(node.symbol, { name, references: [] });
      }
    }
    forEachChild(node, child => {
      collect(child);
      return undefined;
    });
  };
  collect(sourceFile);
  if (targets.size === 0) return [];

  // One workspace pass: every resolved identifier that names a target counts,
  // except the declaration's own name. Stub files cannot reference user code.
  for (const uri of program.getAllUris()) {
    if (uri.startsWith("jdk:")) continue;
    const file = program.getSourceFile(uri);
    if (!file) continue;
    const visit = (node: Node): void => {
      if (node.kind === SyntaxKind.Identifier) {
        const symbol = checker.resolveName(node as Identifier);
        const entry = symbol ? targets.get(symbol) : undefined;
        if (entry && !isDeclarationName(node as Identifier, symbol!)) {
          entry.references.push(node);
        }
      }
      forEachChild(node, child => {
        visit(child);
        return undefined;
      });
    };
    visit(file);
  }
  return [...targets.values()];
}
