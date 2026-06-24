import { test } from "node:test";

import { expect } from "expect";

import { createChecker } from "../compiler/checker.ts";
import { loadJdkStub } from "../compiler/jdkStub.ts";
import { createProgram } from "../compiler/program.ts";
import { type Uri } from "../workspace.ts";
import {
  callHierarchyIncoming,
  callHierarchyOutgoing,
  prepareCallHierarchy,
} from "./callHierarchy.ts";

const SRC = [
  "class C {",
  "  int target() { return 1; }",
  "  int caller() { return target() + target(); }",
  "  int other() { return caller(); }",
  "}",
].join("\n");

function setup(text: string) {
  const program = createProgram();
  loadJdkStub(program);
  program.setOpenDocument("file:///C.java" as Uri, text, 1);
  return { program, checker: createChecker(program), sourceFile: program.getSourceFile("file:///C.java" as Uri)! };
}

test("prepare resolves the method at the cursor", () => {
  const { checker, sourceFile } = setup(SRC);
  const items = prepareCallHierarchy(checker, sourceFile, SRC.indexOf("target() { return 1"));
  expect(items?.map(i => i.name)).toEqual(["target"]);
});

test("incoming calls group the call sites by their enclosing method", () => {
  const { program, checker, sourceFile } = setup(SRC);
  const [target] = prepareCallHierarchy(checker, sourceFile, SRC.indexOf("target() { return 1"))!;
  const incoming = callHierarchyIncoming(program, checker, target!);
  expect(incoming?.map(c => c.from.name)).toEqual(["caller"]);
  // caller() calls target() twice.
  expect(incoming?.[0]!.fromRanges).toHaveLength(2);
});

test("outgoing calls list the callees of a method", () => {
  const { program, checker, sourceFile } = setup(SRC);
  const [caller] = prepareCallHierarchy(checker, sourceFile, SRC.indexOf("caller() { return target"))!;
  const outgoing = callHierarchyOutgoing(program, checker, caller!);
  expect(outgoing?.map(c => c.to.name)).toEqual(["target"]);
  expect(outgoing?.[0]!.fromRanges).toHaveLength(2);
});

test("prepare returns null off any method", () => {
  const { checker, sourceFile } = setup(SRC);
  expect(prepareCallHierarchy(checker, sourceFile, 0)).toBeNull();
});
