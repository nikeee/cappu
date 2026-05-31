// Name resolution: map a name-use Identifier to the Symbol it refers to.
//
// Lexical scope chain (JLS 6.5, simplified): block/method locals + type
// parameters -> type members (incl. inherited from super types) -> enclosing
// types -> single-type imports -> same package -> on-demand imports + java.lang
// -> fully-qualified names via the program's global index. Member access of an
// expression (a.b) needs the checker (P5) and is deferred.

import { forEachChild } from "./parser.ts";
import type { GlobalIndex, Program } from "./program.ts";
import { entityNameToString } from "./utilities.ts";
import {
  type ClassDeclaration,
  type EntityName,
  type EnumDeclaration,
  type Identifier,
  type InterfaceDeclaration,
  type Node,
  type RecordDeclaration,
  type SourceFile,
  type Symbol,
  SymbolFlags,
  SyntaxKind,
  type TypeNode,
  type TypeReference,
} from "./types.ts";

export const enum Meaning {
  Any,
  Type,
  Value,
}

function isTypeDeclaration(node: Node): boolean {
  switch (node.kind) {
    case SyntaxKind.ClassDeclaration:
    case SyntaxKind.InterfaceDeclaration:
    case SyntaxKind.EnumDeclaration:
    case SyntaxKind.AnnotationTypeDeclaration:
    case SyntaxKind.RecordDeclaration:
      return true;
    default:
      return false;
  }
}

function matchesMeaning(symbol: Symbol, meaning: Meaning): boolean {
  switch (meaning) {
    case Meaning.Type:
      return (symbol.flags & SymbolFlags.Type) !== 0;
    case Meaning.Value:
      return (
        (symbol.flags &
          (SymbolFlags.Field |
            SymbolFlags.Parameter |
            SymbolFlags.LocalVariable |
            SymbolFlags.EnumConstant |
            SymbolFlags.Method)) !==
        0
      );
    default:
      return true;
  }
}

export function getSourceFileOfNode(node: Node): SourceFile {
  let current = node;
  while (current.kind !== SyntaxKind.SourceFile) {
    current = current.parent;
  }
  return current as SourceFile;
}

function packageNameOf(sourceFile: SourceFile): string {
  return sourceFile.packageDeclaration
    ? entityNameToString(sourceFile.packageDeclaration.name)
    : "";
}

function lastSegment(qualified: string): string {
  const dot = qualified.lastIndexOf(".");
  return dot < 0 ? qualified : qualified.slice(dot + 1);
}

// --- inheritance-aware member lookup -------------------------------------------------

// Guards against cycles in the super-type graph while resolving inherited members.
const resolvingSupertypes = new Set<Symbol>();

function superTypeNodes(declaration: Node): readonly TypeNode[] {
  switch (declaration.kind) {
    case SyntaxKind.ClassDeclaration: {
      const c = declaration as ClassDeclaration;
      return [...(c.extendsType ? [c.extendsType] : []), ...(c.implementsTypes ?? [])];
    }
    case SyntaxKind.InterfaceDeclaration:
      return (declaration as InterfaceDeclaration).extendsTypes ?? [];
    case SyntaxKind.EnumDeclaration:
      return (declaration as EnumDeclaration).implementsTypes ?? [];
    case SyntaxKind.RecordDeclaration:
      return (declaration as RecordDeclaration).implementsTypes ?? [];
    default:
      return [];
  }
}

function superTypeSymbols(typeSymbol: Symbol, program: Program): Symbol[] {
  if (resolvingSupertypes.has(typeSymbol)) return [];
  const declaration = typeSymbol.valueDeclaration ?? typeSymbol.declarations?.[0];
  if (!declaration) return [];
  resolvingSupertypes.add(typeSymbol);
  try {
    const result: Symbol[] = [];
    for (const typeNode of superTypeNodes(declaration)) {
      if (typeNode.kind === SyntaxKind.TypeReference) {
        const symbol = resolveTypeEntityName(
          (typeNode as TypeReference).typeName,
          declaration,
          program,
        );
        if (symbol) result.push(symbol);
      }
    }
    return result;
  } finally {
    resolvingSupertypes.delete(typeSymbol);
  }
}

/** Look up a member by name in a type and its super types. */
export function lookupMember(
  typeSymbol: Symbol,
  name: string,
  meaning: Meaning,
  program: Program,
  seen = new Set<Symbol>(),
): Symbol | undefined {
  if (seen.has(typeSymbol)) return undefined;
  seen.add(typeSymbol);
  const own = typeSymbol.members?.get(name);
  if (own && matchesMeaning(own, meaning)) return own;
  for (const superSymbol of superTypeSymbols(typeSymbol, program)) {
    const inherited = lookupMember(superSymbol, name, meaning, program, seen);
    if (inherited) return inherited;
  }
  return undefined;
}

// --- scope chain ----------------------------------------------------------------------

function lookupInScopes(
  start: Node,
  name: string,
  meaning: Meaning,
  program: Program,
): Symbol | undefined {
  let node: Node | undefined = start;
  while (node) {
    if (isTypeDeclaration(node) && node.symbol) {
      const member = lookupMember(node.symbol, name, meaning, program);
      if (member) return member;
    } else {
      const local = node.locals?.get(name);
      if (local && matchesMeaning(local, meaning)) return local;
    }
    node = node.parent;
  }
  return undefined;
}

// --- cross-file type resolution -------------------------------------------------------

