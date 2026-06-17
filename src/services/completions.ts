// Completion provider. Designed to stay useful on incomplete / broken code:
// member completion (`expr.|`) lists the members of the receiver's type when it
// is known and falls back to nothing (never a guess) when it is not; identifier
// completion always offers the names visible in the current scope, which works
// regardless of parse errors because the binder still produced scopes.

import type { Checker } from "../compiler/checker.ts";
import { type ClassType, TypeKind } from "../compiler/checkerTypes.ts";
import { getNodeAtPosition } from "./nodeAtPosition.ts";
import type { Fqn, PackageName, Program } from "../compiler/program.ts";
import { getDirectSuperTypeSymbols, getSourceFileOfNode } from "../compiler/resolver.ts";
import {
  type Node,
  type SourceFile,
  type Symbol,
  SymbolFlags,
  type SymbolTable,
  SyntaxKind,
} from "../compiler/types.ts";
import { entityNameToString } from "../compiler/utilities.ts";

// Mirrors a subset of LSP CompletionItemKind (numeric values match the protocol).
export const enum CompletionItemKind {
  Method = 2,
  Field = 5,
  Variable = 6,
  Class = 7,
  Interface = 8,
  Enum = 10,
  EnumMember = 20,
  TypeParameter = 25,
}

export interface CompletionItem {
  readonly label: string;
  readonly kind: CompletionItemKind;
}

function completionKind(flags: SymbolFlags): CompletionItemKind {
  if (flags & (SymbolFlags.Class | SymbolFlags.Record | SymbolFlags.Annotation))
    return CompletionItemKind.Class;
  if (flags & SymbolFlags.Interface) return CompletionItemKind.Interface;
  if (flags & SymbolFlags.Enum) return CompletionItemKind.Enum;
  if (flags & (SymbolFlags.Method | SymbolFlags.Constructor)) return CompletionItemKind.Method;
  if (flags & SymbolFlags.Field) return CompletionItemKind.Field;
  if (flags & SymbolFlags.EnumConstant) return CompletionItemKind.EnumMember;
  if (flags & SymbolFlags.TypeParameter) return CompletionItemKind.TypeParameter;
  return CompletionItemKind.Variable;
}

function toItems(symbols: Map<string, Symbol>): CompletionItem[] {
  return [...symbols].map(([label, symbol]) => ({ label, kind: completionKind(symbol.flags) }));
}

function isExpressionKind(kind: SyntaxKind): boolean {
  return (
    kind === SyntaxKind.Identifier ||
    (kind >= SyntaxKind.FirstExpression && kind <= SyntaxKind.LastExpression)
  );
}

// --- member completion ----------------------------------------------------------------

function gatherTypeMembers(
  typeSymbol: Symbol,
  program: Program,
  into: Map<string, Symbol>,
  seen: Set<Symbol>,
  includeTypeParameters: boolean,
): void {
  if (seen.has(typeSymbol)) return;
  seen.add(typeSymbol);
  if (typeSymbol.members) {
    for (const [name, symbol] of typeSymbol.members) {
      // Type parameters live in the members table for name resolution, but they
      // are not accessible members for "expr." completion.
      if (!includeTypeParameters && symbol.flags & SymbolFlags.TypeParameter) continue;
      // Constructors are not members reachable via `expr.` (they are invoked with
      // `new`), so exclude them from member completion.
      if (symbol.flags & SymbolFlags.Constructor) continue;
      if (!into.has(name)) into.set(name, symbol);
    }
  }
  for (const superSymbol of getDirectSuperTypeSymbols(typeSymbol, program)) {
    gatherTypeMembers(superSymbol, program, into, seen, includeTypeParameters);
  }
}

// --- scope completion -----------------------------------------------------------------

function addAll(table: SymbolTable | undefined, into: Map<string, Symbol>): void {
  if (!table) return;
  for (const [name, symbol] of table) {
    if (!into.has(name)) into.set(name, symbol);
  }
}

function collectScopeSymbols(node: Node, program: Program): Map<string, Symbol> {
  const result = new Map<string, Symbol>();
  let current: Node | undefined = node;
  while (current) {
    if (current.symbol?.members && isTypeDeclaration(current.kind)) {
      gatherTypeMembers(current.symbol, program, result, new Set(), true);
    } else {
      addAll(current.locals, result);
    }
    current = current.parent;
  }
  // file-level: same package, java.lang, single-type imports
  const sourceFile = getSourceFileOfNode(node);
  const index = program.getGlobalIndex();
  const pkg = (
    sourceFile.packageDeclaration ? entityNameToString(sourceFile.packageDeclaration.name) : ""
  ) as PackageName;
  addAll(index.getPackageTypes(pkg), result);
  addAll(index.getPackageTypes("java.lang" as PackageName), result);
  for (const imp of sourceFile.imports) {
    if (imp.isStatic) continue;
    if (imp.isOnDemand) {
      addAll(index.getPackageTypes(entityNameToString(imp.name) as PackageName), result);
    } else {
      const fqn = entityNameToString(imp.name) as Fqn;
      const type = index.getType(fqn);
      if (type) result.set(fqn.slice(fqn.lastIndexOf(".") + 1), type);
    }
  }
  return result;
}

function isTypeDeclaration(kind: SyntaxKind): boolean {
  return (
    kind === SyntaxKind.ClassDeclaration ||
    kind === SyntaxKind.InterfaceDeclaration ||
    kind === SyntaxKind.EnumDeclaration ||
    kind === SyntaxKind.AnnotationTypeDeclaration ||
    kind === SyntaxKind.RecordDeclaration
  );
}

// --- entry point ----------------------------------------------------------------------

/** Completions at an offset. Member completion after '.', otherwise scope names. */
export function getCompletions(
  program: Program,
  checker: Checker,
  sourceFile: SourceFile,
  offset: number,
): CompletionItem[] {
  const text = sourceFile.text;

  // Member access? Look back past any partial member name already being typed
  // (`recv.par|tial`), then whitespace, to a '.'. Without skipping the partial
  // name, completion would stop working as soon as the first letter is typed.
  let i = offset - 1;
  while (i >= 0 && /[A-Za-z0-9_$]/.test(text[i]!)) i--;
  while (i >= 0 && /\s/.test(text[i]!)) i--;
  if (i >= 0 && text[i] === ".") {
    const dot = i;
    if (dot === 0) return [];
    let expr = getNodeAtPosition(sourceFile, dot - 1);
    while (expr.parent && expr.parent.end === dot && isExpressionKind(expr.parent.kind)) {
      expr = expr.parent;
    }
    const type = checker.getTypeOfExpression(expr);
    if (type.kind !== TypeKind.Class) return []; // unknown receiver -> no guesses
    const members = new Map<string, Symbol>();
    gatherTypeMembers((type as ClassType).symbol, program, members, new Set(), false);
    return toItems(members);
  }

  // identifier position -> names visible in scope
  const node = getNodeAtPosition(sourceFile, offset > 0 ? offset - 1 : 0);
  return toItems(collectScopeSymbols(node, program));
}
