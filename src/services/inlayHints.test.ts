import { test } from "node:test";

import { expect } from "expect";

import { createChecker } from "../compiler/checker.ts";
import { DEFAULT_INLAY_HINTS, getInlayHints, type InlayHintsSettings } from "./inlayHints.ts";
import { loadJdkStub } from "../compiler/jdkStub.ts";
import { createProgram } from "../compiler/program.ts";
import { type Uri } from "../workspace.ts";

function hints(source: string, settings?: InlayHintsSettings) {
  const program = createProgram();
  loadJdkStub(program);
  program.setOpenDocument("file:///T.java" as Uri, source, 1);
  const checker = createChecker(program);
  const sourceFile = program.getSourceFile("file:///T.java" as Uri)!;
  return getInlayHints(checker, sourceFile, 0, source.length, settings).map(h => ({
    label: h.label,
    kind: h.kind,
    // The token the hint is attached to (start of an argument / end of a name).
    at: source.slice(h.offset, h.offset + 6),
  }));
}

test("parameter hints appear for literal and expression arguments only", () => {
  const out = hints(
    [
      "class C {",
      "  static int clamp(int value, int low, int high) { return value; }",
      "  void m(int x) {",
      "    int limit = 9;",
      "    clamp(x, 1 + 2, limit);", // x: plain var (skip), 1+2: expr (hint), limit: plain var (skip)
      "    clamp(5, x, compute());", // 5: literal (hint), x: skip, call: hint
      "  }",
      "  int compute() { return 0; }",
      "}",
    ].join("\n"),
  );
  const params = out.filter(h => h.kind === "parameter");
  expect(params.map(h => h.label)).toEqual(["low:", "value:", "high:"]);
  expect(params[0]!.at.startsWith("1 + 2")).toBe(true);
  expect(params[1]!.at.startsWith("5")).toBe(true);
  expect(params[2]!.at.startsWith("comput")).toBe(true);
});

test("varargs arguments hint only the first of the tail", () => {
  const out = hints(
    [
      "class C {",
      "  static int sum(int... xs) { return 0; }",
      "  void m() { sum(1, 2, 3); }",
      "}",
    ].join("\n"),
  );
  expect(out.filter(h => h.kind === "parameter").map(h => h.label)).toEqual(["...xs:"]);
});

test("var declarations and for-each get inferred type hints", () => {
  const out = hints(
    [
      "import java.util.List;",
      "class C {",
      "  void m(List<String> xs) {",
      '    var s = "hi";',
      "    var n = 1 + 2;",
      "    for (var item : xs) { use(item); }",
      "  }",
      "  void use(String s) {}",
      "}",
    ].join("\n"),
  );
  const types = out.filter(h => h.kind === "type");
  expect(types.map(h => h.label)).toEqual([": String", ": int", ": String"]);
});

test("settings disable each hint family independently", () => {
  const source = [
    "class C {",
    "  static int twice(int value) { return value * 2; }",
    "  void m() { var n = twice(21); }",
    "}",
  ].join("\n");
  expect(
    hints(source)
      .map(h => h.kind)
      .sort(),
  ).toEqual(["parameter", "type"]);
  expect(hints(source, { ...DEFAULT_INLAY_HINTS, parameterNames: false }).map(h => h.kind)).toEqual(
    ["type"],
  );
  expect(hints(source, { ...DEFAULT_INLAY_HINTS, varTypes: false }).map(h => h.kind)).toEqual([
    "parameter",
  ]);
});
