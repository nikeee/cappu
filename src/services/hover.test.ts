import { test } from "node:test";

import { expect } from "expect";

import { createChecker } from "../compiler/checker.ts";
import { getHoverText } from "./hover.ts";
import { loadJdkStub } from "../compiler/jdkStub.ts";
import { getIdentifierAtPosition } from "./nodeAtPosition.ts";
import { createProgram } from "../compiler/program.ts";
import { type CallExpression, type Identifier } from "../compiler/types.ts";
import { type Uri } from "../workspace.ts";

function setup(text: string) {
  const program = createProgram();
  loadJdkStub(program);
  program.setOpenDocument("file:///T.java" as Uri, text, 1);
  return { program, checker: createChecker(program), text };
}

function symbolAt(ctx: ReturnType<typeof setup>, needle: string, occ = 1) {
  let offset = -1;
  for (let i = 0; i < occ; i++) offset = ctx.text.indexOf(needle, offset + 1);
  const sf = ctx.program.getSourceFile("file:///T.java" as Uri)!;
  return ctx.checker.resolveName(getIdentifierAtPosition(sf, offset) as Identifier)!;
}

test("method hover shows the full signature", () => {
  const ctx = setup("class C { int add(int a, int b) { return a + b; } }");
  expect(getHoverText(ctx.checker, symbolAt(ctx, "add"))).toBe("int add(int a, int b)");
});

test("generic method signature includes type parameters and throws", () => {
  const ctx = setup("class C { <T> T pick(T x, T y) throws Exception { return x; } }");
  expect(getHoverText(ctx.checker, symbolAt(ctx, "pick"))).toBe(
    "<T> T pick(T x, T y) throws Exception",
  );
});

test("constructor signature omits a return type", () => {
  const ctx = setup("class C { C(int a) {} }");
  expect(getHoverText(ctx.checker, symbolAt(ctx, "C", 2))).toBe("C(int a)");
});

test("record-pattern binding variable hovers as its type", () => {
  const ctx = setup(
    "record Circle(double radius) {}\nclass C { double m(Object s) { return switch (s) { case Circle(double zz) -> zz; default -> 0.0; }; } }",
  );
  // the use of `zz` in the arrow body
  expect(getHoverText(ctx.checker, symbolAt(ctx, "zz", 2))).toBe("(local variable) double zz");
});

test("concise lambda parameter resolves and infers its type from the target", () => {
  const ctx = setup(
    "class C { java.util.function.Function<Integer, Integer> twice = x -> x * 2; }",
  );
  expect(getHoverText(ctx.checker, symbolAt(ctx, "x", 2))).toBe("(parameter) Integer x");
});

test("multi-parameter lambda infers each parameter from the SAM", () => {
  const ctx = setup(
    "class C { java.util.function.BiFunction<String, Integer, Integer> f = (key, num) -> num; }",
  );
  expect(getHoverText(ctx.checker, symbolAt(ctx, "key", 1))).toBe("(parameter) String key");
  expect(getHoverText(ctx.checker, symbolAt(ctx, "num", 1))).toBe("(parameter) Integer num");
});

test("lambda parameter with an unknown target resolves without a type", () => {
  const ctx = setup("class C { void m() { var f = x -> x; } }");
  // resolves (so hover appears) but the target type is unknown, so no type shown
  expect(getHoverText(ctx.checker, symbolAt(ctx, "x", 2))).toBe("(parameter) x");
});

test("array .length resolves as an int field, Object methods resolve on arrays", () => {
  const ctx = setup(
    "class C { void m(String[] arr) { int n = arr.length; var s = arr.hashCode(); } }",
  );
  expect(getHoverText(ctx.checker, symbolAt(ctx, "length"))).toBe("(field) int length");
  expect(getHoverText(ctx.checker, symbolAt(ctx, "hashCode"))).toBe("int hashCode()");
});

test("package-name qualifiers resolve and hover as packages", () => {
  const ctx = setup("class C { java.util.List<String> xs; }");
  expect(getHoverText(ctx.checker, symbolAt(ctx, "java"))).toBe("package java");
  expect(getHoverText(ctx.checker, symbolAt(ctx, "util"))).toBe("package java.util");
  expect(getHoverText(ctx.checker, symbolAt(ctx, "List"))).toBe("interface List");
});

test("getDocumentation returns the cleaned Javadoc of a method", () => {
  const ctx = setup(
    [
      "class C {",
      "  /**",
      "   * Adds two numbers.",
      "   * @param a first",
      "   */",
      "  int add(int a, int b) { return a + b; }",
      "}",
    ].join("\n"),
  );
  expect(ctx.checker.getDocumentation(symbolAt(ctx, "add"))).toBe(
    "Adds two numbers.\n@param a first",
  );
});

test("getDocumentation is undefined when there is no doc comment", () => {
  const ctx = setup("class C {\n  // not javadoc\n  int add(int a) { return a; } }");
  expect(ctx.checker.getDocumentation(symbolAt(ctx, "add"))).toBeUndefined();
});

test("getDocumentation reads a class Javadoc", () => {
  const ctx = setup("/** A widget. */\nclass Widget {}");
  expect(ctx.checker.getDocumentation(symbolAt(ctx, "Widget"))).toBe("A widget.");
});

// Hover on an overloaded call uses the resolved overload's signature, the way
// the server does it (resolveCall -> signatureOfDeclaration).
function callSignatureAt(ctx: ReturnType<typeof setup>, needle: string, occ = 1) {
  let offset = -1;
  for (let i = 0; i < occ; i++) offset = ctx.text.indexOf(needle, offset + 1);
  const sf = ctx.program.getSourceFile("file:///T.java" as Uri)!;
  const id = getIdentifierAtPosition(sf, offset) as Identifier;
  const call = id.parent as CallExpression;
  const decl = ctx.checker.resolveCall(call)!;
  return ctx.checker.signatureOfDeclaration(decl);
}

test("hover picks the signature of the matching overload (String argument)", () => {
  const ctx = setup(
    'class C { int f(int a){return 0;} String f(String s){return "";} void m(){ f("x"); } }',
  );
  expect(callSignatureAt(ctx, "f(", 3)).toBe("String f(String s)"); // f( occ3 = the call site
});

test("hover picks the signature of the matching overload (int argument)", () => {
  const ctx = setup(
    'class C { int f(int a){return 0;} String f(String s){return "";} void m(){ f(1); } }',
  );
  expect(callSignatureAt(ctx, "f(", 3)).toBe("int f(int a)");
});
