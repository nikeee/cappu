import { test } from "node:test";
import { expect } from "expect";

import { getIdentifierAtPosition } from "./nodeAtPosition.ts";
import { createProgram } from "./program.ts";
import { resolveIdentifier } from "./resolver.ts";
import { type Identifier, type Symbol, SymbolFlags } from "./types.ts";

// Resolve the name at the nth occurrence of `needle` in `text`.
function resolveAt(text: string, needle: string, occurrence = 1): Symbol | undefined {
  const program = createProgram();
  program.setOpenDocument("file:///T.java", text, 1);
  const sf = program.getSourceFile("file:///T.java")!;
  let offset = -1;
  for (let i = 0; i < occurrence; i++) offset = text.indexOf(needle, offset + 1);
  const id = getIdentifierAtPosition(sf, offset);
  return id ? resolveIdentifier(id as Identifier, program) : undefined;
}

test("local variable use resolves to its declaration", () => {
  const sym = resolveAt("class C { void m() { int x = 1; return x; } }", "x", 2);
  expect(sym?.flags).toBe(SymbolFlags.LocalVariable);
  expect(sym?.escapedName).toBe("x");
});

test("parameter use resolves to the parameter", () => {
  const sym = resolveAt("class C { int m(int a) { return a; } }", "a", 2);
  expect(sym?.flags).toBe(SymbolFlags.Parameter);
});

test("field use resolves to the field", () => {
  const sym = resolveAt("class C { int f; void m() { f = 1; } }", "f", 2);
  expect(sym?.flags).toBe(SymbolFlags.Field);
});

test("a local shadows a field of the same name", () => {
  const sym = resolveAt("class C { int x; void m() { int x = 1; return x; } }", "x", 3);
  expect(sym?.flags).toBe(SymbolFlags.LocalVariable);
});

test("type reference resolves to a file-local type (declared later)", () => {
  const sym = resolveAt("class C extends Base {}\nclass Base {}", "Base", 1);
  expect(sym?.flags).toBe(SymbolFlags.Class);
  expect(sym?.escapedName).toBe("Base");
});

test("type parameter use resolves to the type parameter", () => {
  const sym = resolveAt("class C<T> { T get() { return null; } }", "T", 2);
  expect(sym?.flags).toBe(SymbolFlags.TypeParameter);
});

test("a method call name resolves to the method", () => {
  const sym = resolveAt(
    "class C { void m() { helper(); } int helper() { return 0; } }",
    "helper",
    1,
  );
  expect(sym?.flags).toBe(SymbolFlags.Method);
});

test("clicking a declaration name resolves to itself", () => {
  const sym = resolveAt("class C { int field; }", "field", 1);
  expect(sym?.flags).toBe(SymbolFlags.Field);
  expect(sym?.escapedName).toBe("field");
});

test("an unresolved name returns undefined", () => {
  expect(resolveAt("class C { void m() { unknownThing(); } }", "unknownThing", 1)).toBeUndefined();
});
