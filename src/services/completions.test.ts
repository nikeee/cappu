import { test } from "node:test";

import { expect } from "expect";

import { createChecker } from "../compiler/checker.ts";
import { CompletionItemKind, getCompletions } from "./completions.ts";
import { loadJdkStub } from "../compiler/jdkStub.ts";
import { createProgram } from "../compiler/program.ts";
import { type Uri } from "../workspace.ts";

function complete(text: string, marker = "/*|*/") {
  const offset = text.indexOf(marker);
  const clean = text.replace(marker, "");
  const program = createProgram();
  loadJdkStub(program);
  program.setOpenDocument("file:///T.java" as Uri, clean, 1);
  const checker = createChecker(program);
  const sf = program.getSourceFile("file:///T.java" as Uri)!;
  return getCompletions(program, checker, sf, offset);
}

test("member completion lists the type's members", () => {
  const items = complete("class P { int age; String name; } class U { void m(P p) { p./*|*/ } }");
  // declared members plus what every class inherits from java.lang.Object
  expect(items.map(i => i.label).sort()).toEqual([
    "age",
    "clone",
    "equals",
    "getClass",
    "hashCode",
    "name",
    "toString",
  ]);
});

test("member completion works on incomplete code (no trailing token)", () => {
  // 's.' with nothing after it, then the method/class close - parser recovers
  const items = complete("class U { void m(String s) { s./*|*/ } }");
  expect(items.map(i => i.label)).toContain("length");
  expect(items.map(i => i.label)).toContain("substring");
});

test("member completion on an unknown receiver returns nothing (no guesses)", () => {
  expect(complete("class U { void m() { mystery./*|*/ } }")).toHaveLength(0);
});

test("scope completion offers locals, params, fields, enclosing type and stub types", () => {
  const labels = complete(
    "class Box { int field; void m(int param) { int local = 0; /*|*/ } }",
  ).map(i => i.label);
  expect(labels).toContain("local");
  expect(labels).toContain("param");
  expect(labels).toContain("field");
  expect(labels).toContain("Box");
  expect(labels).toContain("String"); // implicit java.lang
});

test("scope completion still works inside broken code", () => {
  // missing semicolon / dangling expression should not prevent scope completion
  const labels = complete("class C { void m(int p) { int x = ; /*|*/ } }").map(i => i.label);
  expect(labels).toContain("p");
  expect(labels).toContain("x");
});

test("completion kinds are classified", () => {
  const items = complete(
    "class P { int age; String greet() { return null; } } class U { void m(P p) { p./*|*/ } }",
  );
  const byLabel = new Map(items.map(i => [i.label, i.kind]));
  expect(byLabel.get("age")).toBe(CompletionItemKind.Field);
  expect(byLabel.get("greet")).toBe(CompletionItemKind.Method);
});
