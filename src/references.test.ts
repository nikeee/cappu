import { test } from "node:test";

import { expect } from "expect";

import { createChecker } from "./checker.ts";
import { getIdentifierAtPosition } from "./nodeAtPosition.ts";
import { createProgram } from "./program.ts";
import { findReferences, getSourceFileOfNode } from "./resolver.ts";
import { type Identifier } from "./types.ts";
import { type Uri } from "./workspace.ts";

function setup(files: Record<string, string>) {
  const program = createProgram();
  for (const [uri, text] of Object.entries(files)) program.setOpenDocument(uri as Uri, text, 1);
  return { program, checker: createChecker(program) };
}

function symbolAt(ctx: ReturnType<typeof setup>, uri: Uri, needle: string, occ = 1) {
  const sf = ctx.program.getSourceFile(uri)!;
  let offset = -1;
  for (let i = 0; i < occ; i++) offset = sf.text.indexOf(needle, offset + 1);
  return ctx.checker.resolveName(getIdentifierAtPosition(sf, offset) as Identifier)!;
}

// Two distinct fields named `value`; only A's is the rename target.
const TWO_FIELDS = {
  "file:///A.java": "class A { int value; }",
  "file:///B.java": "class B { int value; void m(A a) { int x = a.value; int y = value; } }",
};

test("findReferences with the checker matches member accesses (a.value)", () => {
  const ctx = setup(TWO_FIELDS);
  const aValue = symbolAt(ctx, "file:///A.java" as Uri, "value");
  const refs = findReferences(aValue, ctx.program, ctx.checker.resolveName);
  // A's declaration name + the `a.value` member access (not B's field, not bare `value`)
  expect(refs.length).toBe(2);
});

test("the default lexical resolver misses the member access", () => {
  const ctx = setup(TWO_FIELDS);
  const aValue = symbolAt(ctx, "file:///A.java" as Uri, "value");
  // Without the checker, `a.value` is not resolved as A.value, so only the
  // declaration is found - which is why rename must use the checker.
  expect(findReferences(aValue, ctx.program).length).toBe(1);
});

test("renaming B's field covers its declaration and the bare use, not a.value", () => {
  const ctx = setup(TWO_FIELDS);
  const bValue = symbolAt(ctx, "file:///B.java" as Uri, "value");
  const refs = findReferences(bValue, ctx.program, ctx.checker.resolveName);
  expect(refs.length).toBe(2); // declaration + `int y = value;`
});

test("renaming a local stays within its method and matches every use", () => {
  const ctx = setup({
    "file:///C.java": "class C { void m() { int count = 1; count = count + 1; use(count); } }",
  });
  const local = symbolAt(ctx, "file:///C.java" as Uri, "count");
  const refs = findReferences(local, ctx.program, ctx.checker.resolveName);
  expect(refs.length).toBe(4); // declaration + three uses
});

test("renaming a parameter covers its declaration and uses within the method only", () => {
  const ctx = setup({
    "file:///P.java":
      "class P { int f(int amount) { return amount + amount; } int g(int amount) { return amount; } }",
  });
  const param = symbolAt(ctx, "file:///P.java" as Uri, "amount"); // f's parameter
  const refs = findReferences(param, ctx.program, ctx.checker.resolveName);
  expect(refs.length).toBe(3); // declaration + two uses in f, not g's amount
});

test("renaming a method matches its qualified call site", () => {
  const ctx = setup({
    "file:///D.java": "class D { void run() {} void m(D d) { d.run(); } }",
  });
  const run = symbolAt(ctx, "file:///D.java" as Uri, "run");
  const refs = findReferences(run, ctx.program, ctx.checker.resolveName);
  expect(refs.length).toBe(2); // declaration + d.run()
});

// Rename is findReferences + an edit per occurrence (see server.onRenameRequest).
// These stress the cross-file shape the WorkspaceEdit is built from.
function renameEdits(
  ctx: ReturnType<typeof setup>,
  uri: Uri,
  needle: string,
  occ = 1,
): Map<string, number> {
  const symbol = symbolAt(ctx, uri, needle, occ);
  const perFile = new Map<string, number>();
  for (const node of findReferences(symbol, ctx.program, ctx.checker.resolveName)) {
    const file = getSourceFileOfNode(node).fileName;
    perFile.set(file, (perFile.get(file) ?? 0) + 1);
  }
  return perFile;
}

test("renaming a class hits its declaration and every cross-file use", () => {
  const ctx = setup({
    "file:///P.java": "class P { }",
    "file:///UseA.java": "class UseA { P p = new P(); P make() { return new P(); } }",
    "file:///UseB.java": "class UseB extends P { }",
  });
  const edits = renameEdits(ctx, "file:///P.java" as Uri, "P");
  expect(edits.get("file:///P.java")).toBe(1); // the declaration
  expect(edits.get("file:///UseA.java")).toBe(4); // field type, two news, return type
  expect(edits.get("file:///UseB.java")).toBe(1); // extends clause
});

test("renaming a method spans files but not same-named methods of other types", () => {
  const ctx = setup({
    "file:///S.java": "class S { void go() { } }",
    "file:///T.java": "class T { void go() { } void m(S s) { s.go(); go(); } }",
  });
  const edits = renameEdits(ctx, "file:///S.java" as Uri, "go");
  expect(edits.get("file:///S.java")).toBe(1); // declaration
  expect(edits.get("file:///T.java")).toBe(1); // s.go() only - the bare go() is T's
});

test("renaming a field used through this, a receiver and statically-typed chains", () => {
  const ctx = setup({
    "file:///H.java": "class H { int count; int bump() { return this.count + count; } }",
    "file:///K.java": "class K { int read(H h) { return h.count; } }",
  });
  const edits = renameEdits(ctx, "file:///H.java" as Uri, "count");
  expect(edits.get("file:///H.java")).toBe(3); // declaration + this.count + count
  expect(edits.get("file:///K.java")).toBe(1); // h.count
});
