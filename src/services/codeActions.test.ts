import { test } from "node:test";

import { expect } from "expect";

import { createChecker } from "../compiler/checker.ts";
import { getCodeActions, languageFeatures, type CodeActionResult } from "./codeActions.ts";
import { loadJdkStub } from "../compiler/jdkStub.ts";
import { createProgram } from "../compiler/program.ts";
import { type Uri } from "../workspace.ts";

function setup(text: string, extra: Record<string, string> = {}) {
  const program = createProgram();
  loadJdkStub(program);
  for (const [uri, t] of Object.entries(extra)) program.addProjectFile(uri as Uri, t);
  program.setOpenDocument("file:///T.java" as Uri, text, 1);
  return { program, checker: createChecker(program), text };
}

// Apply a single-file action's changes to the source text (offsets are in T.java).
function apply(text: string, action: CodeActionResult): string {
  let out = text;
  for (const c of action.changes.toSorted((a, b) => b.start - a.start)) {
    out = out.slice(0, c.start) + c.newText + out.slice(c.end);
  }
  return out;
}

// Apply the proposed edit and assert it is correct: the result equals `want` AND
// re-parses as syntactically valid Java (no parse diagnostics), so a rewrite can
// never silently emit broken code.
function expectEdit(text: string, action: CodeActionResult, want: string): void {
  const out = apply(text, action);
  const reparsed = setup(out).program.getSourceFile("file:///T.java" as Uri)!;
  expect(reparsed.parseDiagnostics).toEqual([]);
  expect(out).toBe(want);
}

function actionsAt(ctx: ReturnType<typeof setup>, needle: string, occ = 1, release?: number) {
  let offset = -1;
  for (let i = 0; i < occ; i++) offset = ctx.text.indexOf(needle, offset + 1);
  return getCodeActions(
    ctx.program,
    ctx.checker,
    ctx.program.getSourceFile("file:///T.java" as Uri)!,
    offset,
    offset,
    languageFeatures(release),
  );
}

// --- remove unused import ------------------------------------------------------------

test("remove unused import deletes the whole line", () => {
  const text =
    "import java.util.List;\nimport java.util.Map;\n\nclass T { Map<String, String> m; }\n";
  const ctx = setup(text);
  const actions = actionsAt(ctx, "java.util.List").filter(a => a.kind === "quickfix");
  expect(actions.map(a => a.title)).toEqual(["Remove unused import 'java.util.List'"]);
  expect(apply(text, actions[0]!)).toBe(
    "import java.util.Map;\n\nclass T { Map<String, String> m; }\n",
  );
});

test("no removal is offered for a used import or outside the import", () => {
  const text = "import java.util.Map;\n\nclass T { Map<String, String> m; }\n";
  const ctx = setup(text);
  expect(actionsAt(ctx, "java.util.Map").filter(a => a.kind === "quickfix")).toEqual([]);
  const unusedElsewhere = setup("import java.util.List;\n\nclass T { int x; }\n");
  expect(actionsAt(unusedElsewhere, "int x").filter(a => a.kind === "quickfix")).toEqual([]);
});

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
  const sf = ctx.program.getSourceFile("file:///T.java" as Uri)!;
  return getCodeActions(
    ctx.program,
    ctx.checker,
    sf,
    start,
    start + exprText.length,
    languageFeatures(undefined),
  ).find(a => a.kind === "refactor.extract");
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
  const sf = ctx.program.getSourceFile("file:///T.java" as Uri)!;
  return getCodeActions(
    ctx.program,
    ctx.checker,
    sf,
    offset,
    offset,
    languageFeatures(undefined),
  ).find(a => a.kind === "refactor.inline");
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
  const sf = ctx.program.getSourceFile("file:///T.java" as Uri)!;
  return getCodeActions(
    ctx.program,
    ctx.checker,
    sf,
    offset,
    offset,
    languageFeatures(undefined),
  ).find(a => a.kind === "refactor.rewrite");
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

// --- remove redundant @Override --------------------------------------------------

test("remove redundant @Override deletes only the wrong annotation", () => {
  const text = [
    "class Base { void real() {} }",
    "class T extends Base {",
    "  @Override void notThere() {}",
    "  @Override void real() {}",
    "}",
  ].join("\n");
  const ctx = setup(text);
  const actions = actionsAt(ctx, "notThere").filter(a => a.kind === "quickfix");
  expect(actions.map(a => a.title)).toEqual(["Remove redundant '@Override'"]);
  expect(apply(text, actions[0]!)).toBe(
    [
      "class Base { void real() {} }",
      "class T extends Base {",
      "  void notThere() {}",
      "  @Override void real() {}",
      "}",
    ].join("\n"),
  );
});

