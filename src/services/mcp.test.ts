import { test } from "node:test";
import { expect } from "expect";

import { createChecker } from "../compiler/checker.ts";
import { createProgram } from "../compiler/program.ts";
import { type Uri } from "../workspace.ts";
import { languageFeatures } from "./codeActions.ts";
import { createMcpTools } from "./mcp.ts";

function toolsFor(files: Record<string, string>) {
  const program = createProgram();
  for (const [uri, text] of Object.entries(files)) program.addProjectFile(uri as Uri, text);
  const checker = createChecker(program);
  return createMcpTools(program, checker, languageFeatures(undefined));
}

test("diagnostics reports a syntax error with a 1-based location", () => {
  const tools = toolsFor({ "file:///Bad.java": "class Bad { void m( }" });
  const { diagnostics } = tools.diagnostics({});
  expect(diagnostics.length).toBeGreaterThan(0);
  const d = diagnostics[0];
  expect(d.file).toBe("/Bad.java");
  expect(d.severity).toBe("error");
  expect(d.line).toBeGreaterThanOrEqual(1);
  expect(d.column).toBeGreaterThanOrEqual(1);
});

test("diagnostics is empty for a valid file", () => {
  const tools = toolsFor({ "file:///Ok.java": "class Ok { int m() { return 1; } }" });
  expect(tools.diagnostics({}).diagnostics).toEqual([]);
});

test("diagnostics honors an explicit file path filter", () => {
  const tools = toolsFor({
    "file:///Bad.java": "class Bad { void m( }",
    "file:///Ok.java": "class Ok {}",
  });
  const { diagnostics } = tools.diagnostics({ files: ["/Ok.java"] });
  expect(diagnostics).toEqual([]);
});

test("deprecated_uses finds @Deprecated method and type uses with details", () => {
  const tools = toolsFor({
    "file:///Api.java": [
      "class Api {",
      '  @Deprecated(since="2.0", forRemoval=true) static int old() { return 1; }',
      "  static int ok() { return 2; }",
      "}",
      "@Deprecated class Legacy {}",
      "class Use {",
      "  void m() { int a = Api.old(); int b = Api.ok(); Legacy x = null; }",
      "}",
    ].join("\n"),
  });
  const { deprecatedUses } = tools.deprecatedUses({});
  const byName = Object.fromEntries(deprecatedUses.map(u => [u.name, u]));
  expect(Object.keys(byName).sort()).toEqual(["Legacy", "old"]);
  expect(byName.old).toMatchObject({ kind: "method", since: "2.0", forRemoval: true });
  expect(byName.old.message).toContain("marked for removal");
  expect(byName.Legacy).toMatchObject({ kind: "type", forRemoval: false });
  expect(byName.old.line).toBeGreaterThanOrEqual(1);
});

test("deprecated_uses finds @Deprecated field accesses", () => {
  const tools = toolsFor({
    "file:///Api.java": [
      "class Api {",
      '  @Deprecated(since="2.0") public static int OLD = 1;',
      "  public static int OK = 2;",
      "}",
      "class Use {",
      "  int m() { return Api.OLD + Api.OK; }",
      "}",
    ].join("\n"),
  });
  const { deprecatedUses } = tools.deprecatedUses({});
  expect(deprecatedUses.map(u => u.name)).toEqual(["OLD"]);
  expect(deprecatedUses[0]).toMatchObject({ kind: "field", since: "2.0" });
  expect(deprecatedUses[0].message).toContain("Field 'OLD' is deprecated");
});

test("deprecated_uses is empty when nothing deprecated is used", () => {
  const tools = toolsFor({ "file:///Ok.java": "class Ok { int m() { return 1; } }" });
  expect(tools.deprecatedUses({}).deprecatedUses).toEqual([]);
});

test("outline returns the top-level types of a file", () => {
  const tools = toolsFor({ "file:///Foo.java": "class Foo { int x; void m() {} }" });
  const { symbols } = tools.outline({ file: "/Foo.java" });
  expect(symbols).toHaveLength(1);
  expect(symbols[0].name).toBe("Foo");
});

