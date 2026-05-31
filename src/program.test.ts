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

test("global index resolves types across files by FQN and package", () => {
  const program = createProgram();
  program.setOpenDocument("file:///A.java", "package com.app;\nclass A {}", 1);
  program.setOpenDocument("file:///B.java", "package com.app;\ninterface B {}", 1);
  program.setOpenDocument("file:///C.java", "class C {}", 1); // default package
  const index = program.getGlobalIndex();

  expect(index.getType("com.app.A")?.flags).toBe(SymbolFlags.Class);
  expect(index.getType("com.app.B")?.flags).toBe(SymbolFlags.Interface);
  expect(index.getType("C")?.flags).toBe(SymbolFlags.Class);

  const pkg = index.getPackageTypes("com.app")!;
  expect([...pkg.keys()].sort()).toEqual(["A", "B"]);
  expect(index.getPackageSymbol("com.app")?.flags).toBe(SymbolFlags.Package);
  // top-level type's parent is the package symbol
  expect(index.getType("com.app.A")?.parent).toBe(index.getPackageSymbol("com.app"));
});

test("index rebuilds when a document changes", () => {
  const program = createProgram();
  program.setOpenDocument("file:///A.java", "package p;\nclass A {}", 1);
  expect(program.getGlobalIndex().getType("p.A")).toBeDefined();
  program.setOpenDocument("file:///A.java", "package p;\nclass Renamed {}", 2);
  const index = program.getGlobalIndex();
  expect(index.getType("p.A")).toBeUndefined();
  expect(index.getType("p.Renamed")).toBeDefined();
});

test("changing one file re-binds only that file (others keep their SourceFile)", () => {
  const program = createProgram();
  program.setOpenDocument("file:///A.java", "class A {}", 1);
  program.setOpenDocument("file:///B.java", "class B {}", 1);
  program.getGlobalIndex();
  const aBefore = program.getSourceFile("file:///A.java");

  program.setOpenDocument("file:///B.java", "class B2 {}", 2);
  const index = program.getGlobalIndex();
  expect(index.getType("B")).toBeUndefined();
  expect(index.getType("B2")).toBeDefined();
  // The untouched file's bound SourceFile is reused - no re-bind on reindex.
  expect(program.getSourceFile("file:///A.java")).toBe(aBefore);
  expect(index.getType("A")).toBeDefined();
});

test("closing a document removes only its types from the index", () => {
  const program = createProgram();
  program.addProjectFile("file:///A.java", "package p;\nclass A {}");
  program.setOpenDocument("file:///B.java", "package p;\nclass B {}", 1);
  expect(program.getGlobalIndex().getType("p.B")).toBeDefined();

  program.closeDocument("file:///B.java");
  const index = program.getGlobalIndex();
  expect(index.getType("p.B")).toBeUndefined();
  expect(index.getType("p.A")).toBeDefined();
});
