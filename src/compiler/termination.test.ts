// Termination guarantees: inputs that once could (or conceivably could) make
// the pipeline loop forever. Each case runs parse -> bind -> check -> emit ->
// subtype index -> code lenses -> completions; the assertion is that it
// completes at all (a regression here hangs the runner) and inside a generous
// budget that documents the intent. The classfileReader counterpart (corrupt
// jar entries) lives in classfileReader.test.ts.

import { test } from "node:test";

import { expect } from "expect";

import { bindSourceFile } from "./binder.ts";
import { createChecker } from "./checker.ts";
import { emitSourceFile } from "./emitter.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { parseSourceFile } from "./parser.ts";
import { createProgram } from "./program.ts";
import { getCodeLenses } from "../services/codeLens.ts";
import { getCompletions } from "../services/completions.ts";
import { getSubtypeIndex } from "../services/subtypes.ts";
import { type Uri } from "../workspace.ts";

const CASE_BUDGET_MS = 10_000; // generous for CI; a healthy run takes ~100ms

const CONSTELLATIONS: Record<string, string> = {
  "self extends": "class A extends A { void m() { m(); } }",
  "two-class extends cycle":
    "class A extends B { int x; } class B extends A { void m() { x = 1; } }",
  "interface extends cycle":
    "interface I extends J {} interface J extends I {} class C implements I { }",
  "type-parameter bound cycle":
    "class C<T extends U, U extends T> { T f; U m(T t) { return null; } }",
  "f-bounded self type": "class C<T extends C<T>> { T self; void m(C<?> c) { } }",
  "assignability over a cyclic hierarchy":
    "class A extends B {} class B extends A {} class C { void m(A a, B b) { a = b; b = a; boolean t = a instanceof B; } }",
  "constructor chain over a cyclic hierarchy":
    "class A extends B { A() { super(); } } class B extends A {} class C { void m() { new A(); } }",
  "for-each over a cyclic Iterable":
    "class A extends B implements Iterable<String> {} class B extends A {} class C { void m(A a) { for (String s : a) {} } }",
  "member lookup over a cyclic hierarchy":
    "class A extends B {} class B extends A { void go() {} } class C { void m(A a) { a.go(); } }",
  "implementations lens over an interface cycle":
    "interface I extends J { void m(); } interface J extends I {} class K implements I { public void m() {} }",
  "nested classes extending their outers": "class A { class B extends A { class C extends B {} } }",
};

for (const [name, source] of Object.entries(CONSTELLATIONS)) {
  test(`terminates: ${name}`, () => {
    const started = Date.now();
    const program = createProgram();
    loadJdkStub(program);
    program.addProjectFile("file:///T.java" as Uri, source);
    const sourceFile = program.getSourceFile("file:///T.java" as Uri)!;
    const checker = createChecker(program);
    checker.getSemanticDiagnostics(sourceFile);
    try {
      emitSourceFile(sourceFile, program, checker);
    } catch {
      // degrading on a malformed hierarchy is fine; not terminating is not
    }
    getSubtypeIndex(program);
    getCodeLenses(program, checker, sourceFile);
    getCompletions(program, checker, sourceFile, Math.floor(source.length / 2));
    expect(Date.now() - started).toBeLessThan(CASE_BUDGET_MS);
  });
}

// Deterministic mutation fuzz over parse+bind: the parser's error recovery
// must always make progress, whatever fragment lands in front of it.
test("terminates: parser+binder under mutation fuzz", () => {
  const seeds = [
    "class A<T extends Comparable<? super T>> { int m(int[] a, String... s) { for (;;) { switch (a[0]) { case 1 -> m(a, s); default -> { yield; } } } } }",
    "record R(int a, String b) implements I { R { a = 1; } }",
    'class B { String s = """\n  text block\n  """; char c = \'\\u0041\'; }',
    "sealed interface I permits A, B {} non-sealed class A implements I {}",
  ];
  const fragments = [
    "",
    "@",
    "<<<<<<<",
    "class A { void m( { } }",
    "/*",
    '"',
    '"""',
    'class A { String s = "',
    "<T extends <T extends <T",
    "class A { { { { { ",
    ")))))",
    "}}}}}}",
    "enum E { A B C }",
  ];
  const PARSE_BUDGET_MS = 1_000; // a healthy parse of these sizes is <5ms
  const INSERT = "{}()<>;,@\"'`\\";
  let rng = 0x9e3779b9;
  const rand = (): number => (rng = (rng * 1103515245 + 12345) >>> 0) / 2 ** 32;

  const check = (text: string): void => {
    const started = Date.now();
    bindSourceFile(parseSourceFile("f.java", text));
    expect(Date.now() - started).toBeLessThan(PARSE_BUDGET_MS);
  };

  for (const fragment of fragments) check(fragment);
  for (const seed of seeds) {
    check(seed);
    for (let i = 0; i < 800; i++) {
      const chars = [...seed];
      const edits = 1 + Math.floor(rand() * 4);
      for (let e = 0; e < edits; e++) {
        const at = Math.floor(rand() * chars.length);
        const op = rand();
        if (op < 0.4) chars.splice(at, 1);
        else if (op < 0.8) chars[at] = String.fromCharCode(32 + Math.floor(rand() * 95));
        else chars.splice(at, 0, INSERT.charAt(Math.floor(rand() * INSERT.length)));
      }
      check(chars.join(""));
    }
  }
});
