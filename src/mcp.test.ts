import { test } from "node:test";
import { expect } from "expect";

import { createChecker } from "./checker.ts";
import { createProgram } from "./program.ts";
import { createMcpTools } from "./mcp.ts";

function toolsFor(files: Record<string, string>) {
  const program = createProgram();
  for (const [uri, text] of Object.entries(files)) program.addProjectFile(uri, text);
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