test("no remove-@Override on a method that genuinely overrides", () => {
  const text = "class Base { void real() {} }\nclass T extends Base { @Override void real() {} }";
  const ctx = setup(text);
  expect(
    actionsAt(ctx, "real", 2)
      .filter(a => a.kind === "quickfix")
      .map(a => a.title),
  ).toEqual([]);
});

// --- make field final (nikeee/cappu#38) ----------------------------------------------

test("add 'final' to a private field with an initializer", () => {
  const text = "class T {\n  private int x = 1;\n  int use() { return x; }\n}";
  const ctx = setup(text);
  const actions = actionsAt(ctx, "x = 1").filter(a => a.kind === "quickfix");
  expect(actions.map(a => a.title)).toEqual(["Add 'final' modifier"]);
  expect(apply(text, actions[0]!)).toBe(
    "class T {\n  private final int x = 1;\n  int use() { return x; }\n}",
  );
});

test("'final' lands after all existing modifiers", () => {
  const text = "class T {\n  @Deprecated private static int N = 1;\n}";
  const ctx = setup(text);
  const actions = actionsAt(ctx, "N = 1").filter(a => a.kind === "quickfix");
  expect(actions.map(a => a.title)).toEqual(["Add 'final' modifier"]);
  expect(apply(text, actions[0]!)).toBe(
    "class T {\n  @Deprecated private static final int N = 1;\n}",
  );
});

test("add 'final' to a constructor-assigned private field", () => {
  const text = "class T {\n  private int y;\n  T(int v) { this.y = v; }\n}";
  const ctx = setup(text);
  const actions = actionsAt(ctx, "int y").filter(a => a.kind === "quickfix");
  expect(actions.map(a => a.title)).toEqual(["Add 'final' modifier"]);
  expect(apply(text, actions[0]!)).toBe(
    "class T {\n  private final int y;\n  T(int v) { this.y = v; }\n}",
  );
});

test("add 'final' to a multi-declarator field applies to the whole declaration", () => {
  const text = "class T {\n  private int a = 1, b = 2;\n}";
  const ctx = setup(text);
  const actions = actionsAt(ctx, "a = 1").filter(a => a.kind === "quickfix");
  expect(actions.map(a => a.title)).toEqual(["Add 'final' modifier"]);
  expect(apply(text, actions[0]!)).toBe("class T {\n  private final int a = 1, b = 2;\n}");
});

test("no add-'final' on reassigned, already-final, or unflagged positions", () => {
  const reassigned = setup("class T {\n  private int x = 1;\n  void m() { x = 2; }\n}");
  expect(
    actionsAt(reassigned, "x = 1")
      .filter(a => a.kind === "quickfix")
      .map(a => a.title),
  ).toEqual([]);
  const alreadyFinal = setup("class T {\n  private final int x = 1;\n}");
  expect(
    actionsAt(alreadyFinal, "x = 1")
      .filter(a => a.kind === "quickfix")
      .map(a => a.title),
  ).toEqual([]);
  const elsewhere = setup("class T {\n  private int x = 1;\n  void m() { int local = 2; }\n}");
  expect(
    actionsAt(elsewhere, "local")
      .filter(a => a.kind === "quickfix")
      .map(a => a.title),
  ).toEqual([]);
});

// --- convert class to record -------------------------------------------------------

const POINT =
  "class Point {\n" +
  "  private final int x;\n" +
  "  private final int y;\n" +
  "  Point(int x, int y) { this.x = x; this.y = y; }\n" +
  "  public int getX() { return x; }\n" +
  "  public int getY() { return this.y; }\n" +
  "}\n";

function recordActions(ctx: ReturnType<typeof setup>, needle = "class Point") {
  return actionsAt(ctx, needle).filter(a => a.title === "Convert class to record");
}

test("converts a POJO to a record", () => {
  const ctx = setup(POINT);
  const action = recordActions(ctx)[0]!;
  expect(action.kind).toBe("refactor.rewrite");
  expect(apply(POINT, action)).toBe("record Point(int x, int y) {\n}\n");
});

test("convert-to-record is gated on release >= 16", () => {
  const ctx = setup(POINT);
  const rec = (r: number) =>
    actionsAt(ctx, "class Point", 1, r).filter(a => a.title === "Convert class to record");
  expect(rec(15)).toEqual([]);
  expect(rec(16).length).toBe(1);
});

