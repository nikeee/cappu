// Semantic tokens: classify every resolved identifier so editors color fields,
// locals, parameters, type parameters etc. accurately. Offset-based and
// position-free; the LSP server encodes the entries into the wire format.

import type { Checker } from "../compiler/checker.ts";
import { symbolDeprecation } from "../compiler/deprecation.ts";
import { forEachChild } from "../compiler/parser.ts";
import { getSourceFileOfNode } from "../compiler/resolver.ts";
import {
  type CallExpression,
  type Identifier,
  type Node,
  type SourceFile,
  type Symbol,
  SymbolFlags,
  SyntaxKind,
} from "../compiler/types.ts";
import { skipTrivia } from "../compiler/utilities.ts";
import { isSyntheticUri } from "../workspace.ts";

// The legend the server advertises; indexes below refer into these arrays.
export const TOKEN_TYPES = [
  "namespace",
  "class",
  "interface",
  "enum",
  "enumMember",
  "typeParameter",
  "type",
  "method",
  "property",
  "parameter",
  "variable",
  "regexp",
] as const;

export const TOKEN_MODIFIERS = [
  "declaration",
  "static",
  "readonly",
  "defaultLibrary",
  "deprecated",
] as const;

const TYPE_INDEX = Object.fromEntries(TOKEN_TYPES.map((t, i) => [t, i]));
const MOD_BIT = Object.fromEntries(TOKEN_MODIFIERS.map((m, i) => [m, 1 << i]));

export interface SemanticTokenEntry {
  readonly offset: number;
  readonly length: number;
  readonly tokenType: number; // index into TOKEN_TYPES
  readonly tokenModifiers: number; // bit set over TOKEN_MODIFIERS
}

function tokenTypeOf(flags: SymbolFlags): number | undefined {
  if (flags & SymbolFlags.Package) return TYPE_INDEX["namespace"];
  if (flags & SymbolFlags.Class) return TYPE_INDEX["class"];
  if (flags & SymbolFlags.Interface) return TYPE_INDEX["interface"];
  if (flags & SymbolFlags.Enum) return TYPE_INDEX["enum"];
  if (flags & SymbolFlags.Record) return TYPE_INDEX["class"];
  if (flags & SymbolFlags.Annotation) return TYPE_INDEX["type"];
  if (flags & SymbolFlags.TypeParameter) return TYPE_INDEX["typeParameter"];
  if (flags & SymbolFlags.EnumConstant) return TYPE_INDEX["enumMember"];
  if (flags & (SymbolFlags.Method | SymbolFlags.Constructor)) return TYPE_INDEX["method"];
  if (flags & SymbolFlags.Field) return TYPE_INDEX["property"];
  if (flags & SymbolFlags.Parameter) return TYPE_INDEX["parameter"];
  if (flags & SymbolFlags.LocalVariable) return TYPE_INDEX["variable"];
  return undefined;
}

// The node whose modifiers govern this symbol: the field declaration for a
// declarator, otherwise the declaration itself.
function modifierCarrier(symbol: Symbol): { modifiers?: readonly Node[] } | undefined {
  const declaration = symbol.valueDeclaration ?? symbol.declarations?.[0];
  if (!declaration) return undefined;
  if (declaration.kind === SyntaxKind.VariableDeclarator) {
    return declaration.parent as { modifiers?: readonly Node[] };
  }
  return declaration as { modifiers?: readonly Node[] };
}

function modifiersOf(symbol: Symbol, isDeclarationName: boolean): number {
  let bits = isDeclarationName ? MOD_BIT["declaration"]! : 0;
  if (symbol.flags & SymbolFlags.EnumConstant) {
    bits |= MOD_BIT["static"]! | MOD_BIT["readonly"]!;
  } else {
    const carrier = modifierCarrier(symbol);
    for (const m of carrier?.modifiers ?? []) {
      if (m.kind === SyntaxKind.StaticKeyword) bits |= MOD_BIT["static"]!;
      if (m.kind === SyntaxKind.FinalKeyword) bits |= MOD_BIT["readonly"]!;
    }
  }
  const declaration = symbol.valueDeclaration ?? symbol.declarations?.[0];
  if (declaration && isSyntheticUri(getSourceFileOfNode(declaration).fileName)) {
    bits |= MOD_BIT["defaultLibrary"]!;
  }
  if (symbolDeprecation(symbol)) bits |= MOD_BIT["deprecated"]!;
  return bits;
}

// Standard-library methods whose named string parameters are regular
// expressions, keyed by "OwnerSimpleName#method" -> regex argument indices.
// A string literal passed at one of these positions is tokenized as `regexp`
// so clients can highlight the pattern (Java has no regex literal syntax).
const REGEX_SINKS: Record<string, ReadonlySet<number>> = {
  "Pattern#compile": new Set([0]),
  "Pattern#matches": new Set([0]),
  "String#matches": new Set([0]),
  "String#split": new Set([0]),
  "String#replaceAll": new Set([0]),
  "String#replaceFirst": new Set([0]),
};

// The simple name of the type declaration enclosing a declaration node.
function enclosingTypeName(node: Node): string | undefined {
  for (let n = node.parent; n; n = n.parent) {
    switch (n.kind) {
      case SyntaxKind.ClassDeclaration:
      case SyntaxKind.InterfaceDeclaration:
      case SyntaxKind.EnumDeclaration:
      case SyntaxKind.RecordDeclaration:
      case SyntaxKind.AnnotationTypeDeclaration:
        return (n as { name?: Identifier }).name?.text;
    }
  }
  return undefined;
}

export function getSemanticTokens(checker: Checker, sourceFile: SourceFile): SemanticTokenEntry[] {
  const entries: SemanticTokenEntry[] = [];
  const visit = (node: Node): void => {
    if (node.kind === SyntaxKind.Identifier) {
      const symbol = checker.resolveName(node as Identifier);
      const tokenType = symbol ? tokenTypeOf(symbol.flags) : undefined;
      if (symbol && tokenType !== undefined) {
        const start = skipTrivia(sourceFile.text, node.pos);
        const length = node.end - start;
        if (length > 0) {
          const isDeclarationName =
            !!node.parent &&
            node.parent.symbol === symbol &&
            (node.parent as { name?: Node }).name === node;
          entries.push({
            offset: start,
            length,
            tokenType,
            tokenModifiers: modifiersOf(symbol, isDeclarationName),
          });
        }
      }
    }
    if (node.kind === SyntaxKind.CallExpression) {
      const call = node as CallExpression;
      const method = checker.resolveCall(call);
      const indices = method && REGEX_SINKS[`${enclosingTypeName(method)}#${method.name?.text}`];
      if (indices) {
        for (const i of indices) {
          const arg = call.arguments[i];
          if (arg?.kind === SyntaxKind.StringLiteral) {
            const start = skipTrivia(sourceFile.text, arg.pos);
            const length = arg.end - start;
            if (length > 0) {
              entries.push({
                offset: start,
                length,
                tokenType: TYPE_INDEX["regexp"]!,
                tokenModifiers: 0,
              });
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
  visit(sourceFile);
  entries.sort((a, b) => a.offset - b.offset);
  return entries;
}
