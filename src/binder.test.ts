import { test } from "node:test";
import { expect } from "expect";

import { forEachChild, parseSourceFile } from "./parser.ts";
import { bindSourceFile } from "./binder.ts";
import {
  type ClassDeclaration,
  type MethodDeclaration,
  type Node,
  type SourceFile,
  SymbolFlags,
  SyntaxKind,
} from "./types.ts";

function bindJava(text: string): SourceFile {
  const sf = parseSourceFile("Test.java", text);
  bindSourceFile(sf);
  return sf;
}

test("top-level types are declared in the file scope", () => {
  const sf = bindJava("class A {} interface B {}");
  expect(sf.locals?.get("A")?.flags).toBe(SymbolFlags.Class);
  expect(sf.locals?.get("B")?.flags).toBe(SymbolFlags.Interface);
});

test("class members get a symbol table", () => {
  const sf = bindJava("class C { int x; void m() {} }");
  const c = sf.locals!.get("C")!;
  expect(c.members?.get("x")?.flags).toBe(SymbolFlags.Field);
  expect(c.members?.get("m")?.flags).toBe(SymbolFlags.Method);
});

test("duplicate field is reported", () => {
  const sf = bindJava("class C { int x; int x; }");
  expect(sf.bindDiagnostics!.length).toBe(1);
  expect(sf.bindDiagnostics![0]!.messageText).toContain("x");
});

test("method overloads do not collide", () => {
  const sf = bindJava("class C { void m() {} void m(int a) {} }");
  expect(sf.bindDiagnostics).toHaveLength(0);
  const m = sf.locals!.get("C")!.members!.get("m")!;
  expect(m.declarations).toHaveLength(2);
});

test("parameters and locals are scoped to their method/block", () => {
  const sf = bindJava("class C { void m(int a) { int b; } }");
  const method = sf.locals!.get("C")!.members!.get("m")!.declarations![0] as MethodDeclaration;
  expect(method.locals?.get("a")?.flags).toBe(SymbolFlags.Parameter);
  // The body block has its own scope holding the local 'b'.
  expect(method.body!.locals?.get("b")?.flags).toBe(SymbolFlags.LocalVariable);
});

test("type parameters are bound", () => {
  const sf = bindJava("class C<T, U> {}");
  const c = sf.locals!.get("C")!;
  expect(c.members?.get("T")?.flags).toBe(SymbolFlags.TypeParameter);
  expect(c.members?.get("U")?.flags).toBe(SymbolFlags.TypeParameter);
});

test("enum constants are bound as symbols", () => {
  const sf = bindJava("enum E { A, B, C }");
  const e = sf.locals!.get("E")!;
  expect(e.members?.get("A")?.flags).toBe(SymbolFlags.EnumConstant);
  expect(e.members?.get("C")?.flags).toBe(SymbolFlags.EnumConstant);
});

test("nested classes are members of the outer class", () => {
  const sf = bindJava("class Outer { class Inner {} }");
  const outer = sf.locals!.get("Outer")!;
  expect(outer.members?.get("Inner")?.flags).toBe(SymbolFlags.Class);
});

test("parent pointers are set throughout the tree", () => {
  const sf = bindJava("class C { void m() { int x = 1; } }");
  const c = sf.statements[0] as ClassDeclaration;
  expect(c.parent).toBe(sf);
  const m = c.members[0] as MethodDeclaration;
  expect(m.parent).toBe(c);
  expect(m.name.parent).toBe(m);
  expect(m.body!.parent).toBe(m);
});

test("multiple declarators in a field each get a symbol", () => {
  const sf = bindJava("class C { int a, b, c; }");
  const c = sf.locals!.get("C")!;
  expect(c.members?.has("a")).toBe(true);
  expect(c.members?.has("b")).toBe(true);
  expect(c.members?.has("c")).toBe(true);
});

test("node.symbol is attached to declarations", () => {
  const sf = bindJava("class C {}");
  const c = sf.statements[0] as Node;
  expect(c.symbol).toBe(sf.locals!.get("C"));
});

test("local class is scoped to the enclosing block", () => {
  const sf = bindJava("class C { void m() { class Local {} } }");
  const method = sf.locals!.get("C")!.members!.get("m")!.declarations![0] as MethodDeclaration;
  expect(method.body!.locals?.get("Local")?.flags).toBe(SymbolFlags.Class);
});

test("unnamed variables '_' are not declared and do not collide (M15)", () => {
  const sf = bindJava("class C { void m() { var _ = a(); var _ = b(); } }");
  expect(sf.bindDiagnostics).toHaveLength(0);
});

test("typed lambda parameters are scoped to the lambda (M10)", () => {
  const sf = bindJava("class C { Runnable r = (int a) -> { int b = a; }; }");
  expect(sf.bindDiagnostics).toHaveLength(0);
  // Find the lambda node and check its parameter scope.
  let lambda: Node | undefined;
  const visit = (n: Node): undefined => {
    if (n.kind === SyntaxKind.LambdaExpression) lambda = n;
    forEachChild(n, visit);
    return undefined;
  };
  visit(sf);
  expect(lambda?.locals?.get("a")?.flags).toBe(SymbolFlags.Parameter);
});

test("symbol.parent and valueDeclaration are set (P1)", () => {
  const sf = bindJava("class C { int x; void m() {} }");
  const c = sf.locals!.get("C")!;
  const x = c.members!.get("x")!;
  expect(x.parent).toBe(c); // member -> enclosing type
  expect(x.valueDeclaration).toBe(x.declarations![0]);
  expect(c.valueDeclaration).toBe(c.declarations![0]);
});
