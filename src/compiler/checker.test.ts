import { test } from "node:test";

import { expect } from "expect";

import { createChecker } from "./checker.ts";
import { type Checker } from "./checker.ts";
import { typeToString } from "./checkerTypes.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { getIdentifierAtPosition, getNodeAtPosition } from "../services/nodeAtPosition.ts";
import { createProgram } from "./program.ts";
import type { Program } from "./program.ts";
import {
  type Identifier,
  type Node,
  SymbolFlags,
  SyntaxKind,
  type VariableDeclarator,
} from "./types.ts";

function setup(text: string): { program: Program; checker: Checker; uri: Uri } {
  const program = createProgram();
  loadJdkStub(program);
  const uri = "file:///T.java" as Uri;
  program.setOpenDocument(uri, text, 1);
  return { program, checker: createChecker(program), uri };
}

function identifierAt(
  { program, uri }: { program: Program; uri: Uri },
  needle: string,
  occurrence = 1,
): Identifier {
  const sf = program.getSourceFile(uri)!;
  let offset = -1;
  for (let i = 0; i < occurrence; i++) offset = sf.text.indexOf(needle, offset + 1);
  return getIdentifierAtPosition(sf, offset) as Identifier;
}

test("declared types of fields and methods", () => {
  const ctx = setup("class C { String name; int count() { return 0; } }");
  const nameSym = ctx.checker.resolveName(identifierAt(ctx, "name"))!;
  expect(typeToString(ctx.checker.getTypeOfSymbol(nameSym))).toBe("String");
  const countSym = ctx.checker.resolveName(identifierAt(ctx, "count"))!;
  expect(typeToString(ctx.checker.getTypeOfSymbol(countSym))).toBe("int");
});

test("member access resolves and types through the stub", () => {
  const ctx = setup("class C { String s; void m() { s.length(); } }");
  const lengthSym = ctx.checker.resolveName(identifierAt(ctx, "length"))!;
  expect(lengthSym.flags).toBe(SymbolFlags.Method);
  // type of the whole call s.length()
  const sf = ctx.program.getSourceFile(ctx.uri)!;
  let node: Node = getNodeAtPosition(sf, sf.text.indexOf("length"));
  while (node.kind !== SyntaxKind.CallExpression) node = node.parent;
  expect(typeToString(ctx.checker.getTypeOfExpression(node))).toBe("int");
});

// Type of a local variable initializer expression.
function initializerType(text: string, varName: string): string {
  const ctx = setup(text);
  const sym = ctx.checker.resolveName(identifierAt(ctx, varName))!;
  const declarator = sym.valueDeclaration as VariableDeclarator;
  return typeToString(ctx.checker.getTypeOfExpression(declarator.initializer as Node));
}

test("expression typing: arithmetic, string concat, comparison", () => {
  expect(initializerType("class C { void m() { var x = 1 + 2; } }", "x")).toBe("int");
  expect(initializerType("class C { void m() { var x = 1 + 2.0; } }", "x")).toBe("double");
  expect(initializerType('class C { void m() { var x = "a" + 1; } }', "x")).toBe("String");
  expect(initializerType("class C { void m() { var x = 1 < 2; } }", "x")).toBe("boolean");
  expect(initializerType("class C { void m() { var x = new C(); } }", "x")).toBe("C");
});

test("unary +/-/~ apply unary numeric promotion; ++/-- keep the variable's type", () => {
  // JLS 5.6.1 / 15.15.3-5: -byte is an int (javac picks f(int) for f(-b)).
  expect(initializerType("class C { void m() { byte b = 1; var x = -b; } }", "x")).toBe("int");
  expect(initializerType("class C { void m() { char c = 'a'; var x = ~c; } }", "x")).toBe("int");
  expect(initializerType("class C { void m() { short s = 1; var x = +s; } }", "x")).toBe("int");
  expect(initializerType("class C { void m() { long l = 1L; var x = -l; } }", "x")).toBe("long");
  // JLS 15.15.1: prefix increment has the type of the variable.
  expect(initializerType("class C { void m() { byte b = 1; var x = ++b; } }", "x")).toBe("byte");
});

