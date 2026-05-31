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

// Type of the initializer of the `var zz = ...;` in the given class body.
function zzType(body: string): string {
  const ctx = setup(`import java.util.*;\nclass C<E> { E val; ${body} }`);
  const declarator = sym(ctx, "zz").valueDeclaration as VariableDeclarator;
  return typeToString(ctx.checker.getTypeOfExpression(declarator.initializer!));
}

// Declared type of the `zz` variable itself (exercises var inference).
function zzVarType(body: string): string {
  const ctx = setup(`import java.util.*;\nclass C { ${body} }`);
  return typeToString(ctx.checker.getTypeOfSymbol(sym(ctx, "zz")));
}

// --- member-access substitution ----------------------------------------------------

test("List<String>.get(int) substitutes E to String", () => {
  expect(zzType("void m(List<String> xs) { var zz = xs.get(0); }")).toBe("String");
});

test("Map<String, Integer>.get substitutes V to Integer", () => {
  expect(zzType("void m(Map<String, Integer> mp) { var zz = mp.get(null); }")).toBe("Integer");
});

test("ArrayList<String>.get (member on the class itself) substitutes E", () => {
  expect(zzType("void m(ArrayList<String> xs) { var zz = xs.get(0); }")).toBe("String");
});

test("ArrayList<String>.iterator() substitutes E to Iterator<String>", () => {
  expect(zzType("void m(ArrayList<String> xs) { var zz = xs.iterator(); }")).toBe(
    "Iterator<String>",
  );
});

test("List<String>.iterator() threads E through Collection and Iterable", () => {
  expect(zzType("void m(List<String> xs) { var zz = xs.iterator(); }")).toBe("Iterator<String>");
});

test("chained: List<String>.iterator().next() yields String", () => {
  expect(zzType("void m(List<String> xs) { var zz = xs.iterator().next(); }")).toBe("String");
});

test("own type parameter field substitutes through the receiver", () => {
  expect(zzType("void m(C<String> c) { var zz = c.val; }")).toBe("String");
});

test("nested generic argument substitutes (List<List<String>>.get -> List<String>)", () => {
  expect(zzType("void m(List<List<String>> xs) { var zz = xs.get(0); }")).toBe("List<String>");
});

// --- var inference -----------------------------------------------------------------

test("var infers a String initializer", () => {
  expect(zzVarType('void m() { var zz = "hi"; }')).toBe("String");
});

test("var infers an int initializer", () => {
  expect(zzVarType("void m() { var zz = 1 + 2; }")).toBe("int");
});

test("var infers from a generic member call", () => {
  expect(zzVarType("void m(java.util.List<String> xs) { var zz = xs.get(0); }")).toBe("String");
});

test("enhanced-for var infers the element type of a List<String>", () => {
  const ctx = setup(
    "import java.util.*;\nclass C { void m(List<String> xs) { for (var item : xs) { use(item); } } }",
  );
  expect(typeToString(ctx.checker.getTypeOfSymbol(sym(ctx, "item", 1)))).toBe("String");
});

test("enhanced-for var infers the element type of an array", () => {
  const ctx = setup("class C { void m(String[] arr) { for (var item : arr) { use(item); } } }");
  expect(typeToString(ctx.checker.getTypeOfSymbol(sym(ctx, "item", 1)))).toBe("String");
});

// --- generic method inference ------------------------------------------------------

test("generic method infers T from a reference argument", () => {
  expect(zzVarType('<T> T id(T arg) {} void m() { var zz = id("s"); }')).toBe("String");
});

test("generic method infers T from two arguments of the same type", () => {
  expect(zzVarType('<T> T pick(T a, T b) {} void m() { var zz = pick("a", "b"); }')).toBe("String");
});

test("generic method infers T from a List<T> argument's element", () => {
  expect(
    zzVarType(
      "<T> T first(java.util.List<T> xs) {} void m(java.util.List<String> ss) { var zz = first(ss); }",
    ),
  ).toBe("String");
});

test("generic method with no inference info degrades to the type variable", () => {
  // T appears only in the return, not in any parameter: stays unbound (T).
  expect(zzVarType("<T> T make() {} void m() { var zz = make(); }")).toBe("T");
});

test("unrelated argument leaves an unconstrained variable unbound", () => {
  expect(zzVarType('<T> int len(T arg) {} void m() { var zz = len("s"); }')).toBe("int");
});