test("preserves modifiers, type params and implements", () => {
  const text =
    "public class Box<T> implements java.io.Serializable {\n" +
    "  private final T v;\n" +
    "  public Box(T v) { this.v = v; }\n" +
    "  public T getV() { return v; }\n" +
    "}\n";
  const ctx = setup(text);
  const action = recordActions(ctx, "class Box")[0]!;
  expect(apply(text, action)).toBe(
    "public record Box<T>(T v) implements java.io.Serializable {\n}\n",
  );
});

test("supports an isX accessor on a boolean field", () => {
  const text =
    "class Flag {\n" +
    "  private final boolean on;\n" +
    "  Flag(boolean on) { this.on = on; }\n" +
    "  public boolean isOn() { return on; }\n" +
    "}\n";
  const ctx = setup(text);
  expect(apply(text, recordActions(ctx, "class Flag")[0]!)).toBe("record Flag(boolean on) {\n}\n");
});

test("renames accessor call sites in other files", () => {
  const other = "class U { int m(Point p) { return p.getX() + p.getY(); } }\n";
  const ctx = setup(POINT, { "file:///U.java": other });
  const action = recordActions(ctx)[0]!;
  const edits = action.additionalEdits?.["file:///U.java"];
  expect(edits).toBeDefined();
  let out = other;
  for (const c of edits!.toSorted((a, b) => b.start - a.start)) {
    out = out.slice(0, c.start) + c.newText + out.slice(c.end);
  }
  expect(out).toBe("class U { int m(Point p) { return p.x() + p.y(); } }\n");
});

test("is not offered when the shape does not fit", () => {
  const cases: string[] = [
    // field with an initializer
    "class C { private final int x = 5; C(int x) { this.x = x; } public int getX() { return x; } }\n",
    // non-final field
    "class C { private int x; C(int x) { this.x = x; } public int getX() { return x; } }\n",
    // non-private field
    "class C { final int x; C(int x) { this.x = x; } public int getX() { return x; } }\n",
    // a static member present
    "class C { static int Z; private final int x; C(int x) { this.x = x; } public int getX() { return x; } }\n",
    // an extra non-getter method
    "class C { private final int x; C(int x) { this.x = x; } public int getX() { return x; } public void run() {} }\n",
    // extends a class
    "class C extends B { private final int x; C(int x) { this.x = x; } public int getX() { return x; } }\n",
    // abstract
    "abstract class C { private final int x; C(int x) { this.x = x; } public int getX() { return x; } }\n",
    // ctor parameter order differs from field order
    "class C { private final int x; private final int y; C(int y, int x) { this.x = x; this.y = y; } }\n",
    // ctor throws
    "class C { private final int x; C(int x) throws Exception { this.x = x; } public int getX() { return x; } }\n",
    // ctor body does more than assign
    "class C { private final int x; C(int x) { this.x = x; run(); } public int getX() { return x; } }\n",
    // getter body is not a plain field return
    "class C { private final int x; C(int x) { this.x = x; } public int getX() { return x + 1; } }\n",
    // getter body returns a literal, not the field
    "class C { private final int x; C(int x) { this.x = x; } public int getX() { return 0; } }\n",
    // getter body has more than one statement
    "class C { private final int x; C(int x) { this.x = x; } public int getX() { log(); return x; } }\n",
    // an isX accessor on a non-boolean field does not match
    "class C { private final int x; C(int x) { this.x = x; } public int isX() { return x; } }\n",
    // a getter that maps to no declared field
    "class C { private final int x; C(int x) { this.x = x; } public int getZ() { return x; } }\n",
    // a getter that takes a parameter is not an accessor
    "class C { private final int x; C(int x) { this.x = x; } public int getX(int i) { return x; } }\n",
    // a generic getter is not a plain accessor
    "class C { private final int x; C(int x) { this.x = x; } public <T> int getX() { return x; } }\n",
    // a getter that declares throws
    "class C { private final int x; C(int x) { this.x = x; } public int getX() throws Exception { return x; } }\n",
    // a static getter
    "class C { private final int x; C(int x) { this.x = x; } public static int getX() { return x; } }\n",
    // a field carrying an annotation
    "class C { @Deprecated private final int x; C(int x) { this.x = x; } public int getX() { return x; } }\n",
    // constructor parameter type differs from the field type
    "class C { private final int x; C(long x) { this.x = x; } public int getX() { return x; } }\n",
    // a varargs constructor parameter
    "class C { private final int[] x; C(int... x) { this.x = x; } public int[] getX() { return x; } }\n",
    // more than one constructor
    "class C { private final int x; C(int x) { this.x = x; } C() { this.x = 0; } public int getX() { return x; } }\n",
    // a field assigned twice while another is never assigned
    "class C { private final int x; private final int y; C(int x, int y) { this.x = x; this.x = y; } }\n",
    // a non-static inner class cannot be a record
    "class O { class C { private final int x; C(int x) { this.x = x; } public int getX() { return x; } } }\n",
  ];
  for (const text of cases) {
    const ctx = setup(text);
    expect(recordActions(ctx, "class C").map(a => a.title)).toEqual([]);
  }
});

