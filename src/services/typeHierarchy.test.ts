import { test } from "node:test";

import { expect } from "expect";

import { createChecker } from "../compiler/checker.ts";
import { loadJdkStub } from "../compiler/jdkStub.ts";
import { createProgram } from "../compiler/program.ts";
import { type Uri } from "../workspace.ts";
import {
  prepareTypeHierarchy,
  typeHierarchySubtypes,
  typeHierarchySupertypes,
} from "./typeHierarchy.ts";

const SRC = [
  "interface Shape {}",
  "class Base implements Shape {}",
  "class Mid extends Base {}",
  "class Leaf extends Mid {}",
].join("\n");

function setup(text: string) {
  const program = createProgram();
  loadJdkStub(program);
  program.setOpenDocument("file:///S.java" as Uri, text, 1);
  return {
    program,
    checker: createChecker(program),
    sourceFile: program.getSourceFile("file:///S.java" as Uri)!,
  };
}

test("prepare resolves the type at the cursor", () => {
  const { checker, sourceFile } = setup(SRC);
  const items = prepareTypeHierarchy(checker, sourceFile, SRC.indexOf("Mid extends"));
  expect(items?.map(i => i.name)).toEqual(["Mid"]);
});

test("supertypes and subtypes return the direct neighbours", () => {
  const { program, checker, sourceFile } = setup(SRC);
  const [mid] = prepareTypeHierarchy(checker, sourceFile, SRC.indexOf("Mid extends"))!;
  expect(typeHierarchySupertypes(program, checker, mid!)?.map(i => i.name)).toEqual(["Base"]);
  expect(typeHierarchySubtypes(program, checker, mid!)?.map(i => i.name)).toEqual(["Leaf"]);
});

test("supertypes of an interface implementor includes the interface", () => {
  const { program, checker, sourceFile } = setup(SRC);
  const [base] = prepareTypeHierarchy(checker, sourceFile, SRC.indexOf("Base implements"))!;
  // Base implements Shape (and implicitly extends Object).
  expect(typeHierarchySupertypes(program, checker, base!)?.map(i => i.name)).toContain("Shape");
});

test("prepare returns null when the cursor is not on a type", () => {
  const { checker, sourceFile } = setup(SRC);
  expect(prepareTypeHierarchy(checker, sourceFile, 0)).toBeNull();
});
