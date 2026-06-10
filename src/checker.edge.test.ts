import { test } from "node:test";

import { expect } from "expect";

import { createChecker } from "./checker.ts";
import { typeToString } from "./checkerTypes.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { getIdentifierAtPosition } from "./nodeAtPosition.ts";
import { createProgram } from "./program.ts";
import { type Identifier, type VariableDeclarator } from "./types.ts";

function setup(text: string) {
  const program = createProgram();
  loadJdkStub(program);
  program.setOpenDocument("file:///T.java", text, 1);
  return { checker: createChecker(program), sf: program.getSourceFile("file:///T.java")! };
}

function sym(ctx: ReturnType<typeof setup>, needle: string, occ = 1) {
  let offset = -1;
  for (let i = 0; i < occ; i++) offset = ctx.sf.text.indexOf(needle, offset + 1);
  return ctx.checker.resolveName(getIdentifierAtPosition(ctx.sf, offset) as Identifier)!;
}

function fieldType(text: string, name: string): string {
  const ctx = setup(text);
  return typeToString(ctx.checker.getTypeOfSymbol(sym(ctx, name)));
}

// type of the initializer of `var zz = <expr>;`
function initType(text: string, expr: string): string {
  const ctx = setup(text.replace("$EXPR", expr));
  const declarator = sym(ctx, "zz").valueDeclaration as VariableDeclarator;
  return typeToString(ctx.checker.getTypeOfExpression(declarator.initializer!));
}

function assignableFields(text: string, a: string, b: string): boolean {
  const ctx = setup(text);
  return ctx.checker.isAssignableTo(
    ctx.checker.getTypeOfSymbol(sym(ctx, a)),
    ctx.checker.getTypeOfSymbol(sym(ctx, b)),
  );
}

const METHOD = (body: string) =>
  `class C { C self; String[] names; void m(boolean flag, Object obj) { ${body} } }`;

test("boxing for long, double, boolean and char", () => {
  const code =
    "class C { long aa; Long bb; double cc; Double dd; boolean ee; Boolean ff; char gg; Character hh; }";
  expect(assignableFields(code, "aa", "bb")).toBe(true); // long -> Long
  expect(assignableFields(code, "dd", "cc")).toBe(true); // Double -> double (unbox)
  expect(assignableFields(code, "ee", "ff")).toBe(true); // boolean -> Boolean
  expect(assignableFields(code, "hh", "gg")).toBe(true); // Character -> char
});

test("widening chains and narrowing", () => {
  const code = "class C { byte vb; short vs; int vi; long vl; float vf; double vd; }";
  expect(assignableFields(code, "vb", "vd")).toBe(true); // byte -> double
  expect(assignableFields(code, "vs", "vl")).toBe(true); // short -> long
  expect(assignableFields(code, "vi", "vf")).toBe(true); // int -> float
  expect(assignableFields(code, "vd", "vi")).toBe(false); // double -> int
  expect(assignableFields(code, "vl", "vb")).toBe(false); // long -> byte
});

test("array covariance and primitive-array invariance", () => {
  const code = "class C { String[] arrS; Object[] arrO; int[] arrI; long[] arrL; }";
  expect(assignableFields(code, "arrS", "arrO")).toBe(true); // String[] -> Object[]
  expect(assignableFields(code, "arrO", "arrS")).toBe(false);
  expect(assignableFields(code, "arrI", "arrL")).toBe(false); // int[] -> long[]
});

test("invariant generics, covariant wildcard", () => {
  const code =
    "class C { java.util.List<String> listS; java.util.List<Object> listO; java.util.List<? extends Object> listW; }";
  expect(assignableFields(code, "listS", "listO")).toBe(false);
  expect(assignableFields(code, "listS", "listW")).toBe(true);
});

test("subtype across generic interfaces (ArrayList -> Collection)", () => {
  const code = "class C { java.util.ArrayList<String> aList; java.util.Collection<String> coll; }";
  expect(assignableFields(code, "aList", "coll")).toBe(true);
});

test("generic field type renders with arguments", () => {
  expect(
    fieldType("class C { java.util.Map<String, java.util.List<Integer>> mapField; }", "mapField"),
  ).toBe("Map<String, List<Integer>>");
});

test("inherited generic member call return type (graceful type variable)", () => {
  const t = initType(METHOD("var zz = new java.util.ArrayList<String>().iterator();"), "");
  expect(t.startsWith("Iterator")).toBe(true);
});

test("generic method return is inferred from the argument (T = Integer)", () => {
  expect(
    initType("class C { <T> T id(T arg) { return arg; } void m() { var zz = id(1); } }", ""),
  ).toBe("Integer");
});

test("member access chain types through fields", () => {
  expect(initType(METHOD("var zz = self.self.self;"), "")).toBe("C");
});

test("element access yields the array element type", () => {
  expect(initType(METHOD("var zz = names[0];"), "")).toBe("String");
});

test("this and string concatenation", () => {
  expect(initType(METHOD("var zz = this;"), "")).toBe("C");
  expect(initType(METHOD('var zz = 1 + "x" + 2;'), "")).toBe("String");
});

test("conditional expression type and instanceof", () => {
  expect(initType(METHOD('var zz = flag ? "a" : "b";'), "")).toBe("String");
  expect(initType(METHOD("var zz = obj instanceof String;"), "")).toBe("boolean");
});

test("enum constant has the enum type", () => {
  expect(fieldType("enum Color { RED, GREEN }", "RED")).toBe("Color");
});

test("unresolved member access stays <error> (graceful)", () => {
  expect(initType(METHOD("var zz = obj.whatever;"), "")).toBe("<error>");
});

test("cast expression takes the cast type", () => {
  expect(initType(METHOD("var zz = (String) obj;"), "")).toBe("String");
});
