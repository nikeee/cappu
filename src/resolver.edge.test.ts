import { test } from "node:test";
import { expect } from "expect";

import { createChecker } from "./checker.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { getIdentifierAtPosition } from "./nodeAtPosition.ts";
import { createProgram } from "./program.ts";
import { resolveIdentifier } from "./resolver.ts";
import { type Identifier, type Symbol, SymbolFlags } from "./types.ts";

function ctxOf(text: string) {
  const program = createProgram();
  loadJdkStub(program);
  program.setOpenDocument("file:///T.java", text, 1);
  return { program, checker: createChecker(program), sf: program.getSourceFile("file:///T.java")! };
}

function resolveAt(text: string, needle: string, occurrence: number): Symbol | undefined {
  const { program, sf } = ctxOf(text);
  let offset = -1;
  for (let i = 0; i < occurrence; i++) offset = text.indexOf(needle, offset + 1);
  const id = getIdentifierAtPosition(sf, offset) as Identifier | undefined;
  return id ? resolveIdentifier(id, program) : undefined;
}

function resolveNameAt(text: string, needle: string, occurrence: number): Symbol | undefined {
  const ctx = ctxOf(text);
  let offset = -1;
  for (let i = 0; i < occurrence; i++) offset = text.indexOf(needle, offset + 1);
  const id = getIdentifierAtPosition(ctx.sf, offset) as Identifier | undefined;
  return id ? ctx.checker.resolveName(id) : undefined;
}

test("parameter shadows a field of the same name", () => {
  expect(resolveAt("class C { int x; int m(int x) { return x; } }", "x", 3)?.flags).toBe(
    SymbolFlags.Parameter,
  );
});

test("a local variable shadows a field", () => {
  expect(resolveAt("class C { int f; int m() { int f = 1; return f; } }", "f", 3)?.flags).toBe(
    SymbolFlags.LocalVariable,
  );
});

test("forward reference to a field declared later", () => {
  expect(resolveAt("class C { int m() { return later; } int later; }", "later", 1)?.flags).toBe(
    SymbolFlags.Field,
  );
});

test("field inherited through two levels of extends", () => {
  const sym = resolveAt(
    "class A extends B { int m() { return g; } }\nclass B extends Base {}\nclass Base { int g; }",
    "g",
    1,
  );
  expect(sym?.flags).toBe(SymbolFlags.Field);
});

test("default interface method is inherited by an implementor", () => {
  const sym = resolveNameAt(
    "interface I { default int d() { return 1; } }\nclass C implements I { void m() { d(); } }",
    "d",
    2,
  );
  expect(sym?.flags).toBe(SymbolFlags.Method);
});

test("enhanced-for variable resolves", () => {
  const sym = resolveAt(
    "class C { void m(java.util.List<String> xs) { for (String item : xs) { use(item); } } }",
    "item",
    2,
  );
  expect(sym?.flags).toBe(SymbolFlags.Parameter);
});

test("catch parameter resolves", () => {
  const sym = resolveAt(
    "class C { void m() { try {} catch (Exception ex) { use(ex); } } }",
    "ex",
    2,
  );
  expect(sym?.flags).toBe(SymbolFlags.Parameter);
});

test("typed lambda parameter resolves", () => {
  const sym = resolveAt("class C { Runnable r = (int arg) -> { use(arg); }; }", "arg", 2);
  expect(sym?.flags).toBe(SymbolFlags.Parameter);
});

test("enum constant resolves through member access", () => {
  const sym = resolveNameAt("enum E { A, B }\nclass C { E x = E.A; }", "A", 2);
  expect(sym?.flags).toBe(SymbolFlags.EnumConstant);
});

test("nested type resolves as a member type", () => {
  const sym = resolveAt("class O { Inner make() { return null; } class Inner {} }", "Inner", 1);
  expect(sym?.flags).toBe(SymbolFlags.Class);
});

test("overloaded method is one symbol with several declarations", () => {
  const sym = resolveAt(
    "class C { void m() { helper(); } void helper(){} void helper(int a){} }",
    "helper",
    1,
  );
  expect(sym?.declarations).toHaveLength(2);
});

test("method-scoped type parameter resolves", () => {
  const sym = resolveAt("class C { <T> T id(T x) { T y = x; return y; } }", "T", 4);
  expect(sym?.flags).toBe(SymbolFlags.TypeParameter);
});
