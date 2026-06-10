import { test } from "node:test";

import { expect } from "expect";

import { createDiagnostic, Diagnostics, formatMessage } from "./diagnostics.ts";
import { SyntaxKind } from "./types.ts";
import {
  isKeyword,
  isModifierKeyword,
  isPrimitiveTypeKeyword,
  textToKeyword,
  tokenToString,
} from "./utilities.ts";

test("range markers are ordered", () => {
  expect(SyntaxKind.FirstLiteralToken <= SyntaxKind.LastLiteralToken).toBe(true);
  expect(SyntaxKind.FirstPunctuation <= SyntaxKind.LastPunctuation).toBe(true);
  expect(SyntaxKind.FirstAssignment <= SyntaxKind.LastAssignment).toBe(true);
  expect(SyntaxKind.FirstKeyword <= SyntaxKind.LastKeyword).toBe(true);
  expect(SyntaxKind.FirstReservedWord <= SyntaxKind.LastReservedWord).toBe(true);
  expect(SyntaxKind.FirstTypeNode <= SyntaxKind.LastTypeNode).toBe(true);
  expect(SyntaxKind.FirstStatement <= SyntaxKind.LastStatement).toBe(true);
  expect(SyntaxKind.FirstExpression <= SyntaxKind.LastExpression).toBe(true);
});

test("every keyword kind falls inside the keyword range", () => {
  for (const kind of textToKeyword.values()) {
    expect(kind >= SyntaxKind.FirstKeyword && kind <= SyntaxKind.LastKeyword).toBe(true);
    expect(isKeyword(kind)).toBe(true);
  }
});

test("keyword text round-trips through textToKeyword and tokenToString", () => {
  for (const [text, kind] of textToKeyword) {
    expect(textToKeyword.get(text)).toBe(kind);
    expect(tokenToString(kind)).toBe(text);
  }
});

test("Identifier is not a keyword", () => {
  expect(isKeyword(SyntaxKind.Identifier)).toBe(false);
});

test("tokenToString returns spellings for punctuation", () => {
  expect(tokenToString(SyntaxKind.OpenBraceToken)).toBe("{");
  expect(tokenToString(SyntaxKind.GreaterThanGreaterThanGreaterThanToken)).toBe(">>>");
  expect(tokenToString(SyntaxKind.ArrowToken)).toBe("->");
  expect(tokenToString(SyntaxKind.Identifier)).toBeUndefined();
});

test("modifier and primitive predicates", () => {
  expect(isModifierKeyword(SyntaxKind.PublicKeyword)).toBe(true);
  expect(isModifierKeyword(SyntaxKind.ClassKeyword)).toBe(false);
  expect(isPrimitiveTypeKeyword(SyntaxKind.IntKeyword)).toBe(true);
  expect(isPrimitiveTypeKeyword(SyntaxKind.VoidKeyword)).toBe(false);
});

test("diagnostics format placeholders and locations", () => {
  expect(formatMessage(Diagnostics._0_expected, [";"])).toBe("';' expected.");
  const d = createDiagnostic(5, 3, Diagnostics._0_expected, "}");
  expect(d.pos).toBe(5);
  expect(d.end).toBe(8);
  expect(d.messageText).toBe("'}' expected.");
  expect(d.code).toBe(Diagnostics._0_expected.code);
});