function resolveTypeNameCrossFile(
  name: string,
  sourceFile: SourceFile,
  index: GlobalIndex,
): Symbol | undefined {
  // single-type imports
  for (const imp of sourceFile.imports) {
    if (!imp.isStatic && !imp.isOnDemand) {
      const fqn = entityNameToString(imp.name);
      if (lastSegment(fqn) === name) {
        const type = index.getType(fqn);
        if (type) return type;
      }
    }
  }
  // same package
  const samePackage = index.getPackageTypes(packageNameOf(sourceFile))?.get(name);
  if (samePackage) return samePackage;
  // on-demand imports
  for (const imp of sourceFile.imports) {
    if (!imp.isStatic && imp.isOnDemand) {
      const type = index.getPackageTypes(entityNameToString(imp.name))?.get(name);
      if (type) return type;
    }
  }
  // implicit java.lang.*
  return index.getPackageTypes("java.lang")?.get(name);
}

function resolveTypeName(name: string, fromNode: Node, program: Program): Symbol | undefined {
  const lexical = lookupInScopes(fromNode, name, Meaning.Type, program);
  if (lexical) return lexical;
  return resolveTypeNameCrossFile(name, getSourceFileOfNode(fromNode), program.getGlobalIndex());
}

/** Resolve an entity name used as a type (Identifier, qualified, or nested). */
export function resolveTypeEntityName(
  name: EntityName,
  fromNode: Node,
  program: Program,
): Symbol | undefined {
  if (name.kind === SyntaxKind.Identifier) {
    return resolveTypeName((name as Identifier).text, fromNode, program);
  }
  const fqn = entityNameToString(name);
  const byFqn = program.getGlobalIndex().getType(fqn);
  if (byFqn) return byFqn;
  // nested type: resolve the left as a type, then look up the right member type
  const qualified = name as { left: EntityName; right: Identifier };
  const leftType = resolveTypeEntityName(qualified.left, fromNode, program);
  if (leftType) return lookupMember(leftType, qualified.right.text, Meaning.Type, program);
  return undefined;
}

// --- identifier classification --------------------------------------------------------

function declarationOf(identifier: Identifier): Node | undefined {
  const parent = identifier.parent;
  if (parent && parent.symbol && (parent as { name?: Node }).name === identifier) return parent;
  return undefined;
}

function isQualifiedTypeNameTail(identifier: Identifier): boolean {
  const parent = identifier.parent;
  return (
    !!parent &&
    parent.kind === SyntaxKind.QualifiedName &&
    (parent as { right?: Node }).right === identifier &&
    meaningOf(identifier) === Meaning.Type
  );
}

function isExpressionMemberAccess(identifier: Identifier): boolean {
  const parent = identifier.parent;
  if (!parent) return false;
  return (
    (parent.kind === SyntaxKind.PropertyAccessExpression ||
      parent.kind === SyntaxKind.MethodReferenceExpression) &&
    (parent as { name?: Node }).name === identifier
  );
}

function meaningOf(identifier: Identifier): Meaning {
  let node: Node | undefined = identifier;
  while (node) {
    if (node.kind === SyntaxKind.TypeReference) return Meaning.Type;
    if (node.kind !== SyntaxKind.QualifiedName && node.kind !== SyntaxKind.Identifier) break;
    node = node.parent;
  }
  return Meaning.Any;
}

/** Resolve a name-use identifier to its declaration symbol, or undefined. */
export function resolveIdentifier(identifier: Identifier, program: Program): Symbol | undefined {
  const declaration = declarationOf(identifier);
  if (declaration) return declaration.symbol;

  if (isQualifiedTypeNameTail(identifier)) {
    return resolveTypeEntityName(identifier.parent as EntityName, identifier, program);
  }
  if (isExpressionMemberAccess(identifier)) return undefined; // needs the checker (P5)

  const meaning = meaningOf(identifier);
  const lexical = lookupInScopes(identifier, identifier.text, meaning, program);
  if (lexical) return lexical;
  if (meaning !== Meaning.Value) {
    return resolveTypeNameCrossFile(
      identifier.text,
      getSourceFileOfNode(identifier),
      program.getGlobalIndex(),
    );
  }
  return undefined;
}

export function getDeclarationNameNode(symbol: Symbol): Node | undefined {
  const declaration = symbol.valueDeclaration ?? symbol.declarations?.[0];
  if (!declaration) return undefined;
  return (declaration as { name?: Node }).name ?? declaration;
}

// --- find references ------------------------------------------------------------------

let referenceCache: { index: GlobalIndex; table: Map<Symbol, Node[]> } | undefined;

function forEachDescendant(node: Node, cb: (n: Node) => void): void {
  cb(node);
  forEachChild(node, child => {
    forEachDescendant(child, cb);
    return undefined;
  });
}

function buildReferenceTable(program: Program): Map<Symbol, Node[]> {
  const table = new Map<Symbol, Node[]>();
  for (const uri of program.getAllUris()) {
    const sourceFile = program.getSourceFile(uri);
    if (!sourceFile) continue;
    forEachDescendant(sourceFile, node => {
      if (node.kind !== SyntaxKind.Identifier) return;
      const symbol = resolveIdentifier(node as Identifier, program);
      if (!symbol) return;
      const list = table.get(symbol);
      if (list) list.push(node);
      else table.set(symbol, [node]);
    });
  }
  return table;
}

/** All identifier nodes (uses and declaration names) that refer to a symbol. */
export function findReferences(symbol: Symbol, program: Program): Node[] {
  const index = program.getGlobalIndex();
  if (referenceCache?.index !== index) {
    referenceCache = { index, table: buildReferenceTable(program) };
  }
  return referenceCache.table.get(symbol) ?? [];
}
