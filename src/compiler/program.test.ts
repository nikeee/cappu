import { test } from "node:test";

import { expect } from "expect";

import { createProgram } from "./program.ts";
import { SymbolFlags } from "./types.ts";
import { type Uri } from "../workspace.ts";
import type { Fqn, PackageName } from "./program.ts";

test("getSourceFile parses and binds an open document", () => {
  const program = createProgram();
  program.setOpenDocument("file:///A.java" as Uri, "class A {}", 1);
  const sf = program.getSourceFile("file:///A.java" as Uri);
  expect(sf).toBeDefined();
  expect(sf!.locals?.get("A")?.flags).toBe(SymbolFlags.Class);
});

test("result is cached per version and rebuilt on change", () => {
  const program = createProgram();
  program.setOpenDocument("file:///A.java" as Uri, "class A {}", 1);
  const first = program.getSourceFile("file:///A.java" as Uri);
  expect(program.getSourceFile("file:///A.java" as Uri)).toBe(first); // same version -> cached

  program.setOpenDocument("file:///A.java" as Uri, "class B {}", 2);
  const second = program.getSourceFile("file:///A.java" as Uri);
  expect(second).not.toBe(first);
  expect(second!.locals?.get("B")?.flags).toBe(SymbolFlags.Class);
});

test("unknown and closed documents return undefined", () => {
  const program = createProgram();
  expect(program.getSourceFile("file:///missing.java" as Uri)).toBeUndefined();
  program.setOpenDocument("file:///A.java" as Uri, "class A {}", 1);
  expect(program.getOpenUris()).toEqual(["file:///A.java"]);
  program.closeDocument("file:///A.java" as Uri);
  expect(program.getSourceFile("file:///A.java" as Uri)).toBeUndefined();
  expect(program.getOpenUris()).toEqual([]);
});

test("global index resolves types across files by FQN and package", () => {
  const program = createProgram();
  program.setOpenDocument("file:///A.java" as Uri, "package com.app;\nclass A {}", 1);
  program.setOpenDocument("file:///B.java" as Uri, "package com.app;\ninterface B {}", 1);
  program.setOpenDocument("file:///C.java" as Uri, "class C {}", 1); // default package
  const index = program.getGlobalIndex();

  expect(index.getType("com.app.A" as Fqn)?.flags).toBe(SymbolFlags.Class);
  expect(index.getType("com.app.B" as Fqn)?.flags).toBe(SymbolFlags.Interface);
  expect(index.getType("C" as Fqn)?.flags).toBe(SymbolFlags.Class);

  const pkg = index.getPackageTypes("com.app" as PackageName)!;
  expect([...pkg.keys()].sort()).toEqual(["A", "B"]);
  expect(index.getPackageSymbol("com.app" as PackageName)?.flags).toBe(SymbolFlags.Package);
  // top-level type's parent is the package symbol
  expect(index.getType("com.app.A" as Fqn)?.parent).toBe(
    index.getPackageSymbol("com.app" as PackageName),
  );
});

test("index rebuilds when a document changes", () => {
  const program = createProgram();
  program.setOpenDocument("file:///A.java" as Uri, "package p;\nclass A {}", 1);
  expect(program.getGlobalIndex().getType("p.A" as Fqn)).toBeDefined();
  program.setOpenDocument("file:///A.java" as Uri, "package p;\nclass Renamed {}", 2);
  const index = program.getGlobalIndex();
  expect(index.getType("p.A" as Fqn)).toBeUndefined();
  expect(index.getType("p.Renamed" as Fqn)).toBeDefined();
});

test("changing one file re-binds only that file (others keep their SourceFile)", () => {
  const program = createProgram();
  program.setOpenDocument("file:///A.java" as Uri, "class A {}", 1);
  program.setOpenDocument("file:///B.java" as Uri, "class B {}", 1);
  program.getGlobalIndex();
  const aBefore = program.getSourceFile("file:///A.java" as Uri);

  program.setOpenDocument("file:///B.java" as Uri, "class B2 {}", 2);
  const index = program.getGlobalIndex();
  expect(index.getType("B" as Fqn)).toBeUndefined();
  expect(index.getType("B2" as Fqn)).toBeDefined();
  // The untouched file's bound SourceFile is reused - no re-bind on reindex.
  expect(program.getSourceFile("file:///A.java" as Uri)).toBe(aBefore);
  expect(index.getType("A" as Fqn)).toBeDefined();
});

test("closing a document removes only its types from the index", () => {
  const program = createProgram();
  program.addProjectFile("file:///A.java" as Uri, "package p;\nclass A {}");
  program.setOpenDocument("file:///B.java" as Uri, "package p;\nclass B {}", 1);
  expect(program.getGlobalIndex().getType("p.B" as Fqn)).toBeDefined();

  program.closeDocument("file:///B.java" as Uri);
  const index = program.getGlobalIndex();
  expect(index.getType("p.B" as Fqn)).toBeUndefined();
  expect(index.getType("p.A" as Fqn)).toBeDefined();
});

test("removing a project file drops its types; an open document for it survives", () => {
  const program = createProgram();
  program.addProjectFile("file:///A.java" as Uri, "package p;\nclass A {}");
  program.addProjectFile("file:///B.java" as Uri, "package p;\nclass B {}");
  expect(program.getGlobalIndex().getType("p.A" as Fqn)).toBeDefined();

  program.removeProjectFile("file:///A.java" as Uri);
  const index = program.getGlobalIndex();
  expect(index.getType("p.A" as Fqn)).toBeUndefined();
  expect(index.getType("p.B" as Fqn)).toBeDefined();
  expect(program.getSourceFile("file:///A.java" as Uri)).toBeUndefined();

  // A file that is also open as a document keeps resolving from the editor copy.
  program.setOpenDocument("file:///B.java" as Uri, "package p;\nclass B { int x; }", 2);
  program.removeProjectFile("file:///B.java" as Uri);
  expect(program.getGlobalIndex().getType("p.B" as Fqn)).toBeDefined();
});