test("outline is empty for an unknown file", () => {
  const tools = toolsFor({ "file:///Foo.java": "class Foo {}" });
  expect(tools.outline({ file: "/Missing.java" }).symbols).toEqual([]);
});

test("searchSymbols matches type fqns case-insensitively by substring", () => {
  const tools = toolsFor({
    "file:///UserService.java": "package app; class UserService {}",
    "file:///Repo.java": "package app; class Repo {}",
  });
  expect(tools.searchSymbols({ query: "service" }).matches).toEqual(["app.UserService"]);
});

test("describeSymbol returns kind, label and definition for a type", () => {
  const tools = toolsFor({ "file:///Foo.java": "package a; class Foo {}" });
  const { matches } = tools.describeSymbol({ ref: "a.Foo" });
  expect(matches).toHaveLength(1);
  expect(matches[0].kind).toBe("class");
  expect(matches[0].label).toBe("class Foo");
  expect(matches[0].definition?.file).toBe("/Foo.java");
});

test("describeSymbol resolves a method member and includes a signature", () => {
  const tools = toolsFor({
    "file:///Foo.java": "package a; class Foo { int add(int x) { return x; } }",
  });
  const { matches } = tools.describeSymbol({ ref: "a.Foo#add" });
  expect(matches).toHaveLength(1);
  expect(matches[0].kind).toBe("method");
  expect(matches[0].signature).toContain("add");
});

test("findDefinition returns the declaration location", () => {
  const tools = toolsFor({ "file:///Foo.java": "package a; class Foo {}" });
  const { definitions } = tools.findDefinition({ ref: "a.Foo" });
  expect(definitions).toHaveLength(1);
  expect(definitions[0].file).toBe("/Foo.java");
  expect(definitions[0].line).toBe(1);
});

test("findReferences returns every use of a field", () => {
  const tools = toolsFor({
    "file:///Foo.java": "package a; class Foo { int f; void m() { f = f + 1; } }",
  });
  const { references } = tools.findReferences({ ref: "a.Foo#f" });
  expect(references.length).toBe(3);
});

test("findReferences reports ambiguity instead of guessing", () => {
  const tools = toolsFor({
    "file:///a/Foo.java": "package a; class Foo {}",
    "file:///b/Foo.java": "package b; class Foo {}",
  });
  const result = tools.findReferences({ ref: "Foo" });
  expect(result.ambiguous).toBe(true);
  expect(result.candidates).toBe(2);
  expect(result.references).toEqual([]);
});

test("findImplementations lists the implementers of an interface", () => {
  const tools = toolsFor({
    "file:///Shape.java": "package a; interface Shape {}",
    "file:///Circle.java": "package a; class Circle implements Shape {}",
    "file:///Square.java": "package a; class Square implements Shape {}",
  });
  const { implementations } = tools.findImplementations({ ref: "a.Shape" });
  expect(implementations.map(i => i.label).sort()).toEqual(["class Circle", "class Square"]);
});

test("findImplementations lists method overrides", () => {
  const tools = toolsFor({
    "file:///Animal.java": "package a; abstract class Animal { abstract String sound(); }",
    "file:///Dog.java": 'package a; class Dog extends Animal { String sound() { return "woof"; } }',
  });
  const { implementations } = tools.findImplementations({ ref: "a.Animal#sound" });
  expect(implementations).toHaveLength(1);
  expect(implementations[0].label).toContain("sound");
  expect(implementations[0].definition?.file).toBe("/Dog.java");
});

test("findImplementations reports ambiguity for a bare name", () => {
  const tools = toolsFor({
    "file:///a/Shape.java": "package a; interface Shape {}",
    "file:///b/Shape.java": "package b; interface Shape {}",
  });
  const result = tools.findImplementations({ ref: "Shape" });
  expect(result.ambiguous).toBe(true);
  expect(result.candidates).toBe(2);
});

