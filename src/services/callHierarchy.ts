// Call hierarchy (incoming / outgoing calls), as pure functions over the program
// - the transport-free half, mirroring TypeScript's services/callHierarchy and
// cappu's other service modules. The server maps the returned LSP shapes to the
// wire; incoming/outgoing re-resolve the method from the item's selectionRange
// position the client hands back, so nothing opaque has to survive the trip.

import {
  type CallHierarchyIncomingCall,
  type CallHierarchyItem,
  type CallHierarchyOutgoingCall,
  type Range,
  SymbolKind,
} from "vscode-languageserver-types";

import { type Checker } from "../compiler/checker.ts";
import {
  type Character,
  computeLineStarts,
  getLineAndCharacterOfPosition,
  getPositionOfLineAndCharacter,
  type Line,
} from "../compiler/lineMap.ts";
import { forEachChild } from "../compiler/parser.ts";
import type { Program } from "../compiler/program.ts";
import { findReferences, getSourceFileOfNode } from "../compiler/resolver.ts";
import {
  type CallExpression,
  type ConstructorDeclaration,
  type Identifier,
  type MethodDeclaration,
  type Node,
  type PropertyAccessExpression,
  type SourceFile,
  type Symbol,
  SymbolFlags,
  SyntaxKind,
} from "../compiler/types.ts";
import { skipTrivia } from "../compiler/utilities.ts";
import { type Uri } from "../workspace.ts";
import { enclosingCall } from "./hover.ts";
import { getIdentifierAtPosition } from "./nodeAtPosition.ts";

const CALLABLE_FLAGS = SymbolFlags.Method | SymbolFlags.Constructor;

function rangeOf(text: string, lineStarts: readonly number[], pos: number, end: number): Range {
  return {
    start: getLineAndCharacterOfPosition(lineStarts, pos),
    end: getLineAndCharacterOfPosition(lineStarts, end),
  };
}

// The enclosing method or constructor declaration of a node, or undefined (a
// reference in a field initializer / static block has no enclosing callable).
function enclosingCallable(node: Node): MethodDeclaration | ConstructorDeclaration | undefined {
  for (let n: Node | undefined = node.parent; n; n = n.parent) {
    if (n.kind === SyntaxKind.MethodDeclaration) return n as MethodDeclaration;
    if (n.kind === SyntaxKind.ConstructorDeclaration) return n as ConstructorDeclaration;
  }
  return undefined;
}

// A CallHierarchyItem for a method/constructor declaration; selectionRange is the
// name, range spans the declaration.
function itemOfDeclaration(decl: MethodDeclaration | ConstructorDeclaration): CallHierarchyItem {
  const file = getSourceFileOfNode(decl);
  const lineStarts = computeLineStarts(file.text);
  const nameNode = (decl as { name?: { text?: string; pos: number; end: number } }).name;
  const name =
    decl.kind === SyntaxKind.ConstructorDeclaration
      ? "<init>"
      : (nameNode?.text ?? "<anonymous>");
  const selection = nameNode ?? decl;
  return {
    name,
    kind: decl.kind === SyntaxKind.ConstructorDeclaration ? SymbolKind.Constructor : SymbolKind.Method,
    uri: file.fileName,
    range: rangeOf(file.text, lineStarts, skipTrivia(file.text, decl.pos), decl.end),
    selectionRange: rangeOf(file.text, lineStarts, skipTrivia(file.text, selection.pos), selection.end),
  };
}

// The callable symbol an item points at, by re-resolving the identifier at its
// selectionRange start.
function callableSymbolOfItem(program: Program, checker: Checker, item: CallHierarchyItem): Symbol | undefined {
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
  return symbol && symbol.flags & CALLABLE_FLAGS ? symbol : undefined;
}

function callableDeclarations(symbol: Symbol): (MethodDeclaration | ConstructorDeclaration)[] {
  return (symbol.declarations ?? []).filter(
    d => d.kind === SyntaxKind.MethodDeclaration || d.kind === SyntaxKind.ConstructorDeclaration,
  ) as (MethodDeclaration | ConstructorDeclaration)[];
}

// The method/constructor at a position, as a single-element item list.
export function prepareCallHierarchy(
  checker: Checker,
  sourceFile: SourceFile,
  offset: number,
): CallHierarchyItem[] | null {
  const id = getIdentifierAtPosition(sourceFile, offset) as Identifier | undefined;
  if (!id) return null;
  const symbol = checker.resolveName(id);
  if (!symbol || !(symbol.flags & CALLABLE_FLAGS)) return null;
  const items = callableDeclarations(symbol).map(itemOfDeclaration);
  return items.length > 0 ? items : null;
}

// Who calls the item's method: every call site, grouped by the enclosing
// method/constructor it sits in.
export function callHierarchyIncoming(
  program: Program,
  checker: Checker,
  item: CallHierarchyItem,
): CallHierarchyIncomingCall[] | null {
  const symbol = callableSymbolOfItem(program, checker, item);
  if (!symbol) return null;
  // group caller declaration -> the call-site name ranges within it
  const byCaller = new Map<Node, { from: CallHierarchyItem; ranges: Range[] }>();
  for (const ref of findReferences(symbol, program, checker.resolveName)) {
    if (!enclosingCall(ref as Identifier)) continue; // only call sites, not other uses
    const caller = enclosingCallable(ref);
    if (!caller) continue;
    const file = getSourceFileOfNode(ref);
    const lineStarts = computeLineStarts(file.text);
    const range = rangeOf(file.text, lineStarts, skipTrivia(file.text, ref.pos), ref.end);
    const entry = byCaller.get(caller);
    if (entry) entry.ranges.push(range);
    else byCaller.set(caller, { from: itemOfDeclaration(caller), ranges: [range] });
  }
  const calls = [...byCaller.values()].map(e => ({ from: e.from, fromRanges: e.ranges }));
  return calls.length > 0 ? calls : null;
}

// What the item's method calls: every call in its body, grouped by callee.
export function callHierarchyOutgoing(
  program: Program,
  checker: Checker,
  item: CallHierarchyItem,
): CallHierarchyOutgoingCall[] | null {
  const symbol = callableSymbolOfItem(program, checker, item);
  if (!symbol) return null;
  const byCallee = new Map<Node, { to: CallHierarchyItem; ranges: Range[] }>();
  for (const decl of callableDeclarations(symbol)) {
    const body = (decl as { body?: Node }).body;
    if (!body) continue;
    const file = getSourceFileOfNode(decl);
    const lineStarts = computeLineStarts(file.text);
    const visit = (node: Node): void => {
      if (node.kind === SyntaxKind.CallExpression) {
        const target = checker.resolveCall(node as CallExpression);
        if (target) {
          // the callee name range at this call site
          const callee = (node as CallExpression).expression;
          const nameNode =
            callee.kind === SyntaxKind.PropertyAccessExpression
              ? (callee as PropertyAccessExpression).name
              : callee.kind === SyntaxKind.Identifier
                ? (callee as Identifier)
                : undefined;
          if (nameNode) {
            const range = rangeOf(file.text, lineStarts, skipTrivia(file.text, nameNode.pos), nameNode.end);
            const entry = byCallee.get(target);
            if (entry) entry.ranges.push(range);
            else byCallee.set(target, { to: itemOfDeclaration(target), ranges: [range] });
          }
        }
      }
      forEachChild(node, child => {
        visit(child);
        return undefined;
      });
    };
    visit(body);
  }
  const calls = [...byCallee.values()].map(e => ({ to: e.to, fromRanges: e.ranges }));
  return calls.length > 0 ? calls : null;
}
