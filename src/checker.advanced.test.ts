import { test } from "node:test";
import { expect } from "expect";

import { createChecker } from "./checker.ts";
import { Diagnostics } from "./diagnostics.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { createProgram } from "./program.ts";

function diagnose(text: string): number[] {
  const program = createProgram();
  loadJdkStub(program);
  program.setOpenDocument("file:///T.java", text, 1);
  const checker = createChecker(program);
  return checker.getSemanticDiagnostics(program.getSourceFile("file:///T.java")!).map(d => d.code);
}

const OVERRIDE = Diagnostics.Method_does_not_override_a_supertype_method.code;
const NOT_EXHAUSTIVE = Diagnostics.Switch_expression_not_exhaustive_0.code;

// --- @Override ---------------------------------------------------------------------

test("@Override on a method that overrides a super class method is accepted", () => {
  const code = "class Base { void run() {} }\nclass Sub extends Base { @Override void run() {} }";
  expect(diagnose(code)).not.toContain(OVERRIDE);
});

test("@Override on a method that implements an interface method is accepted", () => {
  const code =
    "interface I { int d(); }\nclass C implements I { @Override public int d() { return 1; } }";
  expect(diagnose(code)).not.toContain(OVERRIDE);
});

test("@Override on toString (inherited from Object) is accepted", () => {
  const code = 'class C { @Override public String toString() { return ""; } }';
  expect(diagnose(code)).not.toContain(OVERRIDE);
});

test("@Override on a method that overrides nothing is flagged", () => {
  const code = "class Base {}\nclass Sub extends Base { @Override void nope() {} }";
  expect(diagnose(code)).toContain(OVERRIDE);
});

test("@Override inherited transitively through two levels is accepted", () => {
  const code =
    "class A { void f() {} }\nclass B extends A {}\nclass C extends B { @Override void f() {} }";
  expect(diagnose(code)).not.toContain(OVERRIDE);
});

test("no @Override means no override diagnostic even if nothing is overridden", () => {
  const code = "class Base {}\nclass Sub extends Base { void nope() {} }";
  expect(diagnose(code)).not.toContain(OVERRIDE);
});

test("@Override is not flagged when the super type is unresolved (incomplete info)", () => {
  const code = "class Sub extends Unknown { @Override void whatever() {} }";
  expect(diagnose(code)).not.toContain(OVERRIDE);
});

// --- switch-expression exhaustiveness ----------------------------------------------

test("non-exhaustive enum switch expression without default is flagged", () => {
  const code =
    "enum E { A, B, C }\nclass C0 { int m(E e) { return switch (e) { case A -> 1; case B -> 2; }; } }";
  expect(diagnose(code)).toContain(NOT_EXHAUSTIVE);
});

test("exhaustive enum switch expression is accepted", () => {
  const code =
    "enum E { A, B }\nclass C0 { int m(E e) { return switch (e) { case A -> 1; case B -> 2; }; } }";
  expect(diagnose(code)).not.toContain(NOT_EXHAUSTIVE);
});

test("enum switch expression with default is accepted even if not all covered", () => {
  const code =
    "enum E { A, B, C }\nclass C0 { int m(E e) { return switch (e) { case A -> 1; default -> 0; }; } }";
  expect(diagnose(code)).not.toContain(NOT_EXHAUSTIVE);
});

test("multi-label case counts every constant it lists", () => {
  const code =
    "enum E { A, B, C }\nclass C0 { int m(E e) { return switch (e) { case A, B, C -> 1; }; } }";
  expect(diagnose(code)).not.toContain(NOT_EXHAUSTIVE);
});

test("switch over a non-enum is not flagged", () => {
  const code = "class C0 { int m(int n) { return switch (n) { case 1 -> 1; }; } }";
  expect(diagnose(code)).not.toContain(NOT_EXHAUSTIVE);
});

test("switch statement (not expression) is not subject to exhaustiveness", () => {
  const code = "enum E { A, B }\nclass C0 { void m(E e) { switch (e) { case A -> use(1); } } }";
  expect(diagnose(code)).not.toContain(NOT_EXHAUSTIVE);
});
