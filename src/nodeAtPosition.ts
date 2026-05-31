// Locate the AST node at a character offset - the basis for position-driven LSP
// requests (definition, hover, references, completion). Walks down via
// forEachChild into the deepest node whose [pos, end) span contains the offset.

import { forEachChild } from "./parser.ts";
import { type Node, SyntaxKind } from "./types.ts";

function containsOffset(node: Node, offset: number): boolean {
  return offset >= node.pos && offset < node.end;
}

/** Deepest node whose span contains the offset (the SourceFile if none deeper). */
export function getNodeAtPosition(root: Node, offset: number): Node {
  let current = root;
  for (;;) {
    const child = forEachChild(current, c => (containsOffset(c, offset) ? c : undefined));
    if (!child) return current;
    current = child;
  }
}

/**
 * The Identifier at the offset, if the cursor is on a name. Returns the deepest
 * node first; if it is an Identifier (or its span starts exactly at one) it is
 * returned, else undefined. Trailing-edge offsets (cursor just past the name)
 * are accepted so "foo|" resolves.
 */
export function getIdentifierAtPosition(root: Node, offset: number): Node | undefined {
  let node = getNodeAtPosition(root, offset);
  if (node.kind === SyntaxKind.Identifier) return node;
  // Cursor at the trailing edge of an identifier (offset === end): retry one
  // position back so "name|" still resolves.
  if (offset > 0) {
    node = getNodeAtPosition(root, offset - 1);
    if (node.kind === SyntaxKind.Identifier) return node;
  }
  return undefined;
}
