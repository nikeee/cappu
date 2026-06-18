import { test } from "node:test";
import { expect } from "expect";

import { createProgram } from "./program.ts";
import { resolveSymbolRef } from "./mcpResolve.ts";
import { SymbolFlags } from "./types.ts";

function indexFor(files: Record<string, string>) {
  const program = createProgram();
  for (const [uri, text] of Object.entries(files)) program.addProjectFile(uri, text);
  return program.getGlobalIndex();
}

test("resolves a fully-qualified type name", () => {
  const index = indexFor({ "file:///Foo.java": "package a; class Foo {}" });
  const syms = resolveSymbolRef("a.Foo", index);
  expect(syms).toHaveLength(1);
  expect(syms[0].escapedName).toBe("Foo");
  expect(syms[0].flags & SymbolFlags.Class).toBeTruthy();
});

test("resolves a bare simple type name", () => {
  const index = indexFor({ "file:///Foo.java": "package a; class Foo {}" });
  const syms = resolveSymbolRef("Foo", index);
  expect(syms).toHaveLength(1);
  expect(syms[0].escapedName).toBe("Foo");
});

test("returns every candidate for an ambiguous simple name", () => {
  const index = indexFor({
    "file:///a/Foo.java": "package a; class Foo {}",
    "file:///b/Foo.java": "package b; class Foo {}",
  });
  const syms = resolveSymbolRef("Foo", index);
  expect(syms).toHaveLength(2);
});

test("resolves a member via Type#member", () => {
  const index = indexFor({ "file:///Foo.java": "package a; class Foo { int bar() { return 0; } }" });
  const syms = resolveSymbolRef("a.Foo#bar", index);
  expect(syms).toHaveLength(1);
  expect(syms[0].escapedName).toBe("bar");
  expect(syms[0].flags & SymbolFlags.Method).toBeTruthy();
});

test("returns empty for an unknown ref", () => {
  const index = indexFor({ "file:///Foo.java": "package a; class Foo {}" });
  expect(resolveSymbolRef("a.Nope", index)).toEqual([]);
  expect(resolveSymbolRef("a.Foo#nope", index)).toEqual([]);
});