test("primitive assignments: widening and fitting constants pass, the rest errors", () => {
  const errs = (text: string): number => {
    const ctx = setup(text);
    return ctx.checker.getSemanticDiagnostics(ctx.program.getSourceFile(ctx.uri)!).length;
  };
  // identity / widening / fitting constants (JLS 5.2)
  expect(errs("class C { void m() { long l = 1; double d = l; byte b = 1; } }")).toBe(0);
  expect(errs("class C { void m() { short s = 1 + 2; char c = 65; } }")).toBe(0);
  // unfoldable values in the constant-narrowing position stay silent: they may
  // be constant variables, which constfold does not resolve
  expect(errs("class C { void m(int p) { final int k = 1; byte b = k; } }")).toBe(0);
  // definite errors
  expect(errs("class C { void m() { byte b = 128; } }")).toBe(1);
  expect(errs("class C { void m() { char c = -1; } }")).toBe(1);
  expect(errs("class C { void m(long p) { int i = p; } }")).toBe(1);
  expect(errs("class C { void m() { float f = 1.5; } }")).toBe(1);
  expect(errs("class C { void m() { int x = 1; boolean y = x; } }")).toBe(1);
  expect(errs("class C { void m() { boolean t = true; int z = t; } }")).toBe(1);
});

test("C-style array brackets after the name add rank (char buf[])", () => {
  const ctx = setup(
    "class C { char buf[]; int grid[][]; void m(int xs[]) { buf = new char[1]; } }",
  );
  const bufSym = ctx.checker.resolveName(identifierAt(ctx, "buf"))!;
  expect(typeToString(ctx.checker.getTypeOfSymbol(bufSym))).toBe("char[]");
  const gridSym = ctx.checker.resolveName(identifierAt(ctx, "grid"))!;
  expect(typeToString(ctx.checker.getTypeOfSymbol(gridSym))).toBe("int[][]");
  const xsSym = ctx.checker.resolveName(identifierAt(ctx, "xs"))!;
  expect(typeToString(ctx.checker.getTypeOfSymbol(xsSym))).toBe("int[]");
  expect(ctx.checker.getSemanticDiagnostics(ctx.program.getSourceFile(ctx.uri)!)).toHaveLength(0);
});

test("calls with an impossible argument count are reported (1304)", () => {
  const arity = (text: string): string[] => {
    const ctx = setup(text);
    return ctx.checker
      .getSemanticDiagnostics(ctx.program.getSourceFile(ctx.uri)!)
      .filter(d => d.code === 1304)
      .map(d => d.messageText);
  };
  // the reported case: lol(String[]) called with no arguments
  expect(arity("class Main { static void lol(String[] a) {} static void m() { lol(); } }")).toEqual(
    ["Invalid number of arguments: expected 1, got 0."],
  );
  // member call through a receiver
  expect(arity("class C { int f(int a, int b) { return 0; } void m(C c) { c.f(1); } }")).toEqual([
    "Invalid number of arguments: expected 2, got 1.",
  ]);
  // overloads: no overload takes three
  expect(arity("class C { void f() {} void f(int a) {} void m() { f(1, 2, 3); } }")).toEqual([
    "Invalid number of arguments: expected 0 or 1, got 3.",
  ]);
  // varargs accept the fixed prefix and up
  expect(
    arity('class C { void f(int a, String... s) {} void m() { f(1); f(1, "x", "y"); } }'),
  ).toEqual([]);
  expect(arity("class C { void f(int a, String... s) {} void m() { f(); } }")).toEqual([
    "Invalid number of arguments: expected 1+, got 0.",
  ]);
  // constructors: declared, implicit default, record canonical
  expect(arity("class A { A(int x) {} } class B { void m() { new A(); } }")).toEqual([
    "Invalid number of arguments: expected 1, got 0.",
  ]);
  expect(arity("class A { } class B { void m() { new A(1); } }")).toEqual([
    "Invalid number of arguments: expected 0, got 1.",
  ]);
  expect(arity("record R(int a, String b) {} class C { void m() { new R(1); } }")).toEqual([
    "Invalid number of arguments: expected 2, got 1.",
  ]);
  // matching calls stay silent
  expect(
    arity(
      "class A { A(int x) {} } record R(int a) {} class C { void f(int a) {} void m() { f(1); new A(2); new R(3); } }",
    ),
  ).toEqual([]);
});

test("unknown expressions degrade to <error>, never throw", () => {
  expect(initializerType("class C { void m() { var x = mystery(); } }", "x")).toBe("<error>");
});

// P6: assignability

import { nullType } from "./checkerTypes.ts";
import { type Uri } from "../workspace.ts";

