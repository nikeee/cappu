import { test } from "node:test";
import { expect } from "expect";

import { createChecker } from "../compiler/checker.ts";
import { createProgram } from "../compiler/program.ts";
import { type Uri } from "../workspace.ts";
import { createMcpTools } from "./mcp.ts";

function toolsFor(files: Record<string, string>) {
  const program = createProgram();
  for (const [uri, text] of Object.entries(files)) program.addProjectFile(uri as Uri, text);
  const checker = createChecker(program);
  return createMcpTools(program, checker);
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
