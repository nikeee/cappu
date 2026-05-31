import { test } from "node:test";
import { expect } from "expect";

import { forEachChild, parseSourceFile } from "./parser.ts";
import { isValidIdentifier, skipTrivia } from "./utilities.ts";
import { type Identifier, type Node, SyntaxKind } from "./types.ts";

test("isValidIdentifier accepts names and rejects keywords/garbage", () => {
  expect(isValidIdentifier("foo")).toBe(true);
  expect(isValidIdentifier("_x$2")).toBe(true);
  expect(isValidIdentifier("$")).toBe(true);
  expect(isValidIdentifier("2foo")).toBe(false); // leading digit
  expect(isValidIdentifier("a b")).toBe(false); // space
  expect(isValidIdentifier("")).toBe(false);
  expect(isValidIdentifier("class")).toBe(false); // reserved keyword
  expect(isValidIdentifier("true")).toBe(false); // reserved literal
});

test("skipTrivia advances over whitespace and line breaks", () => {
  expect(skipTrivia("   x", 0)).toBe(3);
  expect(skipTrivia("\n\t x", 0)).toBe(3);
  expect(skipTrivia("x", 0)).toBe(0);
});

test("skipTrivia advances over line and block comments", () => {
  expect(skipTrivia("// note\nx", 0)).toBe(8);
  expect(skipTrivia("/* note */ x", 0)).toBe(11);
  expect(skipTrivia("  /*a*/ /*b*/  x", 0)).toBe(15);
});

// The real fix: an identifier node's pos includes leading trivia (the full
// start, as in the TS compiler). skipTrivia maps it to the token's real start,
// which is the range a goto/reference/rename highlight should use.
test("an identifier node's range is trimmed to the name by skipTrivia", () => {
  const text = "class C {\n    int field;\n    int m() { return field; }\n}";
  const sf = parseSourceFile("T.java", text);

  // find the `field` reference inside the return statement (the second occurrence)
  const ids: Identifier[] = [];
  const walk = (n: Node): void => {
    if (n.kind === SyntaxKind.Identifier && (n as Identifier).text === "field") {
      ids.push(n as Identifier);
    }
    forEachChild(n, c => {
      walk(c);
      return undefined;
    });
  };
  walk(sf);
  const useNode = ids[ids.length - 1]!;

  // node.pos sits on whitespace before the name; skipTrivia lands on the name.
  expect(text[useNode.pos]).toMatch(/\s/);
  const start = skipTrivia(text, useNode.pos);
  expect(text.slice(start, useNode.end)).toBe("field");
});
