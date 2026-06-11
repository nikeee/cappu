// Cyclic inheritance and cyclic generics - direct and indirect - must never
// loop: these run the CHECKER (diagnostics, symbol types), the RESOLVER
// (identifier resolution, member lookup, references) and HOVER over every
// identifier of each cyclic source. The assertion is termination (a
// regression hangs the runner); the budget documents the intent. The broader
// pipeline counterpart lives in termination.test.ts.

import { test } from "node:test";

import { expect } from "expect";

import { createChecker } from "./checker.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { createProgram } from "./program.ts";
import { findReferences, lookupMember, Meaning, resolveIdentifier } from "./resolver.ts";
import { type Identifier, SymbolFlags, SyntaxKind } from "./types.ts";
import { forEachChild } from "./parser.ts";
import { getHoverText } from "../services/hover.ts";
import { type Uri } from "../workspace.ts";

const BUDGET_MS = 10_000; // generous for CI; healthy runs take ~100ms

const CYCLES: Record<string, string> = {
  // inheritance, direct
  "class extends itself": "class A extends A { int f; void m() { m(); int x = f; } }",
  "two classes extending each other":
    "class A extends B { void onA() {} } class B extends A { void onB() { onA(); } }",
  "interface extending itself":
    "interface I extends I { void m(); } class C implements I { public void m() {} }",
  // inheritance, indirect
  "three-class extends cycle":
    "class A extends B { int a; } class B extends C { int b; } class C extends A { int m() { return a + b; } }",
  "three-interface extends cycle":
    "interface I extends J {} interface J extends K {} interface K extends I { void go(); } class C implements I { public void go() {} }",
  "class/interface mixed cycle":
    "interface I extends J {} interface J extends I {} class A extends B implements I {} class B extends A { void m(A a, I i) { } }",
  // generics, direct
  "type parameter bounded by itself":
    "class G<T extends T> { T value; T id(T t) { return value; } }",
  "f-bounded type parameter": "class G<T extends G<T>> { T self; T next() { return self; } }",
  "generic class extending itself with new arguments":
    "class G<T> extends G<G<T>> { T value; void m(G<String> g) { } }",
  // generics, indirect
  "mutually bounded type parameters":
    "class C<T extends U, U extends T> { T t; U u; void m() { t = u; u = t; } }",
  "mutually f-bounded classes":
    "class X<T extends Y<T>> { T y; } class Y<U extends X<U>> { U x; void m(Y<?> y) { } }",
  "parameterized two-class extends cycle":
    "class P<T> extends Q<T> { T p; } class Q<T> extends P<T> { T q; void m() { p = q; } }",
};

for (const [name, source] of Object.entries(CYCLES)) {
  test(`cycle-safe: ${name}`, () => {
    const started = Date.now();
    const program = createProgram();
    loadJdkStub(program);
    program.addProjectFile("file:///T.java" as Uri, source);
    const sourceFile = program.getSourceFile("file:///T.java" as Uri)!;
    const checker = createChecker(program);

    // checker: all diagnostics over the cyclic declarations
    checker.getSemanticDiagnostics(sourceFile);

    // resolver + checker + hover over EVERY identifier in the file
    const identifiers: Identifier[] = [];
    const collect = (node: import("./types.ts").Node): void => {
      if (node.kind === SyntaxKind.Identifier) identifiers.push(node as Identifier);
      forEachChild(node, child => {
        collect(child);
        return undefined;
      });
    };
    collect(sourceFile);
    expect(identifiers.length).toBeGreaterThan(0);
    for (const identifier of identifiers) {
      resolveIdentifier(identifier, program);
      const symbol = checker.resolveName(identifier);
      if (!symbol) continue;
      checker.getTypeOfSymbol(symbol);
      checker.typeStringOfSymbol(symbol);
      getHoverText(checker, symbol, identifier); // hover renders bounds/signatures
      if (symbol.flags & SymbolFlags.Type) {
        lookupMember(symbol, "toString", Meaning.Any, program);
        lookupMember(symbol, "no_such_member", Meaning.Any, program);
      }
      findReferences(symbol, program, checker.resolveName);
    }

    expect(Date.now() - started).toBeLessThan(BUDGET_MS);
  });
}
