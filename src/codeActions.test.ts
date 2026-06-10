import { test } from "node:test";

import { expect } from "expect";

import { createChecker } from "./checker.ts";
import { getCodeActions, type CodeActionResult } from "./codeActions.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { createProgram } from "./program.ts";

function setup(text: string, extra: Record<string, string> = {}) {
  const program = createProgram();
  loadJdkStub(program);
  for (const [uri, t] of Object.entries(extra)) program.addProjectFile(uri, t);
  program.setOpenDocument("file:///T.java", text, 1);
  return { program, checker: createChecker(program), text };
}

// Apply a single-file action's changes to the source text (offsets are in T.java).
function apply(text: string, action: CodeActionResult): string {
  let out = text;
  for (const c of [...action.changes].sort((a, b) => b.start - a.start)) {
    out = out.slice(0, c.start) + c.newText + out.slice(c.end);
  }
  return out;
}

function actionsAt(ctx: ReturnType<typeof setup>, needle: string, occ = 1) {
  let offset = -1;
  for (let i = 0; i < occ; i++) offset = ctx.text.indexOf(needle, offset + 1);
  return getCodeActions(
    ctx.program,
    ctx.checker,
    ctx.program.getSourceFile("file:///T.java")!,
    offset,
    offset,
  );
}

// --- add missing import ------------------------------------------------------------

test("offers an import for an unresolved type that exists in the index", () => {
  const ctx = setup("package app;\nclass C { java_unused; List<String> xs; }");
  const actions = actionsAt(ctx, "List").filter(a => a.kind === "quickfix");
  expect(actions.map(a => a.title)).toContain("Import 'java.util.List'");
});

test("inserts the import after existing imports", () => {
  const ctx = setup("package app;\n\nimport java.util.Map;\n\nclass C { List<String> xs; }");
  const action = actionsAt(ctx, "List").find(a => a.title === "Import 'java.util.List'")!;
  expect(apply(ctx.text, action)).toBe(
    "package app;\n\nimport java.util.Map;\nimport java.util.List;\n\nclass C { List<String> xs; }",
  );
});

test("inserts the import after the package when there are no imports", () => {
  const ctx = setup("package app;\n\nclass C { List<String> xs; }");
  const action = actionsAt(ctx, "List").find(a => a.title === "Import 'java.util.List'")!;
  expect(apply(ctx.text, action)).toBe(
    "package app;\n\nimport java.util.List;\n\nclass C { List<String> xs; }",
  );
});

test("no import offered for an already-resolved type", () => {
  const ctx = setup("package app;\nimport java.util.List;\nclass C { List<String> xs; }");
  expect(actionsAt(ctx, "List", 2).filter(a => a.kind === "quickfix")).toEqual([]);
});

test("no import offered for a type in the same package", () => {
  const ctx = setup("package app;\nclass C { Helper h; }", {
    "file:///Helper.java": "package app;\npublic class Helper {}",
  });
  expect(actionsAt(ctx, "Helper").filter(a => a.kind === "quickfix")).toEqual([]);
});

test("no import offered for java.lang types", () => {
  const ctx = setup("package app;\nclass C { String s; }");
  expect(actionsAt(ctx, "String").filter(a => a.kind === "quickfix")).toEqual([]);
});

// --- organize imports --------------------------------------------------------------

function organize(ctx: ReturnType<typeof setup>) {
  return actionsAt(ctx, "class").find(a => a.kind === "source.organizeImports");
}

test("removes an unused single-type import", () => {
  const ctx = setup(
    "package app;\nimport java.util.List;\nimport java.util.Map;\nclass C { List<String> xs; }",
  );
  expect(apply(ctx.text, organize(ctx)!)).toBe(
    "package app;\nimport java.util.List;\nclass C { List<String> xs; }",
  );
});

test("sorts imports and keeps on-demand and static", () => {
  const ctx = setup(
    "package app;\nimport java.util.Map;\nimport static java.lang.Math.max;\nimport java.util.*;\nimport java.util.List;\n" +
      "class C { List<String> xs; Map<String,String> m; }",
  );
  expect(apply(ctx.text, organize(ctx)!)).toBe(
    "package app;\nimport java.util.*;\nimport java.util.List;\nimport java.util.Map;\nimport static java.lang.Math.max;\n" +
      "class C { List<String> xs; Map<String,String> m; }",
  );
});

test("no organize action when imports are already minimal and sorted", () => {
  const ctx = setup("package app;\nimport java.util.List;\nclass C { List<String> xs; }");
  expect(organize(ctx)).toBeUndefined();
});

// --- extract local variable --------------------------------------------------------

function extractAction(ctx: ReturnType<typeof setup>, exprText: string, occ = 1) {
  let start = -1;
  for (let i = 0; i < occ; i++) start = ctx.text.indexOf(exprText, start + 1);
  const sf = ctx.program.getSourceFile("file:///T.java")!;
  return getCodeActions(ctx.program, ctx.checker, sf, start, start + exprText.length).find(
    a => a.kind === "refactor.extract",
  );
}

test("extracts a binary expression into a local above the statement", () => {
  const ctx = setup(
    ["class C {", "  int m() {", "    int b = compute() + 1;", "    return b;", "  }", "}"].join(
      "\n",
    ),
  );
  const action = extractAction(ctx, "compute() + 1")!;
  expect(apply(ctx.text, action)).toBe(
    [
      "class C {",
      "  int m() {",
      "    var extracted = compute() + 1;",
      "    int b = extracted;",
      "    return b;",
      "  }",
      "}",
    ].join("\n"),
  );
});

