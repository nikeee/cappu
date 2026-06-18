import { test } from "node:test";

import { expect } from "expect";

import { getIdentifierAtPosition } from "../services/nodeAtPosition.ts";
import { createProgram } from "./program.ts";
import { resolveIdentifier } from "./resolver.ts";
import { type Identifier, type Symbol, SymbolFlags } from "./types.ts";

// Resolve the name at the nth occurrence of `needle` in `text`.
function resolveAt(text: string, needle: string, occurrence = 1): Symbol | undefined {
  const program = createProgram();
  program.setOpenDocument("file:///T.java" as Uri, text, 1);
  const sf = program.getSourceFile("file:///T.java" as Uri)!;
  let offset = -1;
  for (let i = 0; i < occurrence; i++) offset = text.indexOf(needle, offset + 1);
  const id = getIdentifierAtPosition(sf, offset);
  return id ? resolveIdentifier(id as Identifier, program) : undefined;
}

test("local variable use resolves to its declaration", () => {
  const sym = resolveAt("class C { void m() { int x = 1; return x; } }", "x", 2);
  expect(sym?.flags).toBe(SymbolFlags.LocalVariable);
  expect(sym?.escapedName).toBe("x");
});

test("parameter use resolves to the parameter", () => {
  const sym = resolveAt("class C { int m(int a) { return a; } }", "a", 2);
  expect(sym?.flags).toBe(SymbolFlags.Parameter);
});

test("field use resolves to the field", () => {
  const sym = resolveAt("class C { int f; void m() { f = 1; } }", "f", 2);
  expect(sym?.flags).toBe(SymbolFlags.Field);
});

test("a local shadows a field of the same name", () => {
  const sym = resolveAt("class C { int x; void m() { int x = 1; return x; } }", "x", 3);
  expect(sym?.flags).toBe(SymbolFlags.LocalVariable);
});

test("type reference resolves to a file-local type (declared later)", () => {
  const sym = resolveAt("class C extends Base {}\nclass Base {}", "Base", 1);
  expect(sym?.flags).toBe(SymbolFlags.Class);
  expect(sym?.escapedName).toBe("Base");
});

test("type parameter use resolves to the type parameter", () => {
  const sym = resolveAt("class C<T> { T get() { return null; } }", "T", 2);
  expect(sym?.flags).toBe(SymbolFlags.TypeParameter);
});

test("a method call name resolves to the method", () => {
  const sym = resolveAt(
    "class C { void m() { helper(); } int helper() { return 0; } }",
    "helper",
    1,
  );
  expect(sym?.flags).toBe(SymbolFlags.Method);
});

test("clicking a declaration name resolves to itself", () => {
  const sym = resolveAt("class C { int field; }", "field", 1);
  expect(sym?.flags).toBe(SymbolFlags.Field);
  expect(sym?.escapedName).toBe("field");
});

test("an unresolved name returns undefined", () => {
  expect(resolveAt("class C { void m() { unknownThing(); } }", "unknownThing", 1)).toBeUndefined();
});

// P3: cross-file resolution, inheritance, find-references

import { createProgram as _cp } from "./program.ts";
import { findReferences } from "./resolver.ts";
import type { Program } from "./program.ts";
import { type Uri } from "../workspace.ts";
import type { Fqn } from "./program.ts";

function programOf(files: Record<string, string>): Program {
  const program = _cp();
  for (const [uri, text] of Object.entries(files)) program.setOpenDocument(uri as Uri, text, 1);
  return program;
}

function resolveInFile(
  program: Program,
  uri: Uri,
  needle: string,
  occurrence = 1,
): Symbol | undefined {
  const sf = program.getSourceFile(uri)!;
  let offset = -1;
  for (let i = 0; i < occurrence; i++) offset = sf.text.indexOf(needle, offset + 1);
  const id = getIdentifierAtPosition(sf, offset);
  return id ? resolveIdentifier(id as Identifier, program) : undefined;
}

test("same-package type resolves across files", () => {
  const program = programOf({
    "file:///A.java": "package p;\nclass A extends B {}",
    "file:///B.java": "package p;\nclass B {}",
  });
  const sym = resolveInFile(program, "file:///A.java" as Uri, "B");
  expect(sym?.flags).toBe(SymbolFlags.Class);
  expect(sym).toBe(program.getGlobalIndex().getType("p.B" as Fqn));
});

test("single-type import resolves a type from another package", () => {
  const program = programOf({
    "file:///A.java": "package p;\nimport q.B;\nclass A extends B {}",
    "file:///B.java": "package q;\npublic class B {}",
  });
  expect(resolveInFile(program, "file:///A.java" as Uri, "B", 2)).toBe(
    program.getGlobalIndex().getType("q.B" as Fqn),
  );
});

test("on-demand import resolves a type", () => {
  const program = programOf({
    "file:///A.java": "package p;\nimport q.*;\nclass A extends B {}",
    "file:///B.java": "package q;\npublic class B {}",
  });
  expect(resolveInFile(program, "file:///A.java" as Uri, "B", 1)).toBe(
    program.getGlobalIndex().getType("q.B" as Fqn),
  );
});

test("fully-qualified type name resolves via the global index", () => {
  const program = programOf({
    "file:///A.java": "package p;\nclass A extends q.B {}",
    "file:///B.java": "package q;\npublic class B {}",
  });
  // click the 'B' tail of q.B
  expect(resolveInFile(program, "file:///A.java" as Uri, "B", 1)).toBe(
    program.getGlobalIndex().getType("q.B" as Fqn),
  );
});

test("inherited field resolves to the super class member", () => {
  const program = programOf({
    "file:///Base.java": "package p;\nclass Base { int f; }",
    "file:///Sub.java": "package p;\nclass Sub extends Base { void m() { f = 1; } }",
  });
  const sym = resolveInFile(program, "file:///Sub.java" as Uri, "f", 1);
  expect(sym?.flags).toBe(SymbolFlags.Field);
  expect(sym).toBe(
    program
      .getGlobalIndex()
      .getType("p.Base" as Fqn)!
      .members!.get("f"),
  );
});

test("findReferences returns the declaration and all uses", () => {
  const program = programOf({ "file:///C.java": "class C { int x; void m() { x = x + 1; } }" });
  const sym = resolveInFile(program, "file:///C.java" as Uri, "x", 2); // a use
  const refs = findReferences(sym!, program);
  expect(refs.length).toBe(3); // declaration + 2 uses
});

test("findReferences for a local variable stays within its file", () => {
  const program = programOf({
    "file:///A.java": "package p;\nclass A { void m() { int local = 1; use(local); } }",
    // a same-named local in another file must not be picked up
    "file:///B.java": "package p;\nclass B { void m() { int local = 2; use(local); } }",
  });
  const sym = resolveInFile(program, "file:///A.java" as Uri, "local", 1);
  expect(sym?.flags).toBe(SymbolFlags.LocalVariable);
  const refs = findReferences(sym!, program);
  expect(refs.length).toBe(2); // declaration + 1 use, only in A.java
  expect(refs.every(r => r.parent && true)).toBe(true);
});

test("findReferences for a cross-file type spans the workspace", () => {
  const program = programOf({
    "file:///Base.java": "package p;\nclass Base {}",
    "file:///A.java": "package p;\nclass A extends Base {}",
    "file:///B.java": "package p;\nclass B extends Base {}",
  });
  const sym = program.getGlobalIndex().getType("p.Base" as Fqn)!;
  const refs = findReferences(sym, program);
  // declaration name in Base.java + the two extends uses
  expect(refs.length).toBe(3);
});