test("listMembers includes declared and inherited members with an inherited flag", () => {
  const tools = toolsFor({
    "file:///Base.java": "package a; class Base { int b() { return 0; } }",
    "file:///Sub.java": "package a; class Sub extends Base { int s; }",
  });
  const { members } = tools.listMembers({ ref: "a.Sub" });
  const field = members.find(m => m.kind === "field");
  const method = members.find(m => m.kind === "method");
  expect(field?.inherited).toBe(false);
  expect(method?.inherited).toBe(true);
});

test("findCallers returns only the call sites of a method", () => {
  const tools = toolsFor({
    "file:///A.java": "package a; class A { void run() { helper(); helper(); } void helper() {} }",
  });
  const { callers } = tools.findCallers({ ref: "a.A#helper" });
  expect(callers).toHaveLength(2);
});

test("typeHierarchy reports supertypes and subtypes", () => {
  const tools = toolsFor({
    "file:///I.java": "package a; interface I {}",
    "file:///M.java": "package a; class M implements I {}",
    "file:///N.java": "package a; class N extends M {}",
  });
  const { supertypes, subtypes } = tools.typeHierarchy({ ref: "a.M" });
  expect(supertypes.map(s => s.label)).toEqual(["interface I"]);
  expect(subtypes.map(s => s.label)).toEqual(["class N"]);
});

test("resolveImport returns fqn candidates for a simple name", () => {
  const tools = toolsFor({
    "file:///MyList.java": "package a.util; class MyList {}",
    "file:///Other.java": "package b; class MyList {}",
  });
  expect(tools.resolveImport({ name: "MyList" }).imports.sort()).toEqual([
    "a.util.MyList",
    "b.MyList",
  ]);
});

test("renameSymbol returns an edit per occurrence", () => {
  const tools = toolsFor({
    "file:///A.java": "package a; class A { int x; void m() { x = x + 1; } }",
  });
  const { edits } = tools.renameSymbol({ ref: "a.A#x", newName: "y" });
  expect(edits).toHaveLength(3);
  expect(edits.every(e => e.newText === "y")).toBe(true);
  expect(edits[0].file).toBe("/A.java");
});

test("renameSymbol rejects an invalid identifier", () => {
  const tools = toolsFor({ "file:///A.java": "package a; class A { int x; }" });
  const r = tools.renameSymbol({ ref: "a.A#x", newName: "1bad" });
  expect(r.error).toMatch(/valid Java identifier/);
  expect(r.edits).toEqual([]);
});

test("renameSymbol refuses a symbol defined in a synthetic classpath stub", () => {
  const tools = toolsFor({
    "classpath:///dep/Lib.java": "package dep; public class Lib { public int x; }",
  });
  const r = tools.renameSymbol({ ref: "dep.Lib#x", newName: "y" });
  expect(r.edits).toEqual([]);
  expect(r.error).toMatch(/JDK/);
});

test("codeActions offers a refactoring with 1-based edit positions", () => {
  const tools = toolsFor({
    "file:///T.java": "class T {\n  private int x = 1;\n  int use() { return x; }\n}",
  });
  // Caret on the field name `x` (line 2, column 15, both 1-based).
  const { actions } = tools.codeActions({ file: "/T.java", startLine: 2, startColumn: 15 });
  const final = actions.find(a => a.title === "Add 'final' modifier");
  expect(final).toBeDefined();
  expect(final!.kind).toBe("quickfix");
  expect(final!.edits.length).toBeGreaterThan(0);
  expect(final!.edits[0]!.file).toBe("/T.java");
  expect(final!.edits[0]!.line).toBe(2);
});

test("codeActions is empty for an unknown file", () => {
  const tools = toolsFor({ "file:///T.java": "class T {}" });
  expect(tools.codeActions({ file: "/Nope.java", startLine: 1, startColumn: 1 }).actions).toEqual(
    [],
  );
});