test("extracts a call argument expression", () => {
  const ctx = setup(["class C {", "  void m() {", "    use(a * b + c);", "  }", "}"].join("\n"));
  const action = extractAction(ctx, "a * b + c")!;
  expect(apply(ctx.text, action)).toBe(
    [
      "class C {",
      "  void m() {",
      "    var extracted = a * b + c;",
      "    use(extracted);",
      "  }",
      "}",
    ].join("\n"),
  );
});

test("no extract for a selection that is not a whole expression", () => {
  const ctx = setup(["class C {", "  void m() {", "    use(a + b);", "  }", "}"].join("\n"));
  // "a +" is not a complete expression node
  expect(extractAction(ctx, "a +")).toBeUndefined();
});

test("no extract for an expression outside a block (field initializer)", () => {
  const ctx = setup("class C { int f = 1 + 2; }");
  expect(extractAction(ctx, "1 + 2")).toBeUndefined();
});

// --- inline local variable ---------------------------------------------------------

function inlineAt(ctx: ReturnType<typeof setup>, needle: string, occ = 1) {
  let offset = -1;
  for (let i = 0; i < occ; i++) offset = ctx.text.indexOf(needle, offset + 1);
  const sf = ctx.program.getSourceFile("file:///T.java")!;
  return getCodeActions(ctx.program, ctx.checker, sf, offset, offset).find(
    a => a.kind === "refactor.inline",
  );
}

test("inlines a local into its single use and removes the declaration", () => {
  const ctx = setup(
    ["class C {", "  int m() {", "    int total = 1;", "    return total + 2;", "  }", "}"].join(
      "\n",
    ),
  );
  const action = inlineAt(ctx, "total")!;
  expect(apply(ctx.text, action)).toBe(
    ["class C {", "  int m() {", "    return 1 + 2;", "  }", "}"].join("\n"),
  );
});

test("inlines into multiple uses", () => {
  const ctx = setup(
    [
      "class C {",
      "  void m() {",
      "    String msg = name();",
      "    use(msg, msg);",
      "  }",
      "}",
    ].join("\n"),
  );
  const action = inlineAt(ctx, "msg")!;
  expect(apply(ctx.text, action)).toBe(
    ["class C {", "  void m() {", "    use(name(), name());", "  }", "}"].join("\n"),
  );
});

test("wraps a compound initializer in parentheses when inlining", () => {
  const ctx = setup(
    ["class C {", "  int m() {", "    int sum = a + b;", "    return sum * 2;", "  }", "}"].join(
      "\n",
    ),
  );
  const action = inlineAt(ctx, "sum")!;
  expect(apply(ctx.text, action)).toBe(
    ["class C {", "  int m() {", "    return (a + b) * 2;", "  }", "}"].join("\n"),
  );
});

test("no inline when the local is reassigned", () => {
  const ctx = setup(
    ["class C {", "  void m() {", "    int n = 1;", "    n = 2;", "    use(n);", "  }", "}"].join(
      "\n",
    ),
  );
  expect(inlineAt(ctx, "n ", 1)).toBeUndefined();
});

test("no inline for a local without an initializer", () => {
  const ctx = setup(
    ["class C {", "  void m() {", "    int x;", "    use(x);", "  }", "}"].join("\n"),
  );
  expect(inlineAt(ctx, "x")).toBeUndefined();
});

// --- change signature: remove unused parameter -------------------------------------

function rewriteAt(ctx: ReturnType<typeof setup>, needle: string, occ = 1) {
  let offset = -1;
  for (let i = 0; i < occ; i++) offset = ctx.text.indexOf(needle, offset + 1);
  const sf = ctx.program.getSourceFile("file:///T.java")!;
  return getCodeActions(ctx.program, ctx.checker, sf, offset, offset).find(
    a => a.kind === "refactor.rewrite",
  );
}

test("removes an unused middle parameter from the declaration and call sites", () => {
  const ctx = setup(
    "class C { void m(int aa, int bb, int cc) { use(aa, cc); } void caller() { m(1, 2, 3); } }",
  );
  const action = rewriteAt(ctx, "bb")!;
  expect(action.title).toBe("Remove unused parameter 'bb'");
  expect(apply(ctx.text, action)).toBe(
    "class C { void m(int aa, int cc) { use(aa, cc); } void caller() { m(1, 3); } }",
  );
});

test("removes an unused last parameter", () => {
  const ctx = setup("class C { void m(int aa, int bb) { use(aa); } void caller() { m(1, 2); } }");
  expect(apply(ctx.text, rewriteAt(ctx, "bb")!)).toBe(
    "class C { void m(int aa) { use(aa); } void caller() { m(1); } }",
  );
});

test("removes the only parameter", () => {
  const ctx = setup("class C { void m(int aa) {} void caller() { m(1); } }");
  expect(apply(ctx.text, rewriteAt(ctx, "aa")!)).toBe(
    "class C { void m() {} void caller() { m(); } }",
  );
});

test("no remove-parameter when the parameter is used", () => {
  const ctx = setup("class C { void m(int aa) { use(aa); } }");
  expect(rewriteAt(ctx, "aa")).toBeUndefined();
});

test("no remove-parameter for an overloaded method (ambiguous call sites)", () => {
  const ctx = setup("class C { void m(int aa) {} void m(int aa, int bb) {} }");
  expect(rewriteAt(ctx, "aa")).toBeUndefined();
});
