import { test } from "node:test";

import { expect } from "expect";

import { JDK_STUB_FILES, loadJdkStub } from "./jdkStub.ts";
import { getIdentifierAtPosition } from "./nodeAtPosition.ts";
import { createProgram } from "./program.ts";
import { lookupMember, Meaning, resolveIdentifier } from "./resolver.ts";
import { type Identifier, SymbolFlags } from "./types.ts";

test("stub files parse without diagnostics", () => {
  const program = createProgram();
  loadJdkStub(program);
  for (const file of JDK_STUB_FILES) {
    expect(program.getSourceFile(file.uri)!.parseDiagnostics).toHaveLength(0);
  }
});

test("stub types are in the global index", () => {
  const program = createProgram();
  loadJdkStub(program);
  const index = program.getGlobalIndex();
  expect(index.getType("java.lang.String")?.flags).toBe(SymbolFlags.Class);
  expect(index.getType("java.lang.Object")?.flags).toBe(SymbolFlags.Class);
  expect(index.getType("java.util.List")?.flags).toBe(SymbolFlags.Interface);
});

test("a user type resolves String via implicit java.lang", () => {
  const program = createProgram();
  loadJdkStub(program);
  program.setOpenDocument("file:///C.java", "class C { String name; }", 1);
  const sf = program.getSourceFile("file:///C.java")!;
  const id = getIdentifierAtPosition(sf, sf.text.indexOf("String"));
  const sym = resolveIdentifier(id as Identifier, program);
  expect(sym).toBe(program.getGlobalIndex().getType("java.lang.String"));
});

test("inherited member is found through the stub hierarchy (List -> Collection)", () => {
  const program = createProgram();
  loadJdkStub(program);
  const list = program.getGlobalIndex().getType("java.util.List")!;
  // size() is declared on Collection, inherited by List
  const size = lookupMember(list, "size", Meaning.Value, program);
  expect(size?.flags).toBe(SymbolFlags.Method);
});
