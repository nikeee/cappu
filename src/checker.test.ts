import { test } from "node:test";
import { expect } from "expect";

import { createChecker } from "./checker.ts";
import { typeToString } from "./checkerTypes.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { getIdentifierAtPosition, getNodeAtPosition } from "./nodeAtPosition.ts";
import { createProgram } from "./program.ts";
import type { Program } from "./program.ts";
import { type Checker } from "./checker.ts";
import {
  type Identifier,
  type Node,
  SymbolFlags,
  SyntaxKind,
  type VariableDeclarator,
} from "./types.ts";

function setup(text: string): { program: Program; checker: Checker; uri: string } {
  const program = createProgram();
  loadJdkStub(program);
  const uri = "file:///T.java";
  program.setOpenDocument(uri, text, 1);
  return { program, checker: createChecker(program), uri };
}

function identifierAt(
  { program, uri }: { program: Program; uri: string },
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

test("unknown expressions degrade to <error>, never throw", () => {
  expect(initializerType("class C { void m() { var x = mystery(); } }", "x")).toBe("<error>");
});

// P6: assignability

import { nullType } from "./checkerTypes.ts";

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
