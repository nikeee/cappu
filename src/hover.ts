// Hover / quick-info rendering for a resolved symbol. Shared by the LSP server
// and the fourslash hover baselines so both render identically.

import type { Checker } from "./checker.ts";
import { type Symbol, SymbolFlags } from "./types.ts";

export function symbolKindWord(flags: SymbolFlags): string {
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

/**
 * One-line hover label, in the style of the C#/Roslyn language service:
 *   methods  -> the full signature            e.g.  int add(int a, int b)
 *   types    -> keyword + name                e.g.  class Foo
 *   values   -> (kind) type name              e.g.  (field) int count
 *   type var -> (type parameter) name         e.g.  (type parameter) T
 */
export function getHoverText(checker: Checker, symbol: Symbol): string {
  if (symbol.flags & (SymbolFlags.Method | SymbolFlags.Constructor)) {
    const signature = checker.signatureOfSymbol(symbol);
    if (signature) return signature;
  }
  const word = symbolKindWord(symbol.flags);
  if (symbol.flags & TYPE_FLAGS) {
    return `${word} ${symbol.escapedName}`;
  }
  if (symbol.flags & SymbolFlags.TypeParameter) {
    return `(${word}) ${symbol.escapedName}`;
  }
  const type = checker.typeStringOfSymbol(symbol);
  // Omit an unresolvable type (e.g. a lambda parameter whose target is unknown)
  // rather than printing "<error>".
  return type === "<error>"
    ? `(${word}) ${symbol.escapedName}`
    : `(${word}) ${type} ${symbol.escapedName}`;
}
