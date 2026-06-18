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