test("converts a class whose fields do not all have getters", () => {
  const text =
    "class P {\n" +
    "  private final int x;\n" +
    "  private final int y;\n" +
    "  P(int x, int y) { this.x = x; this.y = y; }\n" +
    "  public int getX() { return x; }\n" +
    "}\n";
  const ctx = setup(text);
  expect(apply(text, recordActions(ctx, "class P")[0]!)).toBe("record P(int x, int y) {\n}\n");
});

test("converts with bare-name constructor assignment", () => {
  const text =
    "class P { private final int v; P(int v) { v = v; } public int getV() { return v; } }\n";
  const ctx = setup(text);
  expect(apply(text, recordActions(ctx, "class P")[0]!)).toBe("record P(int v) {\n}\n");
});

test("preserves multiple implemented interfaces", () => {
  const text =
    "class M implements A, B { private final int x; M(int x) { this.x = x; } public int getX() { return x; } }\n";
  const ctx = setup(text);
  expect(apply(text, recordActions(ctx, "class M")[0]!)).toBe(
    "record M(int x) implements A, B {\n}\n",
  );
});

test("converts a static nested class", () => {
  const text =
    "class Outer {\n" +
    "  static class Inner { private final int x; Inner(int x) { this.x = x; } public int getX() { return x; } }\n" +
    "}\n";
  const ctx = setup(text);
  expect(apply(text, recordActions(ctx, "class Inner")[0]!)).toBe(
    "class Outer {\n" + "  static record Inner(int x) {\n}\n" + "}\n",
  );
});

test("renames accessor call sites elsewhere in the same file", () => {
  const text =
    "class P { private final int x; P(int x) { this.x = x; } public int getX() { return x; } }\n" +
    "class Q { int m(P p) { return p.getX(); } }\n";
  const ctx = setup(text);
  expect(apply(text, recordActions(ctx, "class P")[0]!)).toBe(
    "record P(int x) {\n}\n" + "class Q { int m(P p) { return p.x(); } }\n",
  );
});

test("is not offered on a record or away from a class", () => {
  const rec = setup("record R(int x) {}\n");
  expect(recordActions(rec, "record R").map(a => a.title)).toEqual([]);
  const imp = setup(
    "import java.util.List;\nclass C { private final int x; C(int x){this.x=x;} }\n",
  );
  expect(recordActions(imp, "import").map(a => a.title)).toEqual([]);
});

test("is not offered when another class extends it", () => {
  const base =
    "class Base {\n  private final int x;\n  Base(int x) { this.x = x; }\n  public int getX() { return x; }\n}\n";
  const ctx = setup(base, { "file:///Sub.java": "class Sub extends Base {}\n" });
  expect(recordActions(ctx, "class Base").map(a => a.title)).toEqual([]);
});

// --- use 'var' for a local variable ------------------------------------------

const varTitle = "Use 'var' for local variable";

test("var: offered for a constructor call", () => {
  const text =
    "class T {\n  void m() {\n    java.util.ArrayList<String> xs = new java.util.ArrayList<String>();\n  }\n}";
  const ctx = setup(text);
  const actions = actionsAt(ctx, "xs =").filter(a => a.title === varTitle);
  expect(actions.length).toBe(1);
  expectEdit(
    text,
    actions[0]!,
    "class T {\n  void m() {\n    var xs = new java.util.ArrayList<String>();\n  }\n}",
  );
});

test("var: offered for a cast", () => {
  const text = "class T {\n  void m(Object o) {\n    String s = (String) o;\n  }\n}";
  const ctx = setup(text);
  const actions = actionsAt(ctx, "s =").filter(a => a.title === varTitle);
  expect(actions.length).toBe(1);
  expectEdit(text, actions[0]!, "class T {\n  void m(Object o) {\n    var s = (String) o;\n  }\n}");
});

