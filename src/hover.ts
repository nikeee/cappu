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
  if (flags & SymbolFlags.LocalVariable) return "variable";
  return "symbol";
}

/** One-line hover label, e.g. "variable x: String" or "class Foo". */
export function getHoverText(checker: Checker, symbol: Symbol): string {
  const word = symbolKindWord(symbol.flags);
  if (symbol.flags & SymbolFlags.Type) {
    return `${word} ${symbol.escapedName}`;
  }
  return `${word} ${symbol.escapedName}: ${checker.typeStringOfSymbol(symbol)}`;
}
