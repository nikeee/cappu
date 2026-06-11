import { test } from "node:test";

import { expect } from "expect";

import { createChecker } from "../compiler/checker.ts";
import { getCodeLenses } from "./codeLens.ts";
import { loadJdkStub } from "../compiler/jdkStub.ts";
import { createProgram } from "../compiler/program.ts";
import { type Uri } from "../workspace.ts";

function lenses(files: Record<string, string>, lensFile: string) {
  const program = createProgram();
  loadJdkStub(program);
  for (const [name, text] of Object.entries(files)) {
    program.addProjectFile(`file:///${name}` as Uri, text);
  }
  const checker = createChecker(program);
  const sourceFile = program.getSourceFile(`file:///${lensFile}` as Uri)!;
  return getCodeLenses(program, checker, sourceFile).map(e => ({
    name: e.name.text,
    kind: e.kind,
    count: e.sites.length,
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
  const refs = new Map(out.filter(e => e.kind === "references").map(e => [e.name, e.count]));
  expect(refs.get("Pet")).toBe(2); // two parameter types in Keeper
  expect(refs.get("legs")).toBe(2); // two calls
  expect(refs.get("unused")).toBe(0); // the declaration itself never counts
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
  const refs = new Map(out.filter(e => e.kind === "references").map(e => [e.name, e.count]));
  expect(refs.get("twice")).toBe(2);
  expect(refs.get("m")).toBe(0);
});

test("interfaces and their abstract methods count implementations", () => {
  const out = lenses(
    {
      "Shape.java": [
        "package geo;",
        "public interface Shape {",
        "  double area();",
        '  default String label() { return "shape"; }',
        "}",
      ].join("\n"),
      "Impls.java": [
        "package geo;",
        "class Circle implements Shape { public double area() { return 3.14; } }",
        "class Square implements Shape { public double area() { return 1.0; } }",
        "interface Polygon extends Shape {}",
      ].join("\n"),
    },
    "Shape.java",
  );
  const impls = new Map(out.filter(e => e.kind === "implementations").map(e => [e.name, e.count]));
  expect(impls.get("Shape")).toBe(3); // Circle, Square, Polygon
  expect(impls.get("area")).toBe(2); // the two concrete bodies
  expect(impls.has("label")).toBe(false); // default methods get no implementations lens
});

test("abstract classes count subclasses and abstract-method overrides", () => {
  const out = lenses(
    {
      "Base.java": [
        "abstract class Base {",
        "  abstract int weight();",
        "  int common() { return 0; }",
        "}",
        "class Heavy extends Base { int weight() { return 100; } }",
      ].join("\n"),
    },
    "Base.java",
  );
  const impls = new Map(out.filter(e => e.kind === "implementations").map(e => [e.name, e.count]));
  expect(impls.get("Base")).toBe(1);
  expect(impls.get("weight")).toBe(1);
  expect(impls.has("common")).toBe(false); // concrete methods get no implementations lens
});

test("implementation counts are transitive through intermediate types", () => {
  const out = lenses(
    {
      "I.java": "interface I { int f(); }",
      "Mid.java": "abstract class Mid implements I {}",
      "Leaf.java": "class Leaf extends Mid { public int f() { return 1; } }",
    },
    "I.java",
  );
  const impls = new Map(out.filter(e => e.kind === "implementations").map(e => [e.name, e.count]));
  expect(impls.get("I")).toBe(2); // Mid (direct) and Leaf (via Mid)
  expect(impls.get("f")).toBe(1); // Leaf's concrete body
});
