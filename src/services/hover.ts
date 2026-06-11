// Hover / quick-info rendering for a resolved symbol. Shared by the LSP server
// and the fourslash hover baselines so both render identically.

import type { Checker } from "../compiler/checker.ts";
import { typeToString, TypeKind } from "../compiler/checkerTypes.ts";
import { getSourceFileOfNode } from "../compiler/resolver.ts";
import {
  type CallExpression,
  type Identifier,
  type Node,
  type PropertyAccessExpression,
  type Symbol,
  SymbolFlags,
  SyntaxKind,
  type TypeParameter,
} from "../compiler/types.ts";
import { skipTrivia } from "../compiler/utilities.ts";

export function symbolKindWord(flags: SymbolFlags): string {
  if (flags & SymbolFlags.Package) return "package";
  if (flags & SymbolFlags.Class) return "class";
  if (flags & SymbolFlags.Interface) return "interface";
  if (flags & SymbolFlags.Enum) return "enum";
  if (flags & SymbolFlags.Record) return "record";
  if (flags & SymbolFlags.Annotation) return "@interface";
  if (flags & SymbolFlags.Constructor) return "constructor";
  if (flags & SymbolFlags.Method) return "method";
  if (flags & SymbolFlags.Field) return "field";
  if (flags & SymbolFlags.EnumConstant) return "enum constant";
  if (flags & SymbolFlags.Parameter) return "parameter";
  if (flags & SymbolFlags.TypeParameter) return "type parameter";
  if (flags & SymbolFlags.LocalVariable) return "local variable";
  return "symbol";
}

const TYPE_FLAGS =
  SymbolFlags.Class |
  SymbolFlags.Interface |
  SymbolFlags.Enum |
  SymbolFlags.Record |
  SymbolFlags.Annotation;

/** The call whose callee is this identifier (directly or via `recv.name`). */
export function enclosingCall(identifier: Identifier): CallExpression | undefined {
  const parent = identifier.parent;
  if (
    parent.kind === SyntaxKind.CallExpression &&
    (parent as CallExpression).expression === identifier
  ) {
    return parent as CallExpression;
  }
  if (
    parent.kind === SyntaxKind.PropertyAccessExpression &&
    (parent as PropertyAccessExpression).name === identifier &&
    parent.parent.kind === SyntaxKind.CallExpression &&
    (parent.parent as CallExpression).expression === parent
  ) {
    return parent.parent as CallExpression;
  }
  return undefined;
}

// The written bounds of a type parameter (`T extends Comparable<T> & Cloneable`),
// straight from the declaration source.
function typeParameterBounds(symbol: Symbol): string | undefined {
  const declaration = symbol.valueDeclaration ?? symbol.declarations?.[0];
  if (declaration?.kind !== SyntaxKind.TypeParameter) return undefined;
  const bounds = (declaration as TypeParameter).constraint;
  if (!bounds || bounds.length === 0) return undefined;
  const text = getSourceFileOfNode(declaration).text;
  return bounds.map(b => text.slice(skipTrivia(text, b.pos), b.end)).join(" & ");
}

/**
 * One-line hover label, in the style of the C#/Roslyn language service:
 *   methods  -> the full signature            e.g.  int add(int a, int b)
 *   types    -> keyword + name                e.g.  class Foo
 *   values   -> (kind) type name              e.g.  (field) int count
 *   type var -> (type parameter) name + bound e.g.  (type parameter) T extends CharSequence
 *
 * `atNode` is the referencing identifier, when hovering a use rather than the
 * declaration: a call renders the instantiated overload (`String get(int index)`
 * on a List<String>), and a member access renders the field's use-site type.
 */
export function getHoverText(checker: Checker, symbol: Symbol, atNode?: Node): string {
  if (symbol.flags & (SymbolFlags.Method | SymbolFlags.Constructor)) {
    const call =
      atNode?.kind === SyntaxKind.Identifier ? enclosingCall(atNode as Identifier) : undefined;
    const instantiated = call ? checker.instantiatedSignatureOfCall(call) : undefined;
    if (instantiated) return instantiated;
    const signature = checker.signatureOfSymbol(symbol);
    if (signature) return signature;
  }
  const word = symbolKindWord(symbol.flags);
  if (symbol.flags & (TYPE_FLAGS | SymbolFlags.Package)) {
    return `${word} ${symbol.escapedName}`;
  }
  if (symbol.flags & SymbolFlags.TypeParameter) {
    const bounds = typeParameterBounds(symbol);
    return bounds
      ? `(${word}) ${symbol.escapedName} extends ${bounds}`
      : `(${word}) ${symbol.escapedName}`;
  }
  const type = useSiteTypeString(checker, atNode) ?? checker.typeStringOfSymbol(symbol);
  // Omit an unresolvable type (e.g. a lambda parameter whose target is unknown)
  // rather than printing "<error>".
  return type === "<error>"
    ? `(${word}) ${symbol.escapedName}`
    : `(${word}) ${type} ${symbol.escapedName}`;
}

// The instantiated type of a field accessed through a generic receiver
// (`box.v` on a Box<String> is a String, not the declared T), when it is more
// informative than the declared type.
function useSiteTypeString(checker: Checker, atNode: Node | undefined): string | undefined {
  if (
    !atNode ||
    atNode.kind !== SyntaxKind.Identifier ||
    atNode.parent?.kind !== SyntaxKind.PropertyAccessExpression ||
    (atNode.parent as PropertyAccessExpression).name !== atNode
  ) {
    return undefined;
  }
  const type = checker.getTypeOfExpression(atNode.parent);
  if (type.kind === TypeKind.Error) return undefined;
  return typeToString(type);
}
