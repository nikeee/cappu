import { test } from "node:test";

import { expect } from "expect";

import { createChecker } from "../compiler/checker.ts";
import { type ClassType, TypeKind } from "../compiler/checkerTypes.ts";
import { createProgram } from "../compiler/program.ts";
import { getDeclarationNameNode, getSourceFileOfNode } from "../compiler/resolver.ts";
import { type Identifier, type Node, SyntaxKind } from "../compiler/types.ts";
import { type Uri } from "../workspace.ts";
import { getNodeAtPosition } from "./nodeAtPosition.ts";

// go-to-definition on the `var` keyword navigates to the inferred type's
// declaration (server.onDefinition's var branch). This exercises the pieces it
// composes: the node under the cursor is a VarType, and the declared variable's
// inferred type resolves to the class it was constructed from.
test("the `var` keyword resolves to the inferred type's declaration", () => {
  const src = "class Foo {} class V { void m() { var f = new Foo(); } }";
  const program = createProgram();
  program.setOpenDocument("file:///V.java" as Uri, src, 1);
  const checker = createChecker(program);
  const sf = program.getSourceFile("file:///V.java" as Uri)!;

  const node = getNodeAtPosition(sf, src.indexOf("var")) as
    | (Node & { parent: { declarators: { name: Identifier }[] } })
    | undefined;
  expect(node?.kind).toBe(SyntaxKind.VarType);

  const nameId = node!.parent.declarators[0]!.name;
  const type = checker.getTypeOfExpression(nameId);
  expect(type.kind).toBe(TypeKind.Class);
  const decl = getDeclarationNameNode((type as ClassType).symbol)!;
  expect((decl as Identifier).text).toBe("Foo");
  expect(getSourceFileOfNode(decl).fileName).toBe("file:///V.java");
});
