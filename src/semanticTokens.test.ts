import { test } from "node:test";

import { expect } from "expect";

import { createChecker } from "./checker.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { createProgram } from "./program.ts";
import { getSemanticTokens, TOKEN_MODIFIERS, TOKEN_TYPES } from "./semanticTokens.ts";
import { type Uri } from "./workspace.ts";

function tokens(source: string) {
  const program = createProgram();
  loadJdkStub(program);
  program.setOpenDocument("file:///T.java" as Uri, source, 1);
  const checker = createChecker(program);
  const sourceFile = program.getSourceFile("file:///T.java" as Uri)!;
  return getSemanticTokens(checker, sourceFile).map(t => ({
    text: source.slice(t.offset, t.offset + t.length),
    type: TOKEN_TYPES[t.tokenType]!,
    mods: TOKEN_MODIFIERS.filter((_, i) => (t.tokenModifiers & (1 << i)) !== 0),
  }));
}

test("identifiers classify by symbol kind", () => {
  const out = tokens(
    [
      "class Pet<T> {",
      "  static final int LEGS = 4;",
      "  String name;",
      "  T tag;",
      "  String describe(int extra) {",
      "    int local = LEGS + extra;",
      "    return name + local;",
      "  }",
      "}",
      "enum Color { RED }",
    ].join("\n"),
  );
  const byText = new Map(out.map(t => [t.text, t]));
  expect(byText.get("Pet")).toMatchObject({ type: "class", mods: ["declaration"] });
  expect(byText.get("T")!.type).toBe("typeParameter");
  expect(byText.get("LEGS")!.type).toBe("property");
  expect(byText.get("name")!.type).toBe("property");
  expect(byText.get("extra")!.type).toBe("parameter");
  expect(byText.get("local")!.type).toBe("variable");
  expect(byText.get("describe")).toMatchObject({ type: "method", mods: ["declaration"] });
  expect(byText.get("Color")!.type).toBe("enum");
  expect(byText.get("RED")).toMatchObject({ type: "enumMember" });
  expect(byText.get("RED")!.mods).toEqual(expect.arrayContaining(["static", "readonly"]));
});

test("static/final modifiers and jdk default-library are flagged", () => {
  const out = tokens(
    [
      "class C {",
      "  static final int MAX = 9;",
      "  void m() {",
      "    String s = String.valueOf(MAX);",
      "  }",
      "}",
    ].join("\n"),
  );
  const maxUse = out.filter(t => t.text === "MAX");
  for (const t of maxUse) {
    expect(t.mods).toEqual(expect.arrayContaining(["static", "readonly"]));
  }
  const stringTokens = out.filter(t => t.text === "String");
  expect(stringTokens.length).toBeGreaterThan(0);
  for (const t of stringTokens) {
    expect(t.type).toBe("class");
    expect(t.mods).toContain("defaultLibrary");
  }
  const valueOf = out.find(t => t.text === "valueOf");
  expect(valueOf).toMatchObject({ type: "method" });
  expect(valueOf!.mods).toEqual(expect.arrayContaining(["static", "defaultLibrary"]));
});

test("entries are sorted and cover only resolved identifiers", () => {
  const out = tokens("class C { void m() { unknownThing(); int x = 1; } }");
  expect(out.some(t => t.text === "unknownThing")).toBe(false);
  expect(out.some(t => t.text === "x")).toBe(true);
});
