import { test } from "node:test";
import { expect } from "expect";

import { createProgram } from "./program.ts";
import { SymbolFlags } from "./types.ts";

test("getSourceFile parses and binds an open document", () => {
  const program = createProgram();
  program.setOpenDocument("file:///A.java", "class A {}", 1);
  const sf = program.getSourceFile("file:///A.java");
  expect(sf).toBeDefined();
  expect(sf!.locals?.get("A")?.flags).toBe(SymbolFlags.Class);
});

test("result is cached per version and rebuilt on change", () => {
  const program = createProgram();
  program.setOpenDocument("file:///A.java", "class A {}", 1);
  const first = program.getSourceFile("file:///A.java");
  expect(program.getSourceFile("file:///A.java")).toBe(first); // same version -> cached

  program.setOpenDocument("file:///A.java", "class B {}", 2);
  const second = program.getSourceFile("file:///A.java");
  expect(second).not.toBe(first);
  expect(second!.locals?.get("B")?.flags).toBe(SymbolFlags.Class);
});

test("unknown and closed documents return undefined", () => {
  const program = createProgram();
  expect(program.getSourceFile("file:///missing.java")).toBeUndefined();
  program.setOpenDocument("file:///A.java", "class A {}", 1);
  expect(program.getOpenUris()).toEqual(["file:///A.java"]);
  program.closeDocument("file:///A.java");
  expect(program.getSourceFile("file:///A.java")).toBeUndefined();
  expect(program.getOpenUris()).toEqual([]);
});
