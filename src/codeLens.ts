// Code lenses: a reference count over every type and method declaration in a
// file, and an implementation count over interfaces, abstract classes and
// their abstract methods. All counts are gathered in ONE pass over the
// workspace (resolving an identifier is memoized per node), instead of one
// search per declaration; the LSP server turns entries into protocol CodeLens
// objects.

import type { Checker } from "./checker.ts";
import { forEachChild } from "./parser.ts";
import type { Program } from "./program.ts";
import { resolveTypeEntityName } from "./resolver.ts";
import {
  type ClassDeclaration,
  type Identifier,
  type MethodDeclaration,
  type Node,
  type SourceFile,
  type Symbol,
  SyntaxKind,
  type TypeNode,
  type TypeReference,
} from "./types.ts";

const LENS_DECLARATIONS = new Set<SyntaxKind>([
  SyntaxKind.ClassDeclaration,
  SyntaxKind.InterfaceDeclaration,
  SyntaxKind.EnumDeclaration,
  SyntaxKind.RecordDeclaration,
  SyntaxKind.AnnotationTypeDeclaration,
  SyntaxKind.MethodDeclaration,
]);

const TYPE_DECLARATIONS = new Set<SyntaxKind>([
  SyntaxKind.ClassDeclaration,
  SyntaxKind.InterfaceDeclaration,
  SyntaxKind.EnumDeclaration,
  SyntaxKind.RecordDeclaration,
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

// The written super types of a type declaration (extends + implements).
function superTypeNodes(node: Node): readonly TypeNode[] {
  const d = node as {
    extendsType?: TypeNode;
    extendsTypes?: readonly TypeNode[];
    implementsTypes?: readonly TypeNode[];
  };
  return [
    ...(d.extendsType ? [d.extendsType] : []),
    ...(d.extendsTypes ?? []),
    ...(d.implementsTypes ?? []),
  ];
}

export function getCodeLenses(
  program: Program,
  checker: Checker,
  sourceFile: SourceFile,
): CodeLensEntry[] {
  // Reference targets: every type/method declaration in this file.
  const refTargets = new Map<Symbol, CodeLensEntry>();
  // Implementation targets: interfaces and abstract classes in this file, each
  // carrying its abstract methods so implementing members can be matched.
  interface ImplTarget {
    readonly entry: CodeLensEntry;
    readonly abstractMethods: Map<MethodDeclaration, CodeLensEntry>;
  }
  const implTargets = new Map<Symbol, ImplTarget>();

  const abstractMethodsOf = (declaration: Node): MethodDeclaration[] => {
    const members = (declaration as { members?: readonly Node[] }).members ?? [];
    const isInterface = declaration.kind === SyntaxKind.InterfaceDeclaration;
    return members.filter(
      (m): m is MethodDeclaration =>
        m.kind === SyntaxKind.MethodDeclaration &&
        (isInterface
          ? !(m as MethodDeclaration).body // default/static interface methods have one
          : hasAbstractModifier(m as MethodDeclaration)),
    );
  };

  const collect = (node: Node): void => {
    const name = (node as { name?: Identifier }).name;
    if (name && node.symbol) {
      if (LENS_DECLARATIONS.has(node.kind) && !refTargets.has(node.symbol)) {
        refTargets.set(node.symbol, { name, kind: "references", sites: [] });
      }
      const isImplTarget =
        node.kind === SyntaxKind.InterfaceDeclaration ||
        (node.kind === SyntaxKind.ClassDeclaration &&
          hasAbstractModifier(node as ClassDeclaration));
      if (isImplTarget && !implTargets.has(node.symbol)) {
        implTargets.set(node.symbol, {
          entry: { name, kind: "implementations", sites: [] },
          abstractMethods: new Map(
            abstractMethodsOf(node).map(m => [
              m,
              { name: m.name, kind: "implementations", sites: [] } satisfies CodeLensEntry,
            ]),
          ),
        });
      }
    }
    forEachChild(node, child => {
      collect(child);
      return undefined;
    });
  };
  collect(sourceFile);
  if (refTargets.size === 0 && implTargets.size === 0) return [];

  // One workspace pass. References: every resolved identifier naming a target
  // (except the declaration's own name). Implementations: every type whose
  // extends/implements clause resolves to a target, plus its members matching a
  // target's abstract methods by name and arity. Stub files cannot reference or
  // implement user code, so jdk:/// is skipped.
  for (const uri of program.getAllUris()) {
    if (uri.startsWith("jdk:")) continue;
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
      if (TYPE_DECLARATIONS.has(node.kind)) {
        for (const superType of superTypeNodes(node)) {
          if (superType.kind !== SyntaxKind.TypeReference) continue;
          const superSymbol = resolveTypeEntityName(
            (superType as TypeReference).typeName,
            node,
            program,
          );
          const target = superSymbol ? implTargets.get(superSymbol) : undefined;
          if (!target) continue;
          const name = (node as { name?: Identifier }).name;
          if (name) target.entry.sites.push(name);
          // Match each abstract method against this type's members.
          const members = (node as { members?: readonly Node[] }).members ?? [];
          for (const [abstractMethod, methodEntry] of target.abstractMethods) {
            for (const member of members) {
              if (member.kind !== SyntaxKind.MethodDeclaration) continue;
              const method = member as MethodDeclaration;
              if (
                method.body &&
                method.name.text === abstractMethod.name.text &&
                method.parameters.length === abstractMethod.parameters.length
              ) {
                methodEntry.sites.push(method.name);
              }
            }
          }
        }
      }
      forEachChild(node, child => {
        visit(child);
        return undefined;
      });
    };
    visit(file);
  }

  const result = [...refTargets.values()];
  for (const target of implTargets.values()) {
    result.push(target.entry, ...target.abstractMethods.values());
  }
  return result;
}
