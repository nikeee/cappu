import { test } from "node:test";
import { expect } from "expect";

import { loadJdkStub } from "./jdkStub.ts";
import { createProgram } from "./program.ts";
import { getSubtypeIndex } from "./subtypes.ts";

test("direct and transitive subtypes resolve across files; memo invalidates on change", () => {
  const program = createProgram();
  loadJdkStub(program);
  program.addProjectFile("file:///I.java", "interface I {}");
  program.addProjectFile("file:///A.java", "class A implements I {}");
  program.addProjectFile("file:///B.java", "class B extends A {}");

  const i = program.getGlobalIndex().getType("I")!;
  const a = program.getGlobalIndex().getType("A")!;
  let index = getSubtypeIndex(program);
  expect(index.directSubtypesOf(i).map(s => s.escapedName)).toEqual(["A"]);
  expect(index.allSubtypesOf(i).map(s => s.escapedName).sort()).toEqual(["A", "B"]);
  expect(index.directSubtypesOf(a).map(s => s.escapedName)).toEqual(["B"]);

  // The memo is generation-keyed: a new subtype shows up after a file change.
  program.addProjectFile("file:///C.java", "class C implements I {}");
  index = getSubtypeIndex(program);
  expect(index.allSubtypesOf(program.getGlobalIndex().getType("I")!).length).toBe(3);
});
