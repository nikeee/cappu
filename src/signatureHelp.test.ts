import { test } from "node:test";

import { expect } from "expect";

import { createChecker } from "./checker.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { getNodeAtPosition } from "./nodeAtPosition.ts";
import { createProgram } from "./program.ts";
import { type CallExpression, type Node, SyntaxKind } from "./types.ts";
import { type Uri } from "./workspace.ts";

// The checker-side pieces signature help builds on: resolveCallCandidates (every
// overload a call could bind to), parameterLabelsOf, and signatureOfDeclaration.
// The server handler is a thin position-to-call wrapper over these.

function callAtMarker(text: string, marker = "/*|*/") {
  const offset = text.indexOf(marker);
  const clean = text.replace(marker, "");
  const program = createProgram();
  loadJdkStub(program);
  program.setOpenDocument("file:///T.java" as Uri, clean, 1);
  const checker = createChecker(program);
  const sf = program.getSourceFile("file:///T.java" as Uri)!;
  // Mirrors the server's callAt, including the retry one position left for a
  // cursor sitting at the recovered end of an unclosed call.
  for (const at of [offset, offset - 1]) {
    let node: Node | undefined = getNodeAtPosition(sf, at);
    for (; node; node = node.parent) {
      if (node.kind !== SyntaxKind.CallExpression) continue;
      const call = node as CallExpression;
      if (offset > call.expression.end && offset <= call.end) return { call, checker };
    }
  }
  throw new Error("no call at marker");
}

test("call candidates list every overload", () => {
  const { call, checker } = callAtMarker(
    "class C { int f(int x){return x;} int f(String s){return 0;} void m(){ f(/*|*/ } }",
  );
  const sigs = checker.resolveCallCandidates(call).map(d => checker.signatureOfDeclaration(d));
  expect(sigs.sort()).toEqual(["int f(String s)", "int f(int x)"]);
});

test("parameter labels are the written parameter texts", () => {
  const { call, checker } = callAtMarker(
    "class C { int add(int first, long second){return 0;} void m(){ add(1,/*|*/ } }",
  );
  const decl = checker.resolveCall(call)!;
  expect(checker.parameterLabelsOf(decl)).toEqual(["int first", "long second"]);
});

test("candidates resolve through a receiver, including inherited overloads", () => {
  const { call, checker } = callAtMarker(
    "class B { void g(int x){} } class D extends B { void g(String s){} void m(){ this.g(/*|*/ } }",
  );
  const labels = checker
    .resolveCallCandidates(call)
    .map(d => checker.signatureOfDeclaration(d))
    .sort();
  expect(labels).toEqual(["void g(String s)", "void g(int x)"]);
});

test("the chosen overload matches the argument types", () => {
  const { call, checker } = callAtMarker(
    'class C { int f(int x){return x;} int f(String s){return 0;} void m(){ f("a"/*|*/) } }',
  );
  const resolved = checker.resolveCall(call)!;
  expect(checker.signatureOfDeclaration(resolved)).toBe("int f(String s)");
});
