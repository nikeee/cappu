import { test } from "node:test";
import { expect } from "expect";

import { createChecker } from "./checker.ts";
import { getCodeLenses } from "./codeLens.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { createProgram } from "./program.ts";

function lenses(files: Record<string, string>, lensFile: string) {
  const program = createProgram();
  loadJdkStub(program);
  for (const [name, text] of Object.entries(files)) {
    program.addProjectFile(`file:///${name}`, text);
  }
  const checker = createChecker(program);
  const sourceFile = program.getSourceFile(`file:///${lensFile}`)!;
  return getCodeLenses(program, checker, sourceFile).map(e => ({
    name: e.name.text,
    count: e.references.length,
  }));
}

test("types and methods get cross-file reference counts", () => {
  const out = lenses(
    {
      "Pet.java": [
        "package zoo;",
        "public class Pet {",
        "  public int legs() { return 4; }",
        "  void unused() {}",
        "}",
      ].join("\n"),
      "Keeper.java": [
        "package zoo;",
        "class Keeper {",
        "  int count(Pet a, Pet b) { return a.legs() + b.legs(); }",
        "}",
      ].join("\n"),
    },
    "Pet.java",
  );
  const byName = new Map(out.map(e => [e.name, e.count]));
  expect(byName.get("Pet")).toBe(2); // two parameter types in Keeper
  expect(byName.get("legs")).toBe(2); // two calls
  expect(byName.get("unused")).toBe(0); // the declaration itself never counts
});

test("in-file references count too, declarations excluded", () => {
  const out = lenses(
    {
      "C.java": [
        "class C {",
        "  int twice(int x) { return x * 2; }",
        "  int m() { return twice(1) + twice(2); }",
        "}",
      ].join("\n"),
    },
    "C.java",
  );
  const byName = new Map(out.map(e => [e.name, e.count]));
  expect(byName.get("twice")).toBe(2);
  expect(byName.get("m")).toBe(0);
});
