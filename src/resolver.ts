// Name resolution: map a name-use Identifier to the Symbol it refers to, by
// walking the lexical scope chain (JLS 6.5, simplified). P2 covers single-file
// resolution of locals, parameters, type parameters, fields and file-local
// types. Imports, same-package, inheritance and the JDK stub are layered on in
// P3/P4; member access (a.b) needs the checker (P5).

import type { Program } from "./program.ts";
import {
  type Identifier,
  type Node,
  type SourceFile,
  type Symbol,
  SymbolFlags,
  type SymbolTable,
  SyntaxKind,
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

// The symbol table a node introduces: a type's members, or a container's locals.
function scopeTableOf(node: Node): SymbolTable | undefined {
  if (isTypeDeclaration(node)) return node.symbol?.members;
  return node.locals;
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

/** The node whose `name` is exactly this identifier (i.e. it is a declaration). */
function declarationOf(identifier: Identifier): Node | undefined {
  const parent = identifier.parent;
  if (parent && parent.symbol && (parent as { name?: Node }).name === identifier) {
    return parent;
  }
  return undefined;
}

// The right-hand side of a member access (a.b -> b) needs the type of the
// left side to resolve; deferred to the checker.
function isMemberAccessName(identifier: Identifier): boolean {
  const parent = identifier.parent;
  if (!parent) return false;
  if (parent.kind === SyntaxKind.QualifiedName && (parent as { right?: Node }).right === identifier)
    return true;
  if (
    parent.kind === SyntaxKind.PropertyAccessExpression &&
    (parent as { name?: Node }).name === identifier
  ) {
    return true;
  }
  if (
    parent.kind === SyntaxKind.MethodReferenceExpression &&
    (parent as { name?: Node }).name === identifier
  ) {
    return true;
  }
  return false;
}

function meaningOf(identifier: Identifier): Meaning {
  let node: Node | undefined = identifier;
  // Inside a TypeReference's name (directly or through a QualifiedName) -> Type.
  while (node) {
    if (node.kind === SyntaxKind.TypeReference) return Meaning.Type;
    if (node.kind !== SyntaxKind.QualifiedName && node.kind !== SyntaxKind.Identifier) break;
    node = node.parent;
  }
  return Meaning.Any;
}

function lookupInScopes(start: Node, name: string, meaning: Meaning): Symbol | undefined {
  let node: Node | undefined = start;
  while (node) {
    const symbol = scopeTableOf(node)?.get(name);
    if (symbol && matchesMeaning(symbol, meaning)) return symbol;
    node = node.parent;
  }
  return undefined;
}

/** Resolve a name-use identifier to its declaration symbol, or undefined. */
export function resolveIdentifier(identifier: Identifier, _program: Program): Symbol | undefined {
  const declaration = declarationOf(identifier);
  if (declaration) return declaration.symbol; // the identifier is itself a declaration name

  if (isMemberAccessName(identifier)) return undefined; // needs the checker (P5)

  return lookupInScopes(identifier, identifier.text, meaningOf(identifier));
}

/** Walk to the SourceFile that contains a node. */
export function getSourceFileOfNode(node: Node): SourceFile {
  let current = node;
  while (current.kind !== SyntaxKind.SourceFile) {
    current = current.parent;
  }
  return current as SourceFile;
}

/** The identifier name node of a declaration symbol (for goto-definition targets). */
export function getDeclarationNameNode(symbol: Symbol): Node | undefined {
  const declaration = symbol.valueDeclaration ?? symbol.declarations?.[0];
  if (!declaration) return undefined;
  return (declaration as { name?: Node }).name ?? declaration;
}
