// Builds an LSP DocumentSymbol tree (outline) from a parsed SourceFile. Kept
// separate from the server transport so it is unit-testable.

import { type DocumentSymbol, type Range, SymbolKind, SymbolTag } from "vscode-languageserver-types";

import { getLineAndCharacterOfPosition } from "../compiler/lineMap.ts";
import { readDeprecation } from "../compiler/deprecation.ts";
import {
  type ClassDeclaration,
  type EnumConstantDeclaration,
  type EnumDeclaration,
  type FieldDeclaration,
  type Identifier,
  type InterfaceDeclaration,
  type MethodDeclaration,
  type Node,
  type NodeArray,
  type RecordComponent,
  type RecordDeclaration,
  type SourceFile,
  SyntaxKind,
} from "../compiler/types.ts";

function range(lineStarts: readonly number[], pos: number, end: number): Range {
  return {
    start: getLineAndCharacterOfPosition(lineStarts, pos),
    end: getLineAndCharacterOfPosition(lineStarts, end),
  };
}

function symbol(
  name: string,
  kind: SymbolKind,
  node: Node,
  selection: Node,
  lineStarts: readonly number[],
  children?: DocumentSymbol[],
): DocumentSymbol {
  // `node` is the declaration for every caller except fields (where it is the
  // VariableDeclarator and @Deprecated sits on the enclosing FieldDeclaration -
  // that branch tags the symbol itself, since parent pointers need binding).
  const deprecated = readDeprecation(node) !== undefined;
  return {
    name: name || "<anonymous>",
    kind,
    range: range(lineStarts, node.pos, node.end),
    selectionRange: range(lineStarts, selection.pos, selection.end),
    children,
    ...(deprecated ? { tags: [SymbolTag.Deprecated] } : {}),
  };
}

function nameText(node: { name?: Identifier }): string {
  return node.name?.text ?? "";
}

function membersOf(members: NodeArray<Node>, lineStarts: readonly number[]): DocumentSymbol[] {
  const result: DocumentSymbol[] = [];
  for (const member of members) {
    result.push(...memberSymbols(member, lineStarts));
  }
  return result;
}

function memberSymbols(node: Node, lineStarts: readonly number[]): DocumentSymbol[] {
  switch (node.kind) {
    case SyntaxKind.ClassDeclaration:
    case SyntaxKind.InterfaceDeclaration:
    case SyntaxKind.EnumDeclaration:
    case SyntaxKind.AnnotationTypeDeclaration:
    case SyntaxKind.RecordDeclaration:
      return [typeSymbol(node, lineStarts)];

    case SyntaxKind.MethodDeclaration: {
      const m = node as MethodDeclaration;
      return [symbol(nameText(m), SymbolKind.Method, node, m.name, lineStarts)];
    }
    case SyntaxKind.ConstructorDeclaration:
    case SyntaxKind.CompactConstructorDeclaration: {
      const c = node as unknown as { name: Identifier };
      return [symbol(c.name.text, SymbolKind.Constructor, node, c.name, lineStarts)];
    }
    case SyntaxKind.FieldDeclaration: {
      const f = node as FieldDeclaration;
      // @Deprecated sits on the FieldDeclaration, not the per-name declarators.
      const deprecated = readDeprecation(f) !== undefined;
      return f.declarators.map(d => {
        const s = symbol(d.name.text, SymbolKind.Field, d, d.name, lineStarts);
        if (deprecated) s.tags = [SymbolTag.Deprecated];
        return s;
      });
    }
    case SyntaxKind.EnumConstantDeclaration: {
      const e = node as EnumConstantDeclaration;
      return [symbol(e.name.text, SymbolKind.EnumMember, node, e.name, lineStarts)];
    }
    case SyntaxKind.RecordComponent: {
      const r = node as RecordComponent;
      return [symbol(r.name.text, SymbolKind.Field, node, r.name, lineStarts)];
    }
    default:
      return [];
  }
}

function typeSymbol(node: Node, lineStarts: readonly number[]): DocumentSymbol {
  const children: DocumentSymbol[] = [];
  let kind: SymbolKind = SymbolKind.Class;
  let name: Identifier;

  switch (node.kind) {
    case SyntaxKind.InterfaceDeclaration:
    case SyntaxKind.AnnotationTypeDeclaration:
      kind = SymbolKind.Interface;
      name = (node as InterfaceDeclaration).name;
      children.push(...membersOf((node as InterfaceDeclaration).members, lineStarts));
      break;
    case SyntaxKind.EnumDeclaration: {
      kind = SymbolKind.Enum;
      const e = node as EnumDeclaration;
      name = e.name;
      children.push(...membersOf(e.enumConstants, lineStarts));
      children.push(...membersOf(e.members, lineStarts));
      break;
    }
    case SyntaxKind.RecordDeclaration: {
      const r = node as RecordDeclaration;
      name = r.name;
      children.push(...membersOf(r.recordComponents, lineStarts));
      children.push(...membersOf(r.members, lineStarts));
      break;
    }
    default: {
      const c = node as ClassDeclaration;
      name = c.name;
      children.push(...membersOf(c.members, lineStarts));
      break;
    }
  }

  return symbol(name.text, kind, node, name, lineStarts, children);
}

/** Top-level outline for a source file. */
export function getDocumentSymbols(
  sourceFile: SourceFile,
  lineStarts: readonly number[],
): DocumentSymbol[] {
  const result: DocumentSymbol[] = [];
  for (const statement of sourceFile.statements) {
    switch (statement.kind) {
      case SyntaxKind.ClassDeclaration:
      case SyntaxKind.InterfaceDeclaration:
      case SyntaxKind.EnumDeclaration:
      case SyntaxKind.AnnotationTypeDeclaration:
      case SyntaxKind.RecordDeclaration:
        result.push(typeSymbol(statement, lineStarts));
        break;
      default:
        break;
    }
  }
  return result;
}
