// Semantic tokens: classify every resolved identifier so editors color fields,
// locals, parameters, type parameters etc. accurately. Offset-based and
// position-free; the LSP server encodes the entries into the wire format.

import type { Checker } from "./checker.ts";
import { forEachChild } from "./parser.ts";
import { getSourceFileOfNode } from "./resolver.ts";
import {
  type Identifier,
  type Node,
  type SourceFile,
  type Symbol,
  SymbolFlags,
  SyntaxKind,
} from "./types.ts";
import { skipTrivia } from "./utilities.ts";
import { isSyntheticUri } from "./workspace.ts";

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
] as const;

export const TOKEN_MODIFIERS = ["declaration", "static", "readonly", "defaultLibrary"] as const;

const TYPE_INDEX: Record<string, number> = Object.fromEntries(
  TOKEN_TYPES.map((t, i) => [t, i]),
);
const MOD_BIT: Record<string, number> = Object.fromEntries(
  TOKEN_MODIFIERS.map((m, i) => [m, 1 << i]),
);

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
  return bits;
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
            !!node.parent && node.parent.symbol === symbol && (node.parent as { name?: Node }).name === node;
          entries.push({
            offset: start,
            length,
            tokenType,
            tokenModifiers: modifiersOf(symbol, isDeclarationName),
          });
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
