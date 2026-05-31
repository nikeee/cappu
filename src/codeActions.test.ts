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