test("assignability: widening, boxing, subtyping, arrays, null", () => {
  const ctx = setup(
    "class C { int iv; long lv; double dv; Integer bi; String sv; Object ov;" +
      " java.util.ArrayList<String> al; java.util.List<String> ls; int[] ia; String[] sa; }",
  );
  const t = (name: string) =>
    ctx.checker.getTypeOfSymbol(ctx.checker.resolveName(identifierAt(ctx, name))!);
  const a = (x: string, y: string) => ctx.checker.isAssignableTo(t(x), t(y));

  // primitive widening
  expect(a("iv", "lv")).toBe(true); // int -> long
  expect(a("iv", "dv")).toBe(true); // int -> double
  expect(a("lv", "iv")).toBe(false); // long -> int (narrowing)
  // boxing / unboxing
  expect(a("iv", "bi")).toBe(true); // int -> Integer
  expect(a("bi", "iv")).toBe(true); // Integer -> int
  // reference subtyping
  expect(a("sv", "ov")).toBe(true); // String -> Object
  expect(a("ov", "sv")).toBe(false); // Object -> String
  expect(a("al", "ls")).toBe(true); // ArrayList<String> -> List<String>
  // arrays
  expect(a("sa", "ov")).toBe(true); // String[] -> Object
  expect(a("ia", "sa")).toBe(false); // int[] -> String[]
  // null
  expect(ctx.checker.isAssignableTo(nullType, t("sv"))).toBe(true);
  expect(ctx.checker.isAssignableTo(nullType, t("iv"))).toBe(false);
});

test("wildcard variance in type arguments", () => {
  const ctx = setup(
    "class C { java.util.List<String> ls; java.util.List<? extends Object> wl; java.util.List<Object> lo; }",
  );
  const t = (name: string) =>
    ctx.checker.getTypeOfSymbol(ctx.checker.resolveName(identifierAt(ctx, name))!);
  // List<String> -> List<? extends Object> (covariant)
  expect(ctx.checker.isAssignableTo(t("ls"), t("wl"))).toBe(true);
  // List<String> -> List<Object> (invariant) is NOT allowed
  expect(ctx.checker.isAssignableTo(t("ls"), t("lo"))).toBe(false);
});

// P7: overload resolution

test("overload resolution picks the overload matching the argument types", () => {
  expect(
    initializerType(
      'class C { String f(int x){return "";} int f(String s){return 0;} void m(){ var res = f(1); } }',
      "res",
    ),
  ).toBe("String");
  expect(
    initializerType(
      'class C { String f(int x){return "";} int f(String s){return 0;} void m(){ var res = f("s"); } }',
      "res",
    ),
  ).toBe("int");
});

test("strict phase beats boxing phase", () => {
  // f(1): the int overload applies strictly; the Integer overload only via boxing
  expect(
    initializerType(
      'class C { int f(int x){return 0;} String f(Integer i){return "";} void m(){ var res = f(1); } }',
      "res",
    ),
  ).toBe("int");
});

test("varargs overload is used only when no fixed-arity overload applies", () => {
  expect(
    initializerType(
      "class C { int g(int... xs){return 0;} void m(){ var res = g(1, 2, 3); } }",
      "res",
    ),
  ).toBe("int");
  // a fixed-arity overload wins over varargs for an exact arity match
  expect(
    initializerType(
      'class C { String g(int a){return "";} int g(int... xs){return 0;} void m(){ var res = g(7); } }',
      "res",
    ),
  ).toBe("String");
});

test("most-specific overload is chosen", () => {
  // h(String) is more specific than h(Object); "s" matches both, String wins
  expect(
    initializerType(
      'class C { int h(Object o){return 0;} String h(String s){return "";} void m(){ var res = h("s"); } }',
      "res",
    ),
  ).toBe("String");
});

// P9: semantic diagnostics (type mismatch)

function semanticDiags(text: string): string[] {
  const ctx = setup(text);
  const sf = ctx.program.getSourceFile(ctx.uri)!;
  return ctx.checker.getSemanticDiagnostics(sf).map(d => d.messageText);
}

test("type mismatch is reported for concrete incompatible types", () => {
  expect(semanticDiags('class C { int x = "s"; }')).toHaveLength(1);
  expect(semanticDiags("class C { String s = 3; }")).toHaveLength(1);
  expect(semanticDiags('class C { int m() { return "s"; } }')).toHaveLength(1);
  expect(semanticDiags('class C { void m() { int v = 0; v = "s"; } }')).toHaveLength(1);
});

test("compatible assignments produce no diagnostics", () => {
  expect(semanticDiags("class C { int x = 1; long y = x; double d = y; }")).toHaveLength(0);
  expect(semanticDiags('class C { String s = "ok"; Object o = s; Integer bi = 3; }')).toHaveLength(
    0,
  );
  expect(semanticDiags("class C { int m() { return 0; } }")).toHaveLength(0);
});

test("no diagnostics when a type is unknown or generic (no false positives)", () => {
  expect(semanticDiags("class C { void m() { var x = mystery(); int y = x; } }")).toHaveLength(0);
  expect(semanticDiags("class C<T> { T t; void m(T p) { t = p; } }")).toHaveLength(0);
  expect(semanticDiags("class C { java.util.List<String> a; void m() { a = a; } }")).toHaveLength(
    0,
  );
});
