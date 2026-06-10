// Reverse subtype index: super type symbol -> the type declarations that
// directly extend/implement it, built in one workspace pass and memoized on the
// program generation. Backs textDocument/implementation, transitive
// implementation counts in code lenses, and (later) type/call hierarchy.

import { forEachChild } from "./parser.ts";
import type { Program } from "./program.ts";
import { resolveTypeEntityName } from "./resolver.ts";
import { isSyntheticUri } from "./workspace.ts";
import {
  type Identifier,
  type MethodDeclaration,
  type Node,
  type Symbol,
  SyntaxKind,
  type TypeNode,
  type TypeReference,
} from "./types.ts";

const TYPE_DECLARATIONS = new Set<SyntaxKind>([
  SyntaxKind.ClassDeclaration,
  SyntaxKind.InterfaceDeclaration,
  SyntaxKind.EnumDeclaration,
  SyntaxKind.RecordDeclaration,
]);

export interface SubtypeIndex {
  /** Direct subtypes (declarations whose extends/implements names the symbol). */
  directSubtypesOf(superType: Symbol): readonly Symbol[];
  /** All transitive subtypes, the direct ones first (BFS order, no duplicates). */
  allSubtypesOf(superType: Symbol): Symbol[];
}

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

const indexCache = new WeakMap<Program, { generation: number; index: SubtypeIndex }>();

export function getSubtypeIndex(program: Program): SubtypeIndex {
  const generation = program.getGeneration();
  const cached = indexCache.get(program);
  if (cached && cached.generation === generation) return cached.index;

  const direct = new Map<Symbol, Symbol[]>();
  for (const uri of program.getAllUris()) {
    if (isSyntheticUri(uri)) continue; // stub types never extend user code
    const sourceFile = program.getSourceFile(uri);
    if (!sourceFile) continue;
    const visit = (node: Node): void => {
      if (TYPE_DECLARATIONS.has(node.kind) && node.symbol) {
        for (const superType of superTypeNodes(node)) {
          if (superType.kind !== SyntaxKind.TypeReference) continue;
          const superSymbol = resolveTypeEntityName(
            (superType as TypeReference).typeName,
            node,
            program,
          );
          if (!superSymbol) continue;
          const list = direct.get(superSymbol);
          if (list) list.push(node.symbol);
          else direct.set(superSymbol, [node.symbol]);
        }
      }
      forEachChild(node, child => {
        visit(child);
        return undefined;
      });
    };
    visit(sourceFile);
  }

  const index: SubtypeIndex = {
    directSubtypesOf: superType => direct.get(superType) ?? [],
    allSubtypesOf(superType) {
      const seen = new Set<Symbol>();
      const queue = [...(direct.get(superType) ?? [])];
      const out: Symbol[] = [];
      while (queue.length > 0) {
        const next = queue.shift()!;
        if (seen.has(next)) continue;
        seen.add(next);
        out.push(next);
        queue.push(...(direct.get(next) ?? []));
      }
      return out;
    },
  };
  indexCache.set(program, { generation, index });
  return index;
}

/**
 * The concrete method declarations implementing/overriding `method` (matched by
 * name and arity) in the transitive subtypes of its declaring type.
 */
export function findMethodImplementations(
  method: MethodDeclaration,
  program: Program,
): MethodDeclaration[] {
  const owner = method.parent?.symbol;
  if (!owner) return [];
  const result: MethodDeclaration[] = [];
  for (const subtype of getSubtypeIndex(program).allSubtypesOf(owner)) {
    const declaration = subtype.valueDeclaration ?? subtype.declarations?.[0];
    const members = (declaration as { members?: readonly Node[] } | undefined)?.members ?? [];
    for (const member of members) {
      if (member.kind !== SyntaxKind.MethodDeclaration) continue;
      const candidate = member as MethodDeclaration;
      if (
        candidate.body &&
        candidate.name.text === method.name.text &&
        candidate.parameters.length === method.parameters.length
      ) {
        result.push(candidate);
      }
    }
  }
  return result;
}

/** The name node of a symbol's primary declaration, for jump targets. */
export function declarationName(symbol: Symbol): Identifier | undefined {
  const declaration = symbol.valueDeclaration ?? symbol.declarations?.[0];
  return (declaration as { name?: Identifier } | undefined)?.name;
}
