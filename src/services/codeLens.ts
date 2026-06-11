// Code lenses: a reference count over every type and method declaration in a
// file, and an implementation count over interfaces, abstract classes and
// their abstract methods. Reference counts come from ONE pass over the
// workspace (resolving an identifier is memoized per node); implementation
// counts come from the generation-memoized subtype index, so they are
// transitive (B extends A, A implements I counts B under I).

import type { Checker } from "../compiler/checker.ts";
import { forEachChild } from "../compiler/parser.ts";
import type { Program } from "../compiler/program.ts";
import { declarationName, findMethodImplementations, getSubtypeIndex } from "./subtypes.ts";
import {
  type ClassDeclaration,
  type Identifier,
  type MethodDeclaration,
  type Node,
  type SourceFile,
  type Symbol,
  SyntaxKind,
} from "../compiler/types.ts";
import { isSyntheticUri } from "../workspace.ts";

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
  readonly kind: "references" | "implementations";
  /** Reference or implementation sites, excluding the declaration itself. */
  readonly sites: Node[];
}

function isDeclarationName(node: Identifier, symbol: Symbol): boolean {
  const parent = node.parent;
  return !!parent && parent.symbol === symbol && (parent as { name?: Node }).name === node;
}

function hasAbstractModifier(node: { modifiers?: readonly Node[] }): boolean {
  return (node.modifiers ?? []).some(m => m.kind === SyntaxKind.AbstractKeyword);
}

/** The abstract methods of an interface or abstract class declaration. */
export function abstractMethodsOf(declaration: Node): MethodDeclaration[] {
  const members = (declaration as { members?: readonly Node[] }).members ?? [];
  const isInterface = declaration.kind === SyntaxKind.InterfaceDeclaration;
  return members.filter(
    (m): m is MethodDeclaration =>
      m.kind === SyntaxKind.MethodDeclaration &&
      (isInterface
        ? !(m as MethodDeclaration).body // default/static interface methods have one
        : hasAbstractModifier(m as MethodDeclaration)),
  );
}

export function getCodeLenses(
  program: Program,
  checker: Checker,
  sourceFile: SourceFile,
): CodeLensEntry[] {
  // Reference targets: every type/method declaration in this file.
  const refTargets = new Map<Symbol, CodeLensEntry>();
  const entries: CodeLensEntry[] = [];
  const subtypes = getSubtypeIndex(program);

  const collect = (node: Node): void => {
    const name = (node as { name?: Identifier }).name;
    if (name && node.symbol) {
      if (LENS_DECLARATIONS.has(node.kind) && !refTargets.has(node.symbol)) {
        const entry: CodeLensEntry = { name, kind: "references", sites: [] };
        refTargets.set(node.symbol, entry);
        entries.push(entry);
      }
      const isImplTarget =
        node.kind === SyntaxKind.InterfaceDeclaration ||
        (node.kind === SyntaxKind.ClassDeclaration &&
          hasAbstractModifier(node as ClassDeclaration));
      if (isImplTarget) {
        entries.push({
          name,
          kind: "implementations",
          sites: subtypes
            .allSubtypesOf(node.symbol)
            .map(declarationName)
            .filter((n): n is Identifier => n !== undefined),
        });
        for (const method of abstractMethodsOf(node)) {
          entries.push({
            name: method.name,
            kind: "implementations",
            sites: findMethodImplementations(method, program).map(m => m.name),
          });
        }
      }
    }
    forEachChild(node, child => {
      collect(child);
      return undefined;
    });
  };
  collect(sourceFile);
  if (refTargets.size === 0) return entries;

  // One workspace pass for the reference counts: every resolved identifier that
  // names a target counts, except the declaration's own name. Stub files cannot
  // reference user code.
  for (const uri of program.getAllUris()) {
    if (isSyntheticUri(uri)) continue;
    const file = program.getSourceFile(uri);
    if (!file) continue;
    const visit = (node: Node): void => {
      if (node.kind === SyntaxKind.Identifier) {
        const symbol = checker.resolveName(node as Identifier);
        const entry = symbol ? refTargets.get(symbol) : undefined;
        if (entry && !isDeclarationName(node as Identifier, symbol!)) {
          entry.sites.push(node);
        }
      }
      forEachChild(node, child => {
        visit(child);
        return undefined;
      });
    };
    visit(file);
  }
  return entries;
}
