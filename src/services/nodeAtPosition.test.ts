import { test } from "node:test";

import { expect } from "expect";

import { getIdentifierAtPosition, getNodeAtPosition } from "./nodeAtPosition.ts";
import { parseSourceFile } from "../compiler/parser.ts";
import { type Identifier, SyntaxKind } from "../compiler/types.ts";

const source = "class C {\n  int field;\n  void m() { int local = 1; }\n}\n";
const sf = parseSourceFile("T.java", source);

test("getNodeAtPosition finds the deepest node at an offset", () => {
  const node = getNodeAtPosition(sf, source.indexOf("field"));
  expect(node.kind).toBe(SyntaxKind.Identifier);
  expect((node as Identifier).text).toBe("field");
});

test("getIdentifierAtPosition resolves a name under the cursor", () => {
  const localOffset = source.indexOf("local");
  expect((getIdentifierAtPosition(sf, localOffset) as Identifier).text).toBe("local");
});

test("getIdentifierAtPosition accepts the trailing edge of a name", () => {
  const end = source.indexOf("field") + "field".length;
  expect((getIdentifierAtPosition(sf, end) as Identifier).text).toBe("field");
});

test("getIdentifierAtPosition returns undefined off any name", () => {
  // offset on the '{' of the class body
  expect(getIdentifierAtPosition(sf, source.indexOf("{"))).toBeUndefined();
});