test("var: offered for a literal, preserving a final modifier", () => {
  const text = "class T {\n  void m() {\n    final int n = 42;\n  }\n}";
  const ctx = setup(text);
  const actions = actionsAt(ctx, "n =").filter(a => a.title === varTitle);
  expect(actions.length).toBe(1);
  expectEdit(text, actions[0]!, "class T {\n  void m() {\n    final var n = 42;\n  }\n}");
});

test("var: not offered for a diamond new (would not compile)", () => {
  const text =
    "class T {\n  void m() {\n    java.util.List<String> xs = new java.util.ArrayList<>();\n  }\n}";
  const ctx = setup(text);
  expect(actionsAt(ctx, "xs =").filter(a => a.title === varTitle)).toEqual([]);
});

test("var: not offered when the type is already var", () => {
  const text = "class T {\n  void m() {\n    var s = (String) null;\n  }\n}";
  const ctx = setup(text);
  expect(actionsAt(ctx, "s =").filter(a => a.title === varTitle)).toEqual([]);
});

test("var: not offered for a non-obvious initializer (method call)", () => {
  const text = "class T {\n  int f() { return 1; }\n  void m() {\n    int n = f();\n  }\n}";
  const ctx = setup(text);
  expect(actionsAt(ctx, "n =").filter(a => a.title === varTitle)).toEqual([]);
});

test("var: gated on release >= 10", () => {
  const text = "class T {\n  void m() {\n    String s = (String) null;\n  }\n}";
  const ctx = setup(text);
  expect(actionsAt(ctx, "s =", 1, 9).filter(a => a.title === varTitle)).toEqual([]);
  expect(actionsAt(ctx, "s =", 1, 10).filter(a => a.title === varTitle).length).toBe(1);
});

// --- convert anonymous class to lambda ---------------------------------------

const lambdaTitle = "Convert anonymous class to lambda";

function lambdaActions(ctx: ReturnType<typeof setup>, needle = "new Runnable", release?: number) {
  return actionsAt(ctx, needle, 1, release).filter(a => a.title === lambdaTitle);
}

test("lambda: converts a Runnable anonymous class", () => {
  const text =
    'class T {\n  Runnable r = new Runnable() {\n    public void run() { System.out.println("hi"); }\n  };\n}';
  const ctx = setup(text);
  const actions = lambdaActions(ctx);
  expect(actions.length).toBe(1);
  expectEdit(
    text,
    actions[0]!,
    'class T {\n  Runnable r = () -> { System.out.println("hi"); };\n}',
  );
});

test("lambda: converts a Comparator, ignoring its default and static methods", () => {
  const text =
    "class T {\n  java.util.Comparator<String> c = new java.util.Comparator<String>() {\n    public int compare(String a, String b) { return 0; }\n  };\n}";
  const ctx = setup(text);
  const actions = lambdaActions(ctx, "new java.util.Comparator");
  expect(actions.length).toBe(1);
  expectEdit(
    text,
    actions[0]!,
    "class T {\n  java.util.Comparator<String> c = (a, b) -> { return 0; };\n}",
  );
});

test("lambda: not offered for a non-functional interface (two abstract methods)", () => {
  const text =
    "class T {\n  interface Two { void a(); void b(); }\n  Two t = new Two() { public void a() {} };\n}";
  const ctx = setup(text);
  expect(lambdaActions(ctx, "new Two")).toEqual([]);
});

test("lambda: not offered when the body has an extra member", () => {
  const text =
    "class T {\n  Runnable r = new Runnable() {\n    int x = 1;\n    public void run() {}\n  };\n}";
  const ctx = setup(text);
  expect(lambdaActions(ctx)).toEqual([]);
});

test("lambda: not offered when the body references this", () => {
  const text =
    "class T {\n  Runnable r = new Runnable() {\n    public void run() { this.hashCode(); }\n  };\n}";
  const ctx = setup(text);
  expect(lambdaActions(ctx)).toEqual([]);
});

test("lambda: not offered for an anonymous subclass of a non-interface", () => {
  const text =
    "class T {\n  abstract static class A { abstract void go(); }\n  A a = new A() { void go() {} };\n}";
  const ctx = setup(text);
  expect(lambdaActions(ctx, "new A")).toEqual([]);
});

test("lambda: gated on release >= 8", () => {
  const text = "class T {\n  Runnable r = new Runnable() {\n    public void run() {}\n  };\n}";
  const ctx = setup(text);
  expect(lambdaActions(ctx, "new Runnable", 7)).toEqual([]);
  expect(lambdaActions(ctx, "new Runnable", 8).length).toBe(1);
});
