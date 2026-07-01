import { mkdirSync, writeFileSync } from "node:fs";
import TempDir from "../TempDir.ts";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import { createChecker } from "../compiler/checker.ts";
import { loadConfig } from "../config.ts";
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

test("member completion keeps working once a partial member name is typed", () => {
  // the cursor is no longer right after '.', so the '.' must be found past the
  // partial identifier - otherwise completion vanishes as soon as you type.
  expect(complete("class U { void m(String s) { s.sub/*|*/ } }").map(i => i.label)).toContain(
    "substring",
  );
  // a bare instance field receiver (no `this.`), mid-token
  expect(
    complete("class C { String name; void m() { name.len/*|*/ } }").map(i => i.label),
  ).toContain("length");
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

test("classpath resources complete inside a getResourceAsStream string", () => {
  using dir = TempDir.create("cappu-res-");
  writeFileSync(join(dir.path, "cappu.json"), "{}\n");
  mkdirSync(join(dir.path, "src", "main", "resources", "db"), { recursive: true });
  writeFileSync(join(dir.path, "src", "main", "resources", "messages.properties"), "x=1");
  writeFileSync(join(dir.path, "src", "main", "resources", "db", "schema.sql"), "create");
  const config = loadConfig(undefined, dir.path);

  const src = 'class C { void m() throws Exception { getClass().getResourceAsStream("/*|*/"); } }';
  const offset = src.indexOf("/*|*/");
  const program = createProgram();
  program.setOpenDocument("file:///C.java" as Uri, src.replace("/*|*/", ""), 1);
  const checker = createChecker(program);
  const sf = program.getSourceFile("file:///C.java" as Uri)!;

  const items = getCompletions(program, checker, sf, offset, config);
  expect(items.map(i => i.label).sort()).toEqual(["/db/schema.sql", "/messages.properties"]);
  expect(items[0]!.kind).toBe(CompletionItemKind.File);
});

test("completion kinds are classified", () => {
  const items = complete(
    "class P { int age; String greet() { return null; } } class U { void m(P p) { p./*|*/ } }",
  );
  const byLabel = new Map(items.map(i => [i.label, i.kind]));
  expect(byLabel.get("age")).toBe(CompletionItemKind.Field);
  expect(byLabel.get("greet")).toBe(CompletionItemKind.Method);
});

test("deprecated members are flagged (method and field, not fresh ones)", () => {
  const items = complete(
    "class P { @Deprecated int old; int cur; @Deprecated String gone() { return null; } String live() { return null; } }" +
      " class U { void m(P p) { p./*|*/ } }",
  );
  const byLabel = new Map(items.map(i => [i.label, i.deprecated]));
  expect(byLabel.get("old")).toBe(true);
  expect(byLabel.get("gone")).toBe(true);
  expect(byLabel.get("cur")).toBe(false);
  expect(byLabel.get("live")).toBe(false);
});
