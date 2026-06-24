// Type hierarchy (supertypes / subtypes), as pure functions over the program -
// the transport-free half, mirroring how TypeScript keeps its callHierarchy /
// typeHierarchy logic in a service module separate from the protocol layer. The
// server maps the returned LSP TypeHierarchyItems to the wire and re-enters
// `supertypes`/`subtypes` with the item the client hands back; each call
// re-resolves the type symbol from the item's selectionRange position, so no
// symbol identity has to survive the round-trip.

import { type Range, SymbolKind, type TypeHierarchyItem } from "vscode-languageserver-types";

import {
  type Character,
  computeLineStarts,
  getLineAndCharacterOfPosition,
  getPositionOfLineAndCharacter,
  type Line,
} from "../compiler/lineMap.ts";
import type { Program } from "../compiler/program.ts";
import {
  getDeclarationNameNode,
  getDirectSuperTypeSymbols,
  getSourceFileOfNode,
} from "../compiler/resolver.ts";
import { type Checker } from "../compiler/checker.ts";
import {
  type Identifier,
  type SourceFile,
  type Symbol,
  SymbolFlags,
} from "../compiler/types.ts";
import { skipTrivia } from "../compiler/utilities.ts";
import { type Uri } from "../workspace.ts";
import { getIdentifierAtPosition } from "./nodeAtPosition.ts";
import { getSubtypeIndex } from "./subtypes.ts";

const TYPE_FLAGS =
  SymbolFlags.Class |
  SymbolFlags.Interface |
  SymbolFlags.Enum |
  SymbolFlags.Record |
  SymbolFlags.Annotation;

function symbolKindOf(flags: SymbolFlags): SymbolKind {
  if (flags & (SymbolFlags.Interface | SymbolFlags.Annotation)) return SymbolKind.Interface;
  if (flags & SymbolFlags.Enum) return SymbolKind.Enum;
  return SymbolKind.Class; // class and record
}

function rangeOf(text: string, lineStarts: readonly number[], pos: number, end: number): Range {
  return {
    start: getLineAndCharacterOfPosition(lineStarts, pos),
    end: getLineAndCharacterOfPosition(lineStarts, end),
  };
}

// Build a TypeHierarchyItem for a type symbol, or undefined if it has no source
// declaration (e.g. a JDK-stub type). selectionRange is the type's name; range
// spans the whole declaration.
function itemOf(symbol: Symbol): TypeHierarchyItem | undefined {
  const nameNode = getDeclarationNameNode(symbol);
  const declaration = symbol.valueDeclaration ?? symbol.declarations?.[0];
  if (!nameNode || !declaration) return undefined;
  const file = getSourceFileOfNode(declaration);
  const lineStarts = computeLineStarts(file.text);
  return {
    name: (nameNode as { text?: string }).text ?? "<anonymous>",
    kind: symbolKindOf(symbol.flags),
    uri: file.fileName,
    range: rangeOf(file.text, lineStarts, skipTrivia(file.text, declaration.pos), declaration.end),
    selectionRange: rangeOf(file.text, lineStarts, skipTrivia(file.text, nameNode.pos), nameNode.end),
  };
}

// Resolve the type symbol an item points at, by re-resolving the identifier at
// its selectionRange start (the type's name).
function typeSymbolOfItem(program: Program, checker: Checker, item: TypeHierarchyItem): Symbol | undefined {
  const sourceFile = program.getSourceFile(item.uri as Uri);
  if (!sourceFile) return undefined;
  const lineStarts = computeLineStarts(sourceFile.text);
  const offset = getPositionOfLineAndCharacter(
    lineStarts,
    item.selectionRange.start.line as Line,
    item.selectionRange.start.character as Character,
  );
  const id = getIdentifierAtPosition(sourceFile, offset) as Identifier | undefined;
  const symbol = id && checker.resolveName(id);
  return symbol && symbol.flags & TYPE_FLAGS ? symbol : undefined;
}

// The type at a position, as a single-element item list (or null for "no type").
export function prepareTypeHierarchy(
  checker: Checker,
  sourceFile: SourceFile,
  offset: number,
): TypeHierarchyItem[] | null {
  const id = getIdentifierAtPosition(sourceFile, offset) as Identifier | undefined;
  if (!id) return null;
  const symbol = checker.resolveName(id);
  if (!symbol || !(symbol.flags & TYPE_FLAGS)) return null;
  const item = itemOf(symbol);
  return item ? [item] : null;
}

// The direct supertypes (extends + implements) of an item's type.
export function typeHierarchySupertypes(
  program: Program,
  checker: Checker,
  item: TypeHierarchyItem,
): TypeHierarchyItem[] | null {
  const symbol = typeSymbolOfItem(program, checker, item);
  if (!symbol) return null;
  const items = getDirectSuperTypeSymbols(symbol, program)
    .map(itemOf)
    .filter((i): i is TypeHierarchyItem => i !== undefined);
  return items.length > 0 ? items : null;
}

// The direct subtypes (declarations whose extends/implements names this type).
export function typeHierarchySubtypes(
  program: Program,
  checker: Checker,
  item: TypeHierarchyItem,
): TypeHierarchyItem[] | null {
  const symbol = typeSymbolOfItem(program, checker, item);
  if (!symbol) return null;
  const items = getSubtypeIndex(program)
    .directSubtypesOf(symbol)
    .map(itemOf)
    .filter((i): i is TypeHierarchyItem => i !== undefined);
  return items.length > 0 ? items : null;
}
