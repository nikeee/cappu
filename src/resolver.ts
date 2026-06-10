// Name resolution: map a name-use Identifier to the Symbol it refers to.
//
// Lexical scope chain (JLS 6.5, simplified): block/method locals + type
// parameters -> type members (incl. inherited from super types) -> enclosing
// types -> single-type imports -> same package -> on-demand imports + java.lang
// -> fully-qualified names via the program's global index. Member access of an
// expression (a.b) needs the checker (P5) and is deferred.

import { forEachChild } from "./parser.ts";
import { type Uri } from "./workspace.ts";
import type { Fqn, GlobalIndex, PackageName, Program } from "./program.ts";
import {
  type ClassDeclaration,
  type EntityName,
  type EnumDeclaration,
  type Identifier,
  type InterfaceDeclaration,
  type Node,
  type ObjectCreationExpression,
  type RecordDeclaration,
  type SourceFile,
  type Symbol,
  SymbolFlags,
  SyntaxKind,
  type TypeNode,
  type TypeReference,
} from "./types.ts";
import { entityNameToString } from "./utilities.ts";

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

function packageNameOf(sourceFile: SourceFile): PackageName {
  return (
    sourceFile.packageDeclaration ? entityNameToString(sourceFile.packageDeclaration.name) : ""
  ) as PackageName;
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

/** Direct super-type symbols (extends/implements) of a type symbol. */
export function getDirectSuperTypeSymbols(typeSymbol: Symbol, program: Program): Symbol[] {
  return superTypeSymbols(typeSymbol, program);
}

function superTypeSymbols(typeSymbol: Symbol, program: Program): Symbol[] {
  if (resolvingSupertypes.has(typeSymbol)) return [];
  const declaration = typeSymbol.valueDeclaration ?? typeSymbol.declarations?.[0];
  if (!declaration) return [];
  resolvingSupertypes.add(typeSymbol);
  try {
    const result: Symbol[] = [];
    // An enum implicitly extends java.lang.Enum (JLS 8.9): name(), ordinal(),
    // compareTo(), etc.
    if (declaration.kind === SyntaxKind.EnumDeclaration) {
      const enumSymbol = program.getGlobalIndex().getType("java.lang.Enum" as Fqn);
      if (enumSymbol) result.push(enumSymbol);
    }
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
  let prev: Node | undefined;
  while (node) {
    if (isTypeDeclaration(node) && node.symbol) {
      const member = lookupMember(node.symbol, name, meaning, program);
      if (member) return member;
    } else if (
      node.kind === SyntaxKind.ObjectCreationExpression &&
      (node as ObjectCreationExpression).classBody &&
      prev !== undefined &&
      ((node as ObjectCreationExpression).classBody as readonly Node[]).includes(prev)
    ) {
      // Inside an anonymous class body: members are inherited from the type it
      // extends/implements, so look them up on the supertype.
      const oce = node as ObjectCreationExpression;
      const target =
        oce.type.kind === SyntaxKind.TypeReference
          ? resolveTypeEntityName((oce.type as TypeReference).typeName, oce, program)
          : undefined;
      if (target) {
        const member = lookupMember(target, name, meaning, program);
        if (member) return member;
      }
    } else {
      const local = node.locals?.get(name);
      if (local && matchesMeaning(local, meaning)) return local;
    }
    prev = node;
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
      const fqn = entityNameToString(imp.name) as Fqn;
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
      const type = index.getPackageTypes(entityNameToString(imp.name) as PackageName)?.get(name);
      if (type) return type;
    }
  }
  // implicit java.lang.*
  return index.getPackageTypes("java.lang" as PackageName)?.get(name);
}

// A value/method imported via `import static T.member` or `import static T.*`
// (JLS 7.5.3/7.5.4): resolve the named type and look the member up on it.
function resolveStaticImport(
  name: string,
  sourceFile: SourceFile,
  program: Program,
): Symbol | undefined {
  const index = program.getGlobalIndex();
  for (const imp of sourceFile.imports) {
    if (!imp.isStatic) continue;
    const fqn = entityNameToString(imp.name) as Fqn;
    if (imp.isOnDemand) {
      const type = index.getType(fqn);
      const member = type && lookupMember(type, name, Meaning.Any, program);
      if (member) return member;
    } else if (lastSegment(fqn) === name) {
      const type = index.getType(fqn.slice(0, fqn.lastIndexOf(".")) as Fqn);
      const member = type && lookupMember(type, name, Meaning.Any, program);
      if (member) return member;
    }
  }
  return undefined;
}

function resolveTypeName(name: string, fromNode: Node, program: Program): Symbol | undefined {
  const lexical = lookupInScopes(fromNode, name, Meaning.Type, program);
  if (lexical) return lexical;
  return resolveTypeNameCrossFile(name, getSourceFileOfNode(fromNode), program.getGlobalIndex());
}

// Per-node resolution memo (the TypeScript compiler's nodeLinks pattern): a
// reparse creates fresh nodes, so stale entries die with their keys. `null`
// records a resolved-to-nothing answer (distinct from "not yet computed").
const typeNameLinks = new WeakMap<Node, Symbol | null>();

/** Resolve an entity name used as a type (Identifier, qualified, or nested). */
export function resolveTypeEntityName(
  name: EntityName,
  fromNode: Node,
  program: Program,
): Symbol | undefined {
  const cached = typeNameLinks.get(name);
  if (cached !== undefined) return cached ?? undefined;
  const result = resolveTypeEntityNameWorker(name, fromNode, program);
  typeNameLinks.set(name, result ?? null);
  return result;
}

function resolveTypeEntityNameWorker(
  name: EntityName,
  fromNode: Node,
  program: Program,
): Symbol | undefined {
  if (name.kind === SyntaxKind.Identifier) {
    return resolveTypeName((name as Identifier).text, fromNode, program);
  }
  const fqn = entityNameToString(name) as Fqn;
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
const identifierLinks = new WeakMap<Node, Symbol | null>();

export function resolveIdentifier(identifier: Identifier, program: Program): Symbol | undefined {
  const cached = identifierLinks.get(identifier);
  if (cached !== undefined) return cached ?? undefined;
  const result = resolveIdentifierWorker(identifier, program);
  identifierLinks.set(identifier, result ?? null);
  return result;
}

function resolveIdentifierWorker(identifier: Identifier, program: Program): Symbol | undefined {
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
    const type = resolveTypeNameCrossFile(
      identifier.text,
      getSourceFileOfNode(identifier),
      program.getGlobalIndex(),
    );
    if (type) return type;
  }
  // A statically-imported field or method used by its simple name.
  return resolveStaticImport(identifier.text, getSourceFileOfNode(identifier), program);
}

export function getDeclarationNameNode(symbol: Symbol): Node | undefined {
  const declaration = symbol.valueDeclaration ?? symbol.declarations?.[0];
  if (!declaration) return undefined;
  return (declaration as { name?: Node }).name ?? declaration;
}

// --- find references ------------------------------------------------------------------

function forEachDescendant(node: Node, cb: (n: Node) => void): void {
  cb(node);
  forEachChild(node, child => {
    forEachDescendant(child, cb);
    return undefined;
  });
}

// Locals, parameters and type parameters cannot be referenced outside the file
// that declares them, so narrow the search to that file. Everything else (types,
// fields, methods) may be referenced cross-file, so the whole workspace is scanned.
const FILE_LOCAL_FLAGS =
  SymbolFlags.LocalVariable | SymbolFlags.Parameter | SymbolFlags.TypeParameter;

function candidateUris(symbol: Symbol, program: Program): Uri[] {
  if (symbol.flags & FILE_LOCAL_FLAGS) {
    const declaration = symbol.valueDeclaration ?? symbol.declarations?.[0];
    // a SourceFile's fileName is the uri it was registered under
    if (declaration) return [getSourceFileOfNode(declaration).fileName as Uri];
  }
  return program.getAllUris();
}

/**
 * All identifier nodes (uses and declaration names) that refer to a symbol.
 *
 * `resolve` maps an identifier to its symbol. The default resolves lexical names
 * only; pass the checker's resolveName to also match member accesses (a.field),
 * which is required for a correct rename of fields and methods.
 */
export function findReferences(
  symbol: Symbol,
  program: Program,
  resolve: (id: Identifier) => Symbol | undefined = id => resolveIdentifier(id, program),
): Node[] {
  const result: Node[] = [];
  for (const uri of candidateUris(symbol, program)) {
    const sourceFile = program.getSourceFile(uri);
    if (!sourceFile) continue;
    forEachDescendant(sourceFile, node => {
      if (node.kind !== SyntaxKind.Identifier) return;
      if (resolve(node as Identifier) === symbol) result.push(node);
    });
  }
  return result;
}
