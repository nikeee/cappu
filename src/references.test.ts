import { test } from "node:test";
import { expect } from "expect";

import { createChecker } from "./checker.ts";
import { getIdentifierAtPosition } from "./nodeAtPosition.ts";
import { createProgram } from "./program.ts";
import { findReferences } from "./resolver.ts";
import { type Identifier } from "./types.ts";

function setup(files: Record<string, string>) {
  const program = createProgram();
  for (const [uri, text] of Object.entries(files)) program.setOpenDocument(uri, text, 1);
  return { program, checker: createChecker(program) };
}

function symbolAt(ctx: ReturnType<typeof setup>, uri: string, needle: string, occ = 1) {
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
  const aValue = symbolAt(ctx, "file:///A.java", "value");
  const refs = findReferences(aValue, ctx.program, ctx.checker.resolveName);
  // A's declaration name + the `a.value` member access (not B's field, not bare `value`)
  expect(refs.length).toBe(2);
});

test("the default lexical resolver misses the member access", () => {
  const ctx = setup(TWO_FIELDS);
  const aValue = symbolAt(ctx, "file:///A.java", "value");
  // Without the checker, `a.value` is not resolved as A.value, so only the
  // declaration is found - which is why rename must use the checker.
  expect(findReferences(aValue, ctx.program).length).toBe(1);
});

test("renaming B's field covers its declaration and the bare use, not a.value", () => {
  const ctx = setup(TWO_FIELDS);
  const bValue = symbolAt(ctx, "file:///B.java", "value");
  const refs = findReferences(bValue, ctx.program, ctx.checker.resolveName);
  expect(refs.length).toBe(2); // declaration + `int y = value;`
});

test("renaming a local stays within its method and matches every use", () => {
  const ctx = setup({
    "file:///C.java": "class C { void m() { int count = 1; count = count + 1; use(count); } }",
  });
  const local = symbolAt(ctx, "file:///C.java", "count");
  const refs = findReferences(local, ctx.program, ctx.checker.resolveName);
  expect(refs.length).toBe(4); // declaration + three uses
});

test("renaming a parameter covers its declaration and uses within the method only", () => {
  const ctx = setup({
    "file:///P.java":
      "class P { int f(int amount) { return amount + amount; } int g(int amount) { return amount; } }",
  });
  const param = symbolAt(ctx, "file:///P.java", "amount"); // f's parameter
  const refs = findReferences(param, ctx.program, ctx.checker.resolveName);
  expect(refs.length).toBe(3); // declaration + two uses in f, not g's amount
});

test("renaming a method matches its qualified call site", () => {
  const ctx = setup({
    "file:///D.java": "class D { void run() {} void m(D d) { d.run(); } }",
  });
  const run = symbolAt(ctx, "file:///D.java", "run");
  const refs = findReferences(run, ctx.program, ctx.checker.resolveName);
  expect(refs.length).toBe(2); // declaration + d.run()
});
