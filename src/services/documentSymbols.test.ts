import { test } from "node:test";

import { expect } from "expect";
import { SymbolKind } from "vscode-languageserver-types";

import { getDocumentSymbols } from "./documentSymbols.ts";
import { computeLineStarts } from "../compiler/lineMap.ts";
import { parseSourceFile } from "../compiler/parser.ts";

function outline(text: string) {
  const sf = parseSourceFile("Test.java", text);
  return getDocumentSymbols(sf, computeLineStarts(text));
}

test("class with members produces a nested outline", () => {
  const symbols = outline("class C {\n  int x;\n  C() {}\n  void m(int a) {}\n}");
  expect(symbols).toHaveLength(1);
  const cls = symbols[0]!;
  expect(cls.name).toBe("C");
  expect(cls.kind).toBe(SymbolKind.Class);
  expect(cls.children!.map(c => [c.name, c.kind])).toEqual([
    ["x", SymbolKind.Field],
    ["C", SymbolKind.Constructor],
    ["m", SymbolKind.Method],
  ]);
});

test("multiple declarators each become a field symbol", () => {
  const [cls] = outline("class C { int a, b, c; }");
  expect(cls!.children!.map(c => c.name)).toEqual(["a", "b", "c"]);
});

test("enum constants and record components are listed", () => {
  const [e] = outline("enum E { A, B; int code; }");
  expect(e!.kind).toBe(SymbolKind.Enum);
  expect(e!.children!.map(c => [c.name, c.kind])).toEqual([
    ["A", SymbolKind.EnumMember],
    ["B", SymbolKind.EnumMember],
    ["code", SymbolKind.Field],
  ]);

  const [r] = outline("record Point(int x, int y) {}");
  expect(r!.children!.map(c => c.name)).toEqual(["x", "y"]);
});

test("nested types nest in the outline", () => {
  const [outer] = outline("class Outer { interface Inner { void f(); } }");
  const inner = outer!.children![0]!;
  expect([inner.name, inner.kind]).toEqual(["Inner", SymbolKind.Interface]);
  expect(inner.children!.map(c => c.name)).toEqual(["f"]);
});

test("selectionRange (name) is contained in range (whole declaration)", () => {
  const [cls] = outline("class C {\n  void method() {}\n}");
  const m = cls!.children![0]!;
  const before = (a: { line: number; character: number }, b: { line: number; character: number }) =>
    a.line < b.line || (a.line === b.line && a.character <= b.character);
  expect(before(m.range.start, m.selectionRange.start)).toBe(true);
  expect(before(m.selectionRange.end, m.range.end)).toBe(true);
});
