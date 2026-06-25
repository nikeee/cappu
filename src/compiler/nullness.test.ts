import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import { createChecker } from "./checker.ts";
import { Diagnostics } from "./diagnostics.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { type NullnessOptions } from "./nullness.ts";
import { createProgram } from "./program.ts";
import { type Uri } from "../workspace.ts";

const JSPECIFY: NullnessOptions = {
  enabled: true,
  nullableAnnotations: ["org.jspecify.annotations.Nullable"],
  nonNullAnnotations: ["org.jspecify.annotations.NonNull"],
  nullMarkedAnnotations: ["org.jspecify.annotations.NullMarked"],
  nullUnmarkedAnnotations: ["org.jspecify.annotations.NullUnmarked"],
};

const NULL_INTO_NONNULL = Diagnostics.Possibly_null_value_assigned_to_non_null_0.code;
const DEREF = Diagnostics.Dereference_of_possibly_null_value_0.code;

// `null` (the default) means "no options passed at all", i.e. the feature off.
function diagnose(text: string, nullness: NullnessOptions | null = JSPECIFY): number[] {
  const program = createProgram();
  loadJdkStub(program);
  program.setOpenDocument("file:///T.java" as Uri, text, 1);
  const checker = createChecker(program, nullness ?? undefined);
  return checker
    .getSemanticDiagnostics(program.getSourceFile("file:///T.java" as Uri)!)
    .map(d => d.code);
}

// --- explicit @NonNull -------------------------------------------------------------

test("null literal passed to a @NonNull parameter is flagged", () => {
  const code = "class C { void f(@NonNull String s) {} void g() { f(null); } }";
  expect(diagnose(code)).toContain(NULL_INTO_NONNULL);
});

test("null literal assigned to a @NonNull field is flagged", () => {
  const code = "class C { @NonNull String x = null; }";
  expect(diagnose(code)).toContain(NULL_INTO_NONNULL);
});

test("null returned from a @NonNull method is flagged", () => {
  const code = "class C { @NonNull String f() { return null; } }";
  expect(diagnose(code)).toContain(NULL_INTO_NONNULL);
});

test("null assigned to a @NonNull field by an assignment is flagged", () => {
  const code = 'class C { @NonNull String x = "a"; void g() { x = null; } }';
  expect(diagnose(code)).toContain(NULL_INTO_NONNULL);
});

test("a @Nullable parameter accepts null", () => {
  const code = "class C { void f(@Nullable String s) {} void g() { f(null); } }";
  expect(diagnose(code)).not.toContain(NULL_INTO_NONNULL);
});

// --- @NullMarked defaults ----------------------------------------------------------

test("inside @NullMarked an unannotated reference parameter rejects null", () => {
  const code = "@NullMarked class C { void f(String s) {} void g() { f(null); } }";
  expect(diagnose(code)).toContain(NULL_INTO_NONNULL);
});

test("inside @NullMarked a @Nullable parameter still accepts null", () => {
  const code = "@NullMarked class C { void f(@Nullable String s) {} void g() { f(null); } }";
  expect(diagnose(code)).not.toContain(NULL_INTO_NONNULL);
});

test("@NullUnmarked on a method opts back out of an enclosing @NullMarked", () => {
  const code = "@NullMarked class C { @NullUnmarked void f(String s) {} void g() { f(null); } }";
  expect(diagnose(code)).not.toContain(NULL_INTO_NONNULL);
});

test("inside @NullMarked an unannotated reference local rejects null", () => {
  const code = "@NullMarked class C { void g() { String s = null; } }";
  expect(diagnose(code)).toContain(NULL_INTO_NONNULL);
});

test("a plain unannotated parameter outside @NullMarked accepts null", () => {
  const code = "class C { void f(String s) {} void g() { f(null); } }";
  expect(diagnose(code)).not.toContain(NULL_INTO_NONNULL);
});

// --- @Nullable-typed values --------------------------------------------------------

test("a @Nullable method return passed to a @NonNull parameter is flagged", () => {
  const code =
    "class C { @Nullable String n() { return null; } void f(@NonNull String s) {} void g() { f(n()); } }";
  expect(diagnose(code)).toContain(NULL_INTO_NONNULL);
});

test("a non-null value passed to a @NonNull parameter is accepted", () => {
  const code =
    'class C { @NonNull String n() { return "a"; } void f(@NonNull String s) {} void g() { f(n()); } }';
  expect(diagnose(code)).not.toContain(NULL_INTO_NONNULL);
});

// --- disabled / config -------------------------------------------------------------

test("nullness checks are off by default (no options passed)", () => {
  const code = "class C { void f(@NonNull String s) {} void g() { f(null); } }";
  expect(diagnose(code, null)).not.toContain(NULL_INTO_NONNULL);
});

test("enabled: false produces no nullness diagnostics", () => {
  const code = "class C { void f(@NonNull String s) {} void g() { f(null); } }";
  expect(diagnose(code, { ...JSPECIFY, enabled: false })).not.toContain(NULL_INTO_NONNULL);
});

test("a custom non-null annotation list (JSR-305) is honored", () => {
  const code = "class C { void f(@Nonnull String s) {} void g() { f(null); } }";
  const config: NullnessOptions = { ...JSPECIFY, nonNullAnnotations: ["javax.annotation.Nonnull"] };
  expect(diagnose(code, config)).toContain(NULL_INTO_NONNULL);
});

// --- generic (type-argument) nullness ----------------------------------------------

const BOX = "class Box<T> { void put(T t) {} T get() { return get(); } }\n";

test("null into the non-null element of Box<@NonNull String>.put is flagged", () => {
  const code = `${BOX}class C { void g(Box<@NonNull String> b) { b.put(null); } }`;
  expect(diagnose(code)).toContain(NULL_INTO_NONNULL);
});

test("null into the nullable element of Box<@Nullable String>.put is accepted", () => {
  const code = `${BOX}class C { void g(Box<@Nullable String> b) { b.put(null); } }`;
  expect(diagnose(code)).not.toContain(NULL_INTO_NONNULL);
});

test("inside @NullMarked an unannotated Box<String> element rejects null", () => {
  const code = `${BOX}@NullMarked class C { void g(Box<String> b) { b.put(null); } }`;
  expect(diagnose(code)).toContain(NULL_INTO_NONNULL);
});

test("a non-null value into Box<@NonNull String>.put is accepted", () => {
  const code = `${BOX}class C { void g(Box<@NonNull String> b) { b.put("x"); } }`;
  expect(diagnose(code)).not.toContain(NULL_INTO_NONNULL);
});

test("a @Nullable generic element returned by get flows into a non-null parameter", () => {
  const code = `${BOX}class C { void f(@NonNull String s) {} void g(Box<@Nullable String> b) { f(b.get()); } }`;
  expect(diagnose(code)).toContain(NULL_INTO_NONNULL);
});

// --- cross-file package-info.java ---------------------------------------------------

function diagnoseFiles(files: Record<string, string>, target: string): number[] {
  const program = createProgram();
  loadJdkStub(program);
  for (const [uri, text] of Object.entries(files)) {
    program.setOpenDocument(uri as Uri, text, 1);
  }
  const checker = createChecker(program, JSPECIFY);
  return checker.getSemanticDiagnostics(program.getSourceFile(target as Uri)!).map(d => d.code);
}

test("@NullMarked in a package-info.java marks another file of the same package", () => {
  const codes = diagnoseFiles(
    {
      "file:///p/package-info.java": "@NullMarked package p;",
      "file:///p/C.java": "package p; class C { void f(String s) {} void g() { f(null); } }",
    },
    "file:///p/C.java",
  );
  expect(codes).toContain(NULL_INTO_NONNULL);
});

test("without a @NullMarked package-info.java the same code is not flagged", () => {
  const codes = diagnoseFiles(
    {
      "file:///p/package-info.java": "package p;",
      "file:///p/C.java": "package p; class C { void f(String s) {} void g() { f(null); } }",
    },
    "file:///p/C.java",
  );
  expect(codes).not.toContain(NULL_INTO_NONNULL);
});

// --- additional coverage -----------------------------------------------------------

test("null into a @NonNull constructor parameter is flagged", () => {
  const code = "class Foo { Foo(@NonNull String s) {} }\nclass C { void g() { new Foo(null); } }";
  expect(diagnose(code)).toContain(NULL_INTO_NONNULL);
});

test("a non-null value into a @NonNull constructor parameter is accepted", () => {
  const code = 'class Foo { Foo(@NonNull String s) {} }\nclass C { void g() { new Foo("a"); } }';
  expect(diagnose(code)).not.toContain(NULL_INTO_NONNULL);
});

test("null reassigned to a @NonNull local is flagged", () => {
  const code = 'class C { void g() { @NonNull String x = "a"; x = null; } }';
  expect(diagnose(code)).toContain(NULL_INTO_NONNULL);
});

test("a @Nullable field passed to a @NonNull parameter is flagged", () => {
  const code =
    "class C { @Nullable String fld; void f(@NonNull String s) {} void g() { f(fld); } }";
  expect(diagnose(code)).toContain(NULL_INTO_NONNULL);
});

test("a @NonNull field read passed to a @NonNull parameter is accepted", () => {
  const code =
    'class C { @NonNull String fld = "a"; void f(@NonNull String s) {} void g() { f(fld); } }';
  expect(diagnose(code)).not.toContain(NULL_INTO_NONNULL);
});

test("a @Nullable return initializing a @NonNull local is flagged", () => {
  const code =
    "class C { @Nullable String n() { return n(); } void g() { @NonNull String x = n(); } }";
  expect(diagnose(code)).toContain(NULL_INTO_NONNULL);
});

test("a varargs @NonNull parameter is not checked (array, not element)", () => {
  const code = "class C { void f(@NonNull String... xs) {} void g() { f(null); } }";
  expect(diagnose(code)).not.toContain(NULL_INTO_NONNULL);
});

test("an in-file @NullMarked package marks an unannotated parameter", () => {
  const code = "@NullMarked package p;\nclass C { void f(String s) {} void g() { f(null); } }";
  expect(diagnose(code)).toContain(NULL_INTO_NONNULL);
});

test("@NullMarked on an enclosing type marks a nested type's parameter", () => {
  const code =
    "@NullMarked class Outer { static class Inner { void f(String s) {} void g() { f(null); } } }";
  expect(diagnose(code)).toContain(NULL_INTO_NONNULL);
});

test("@NullUnmarked on a type opts out of a @NullMarked package", () => {
  const code =
    "@NullMarked package p;\n@NullUnmarked class C { void f(String s) {} void g() { f(null); } }";
  expect(diagnose(code)).not.toContain(NULL_INTO_NONNULL);
});

test("generic nullness is off when no options are passed", () => {
  const code = `${BOX}class C { void g(Box<@NonNull String> b) { b.put(null); } }`;
  expect(diagnose(code, null)).not.toContain(NULL_INTO_NONNULL);
});

test("a custom @Nullable annotation list (JSR-305 @CheckForNull) is honored", () => {
  const code =
    "class C { @CheckForNull String n() { return n(); } void f(@NonNull String s) {} void g() { f(n()); } }";
  const config: NullnessOptions = {
    ...JSPECIFY,
    nullableAnnotations: ["javax.annotation.CheckForNull"],
  };
  expect(diagnose(code, config)).toContain(NULL_INTO_NONNULL);
});

test("a @NullMarked sub-package does not mark its parent package", () => {
  const codes = diagnoseFiles(
    {
      "file:///a/b/package-info.java": "@NullMarked package a.b;",
      "file:///a/C.java": "package a; class C { void f(String s) {} void g() { f(null); } }",
    },
    "file:///a/C.java",
  );
  expect(codes).not.toContain(NULL_INTO_NONNULL);
});

// --- flow-aware narrowing ----------------------------------------------------------

// A class with non-null sinks; `m`'s body is spliced in around a @Nullable local x.
const NARROW = (body: string): string =>
  `import java.util.Objects;
class C {
  void f(@NonNull String s) {}
  boolean ok(@NonNull String s) { return true; }
  String use(@NonNull String s) { return s; }
  void h(@NonNull Object o) {}
  @Nullable String src() { return src(); }
  void m() { @Nullable String x = src(); ${body} }
}`;

test("an if (x != null) guard narrows x to non-null in the then-branch", () => {
  expect(diagnose(NARROW("if (x != null) { f(x); }"))).not.toContain(NULL_INTO_NONNULL);
});

test("an early-return on null narrows x for the rest of the block", () => {
  expect(diagnose(NARROW("if (x == null) return; f(x);"))).not.toContain(NULL_INTO_NONNULL);
});

test("a && short-circuit narrows x in the right operand", () => {
  expect(diagnose(NARROW("boolean b = x != null && ok(x);"))).not.toContain(NULL_INTO_NONNULL);
});

test("a || short-circuit narrows x in the right operand", () => {
  expect(diagnose(NARROW("boolean b = x == null || ok(x);"))).not.toContain(NULL_INTO_NONNULL);
});

test("a ternary condition narrows x in the whenTrue arm", () => {
  expect(diagnose(NARROW('String r = x != null ? use(x) : "";'))).not.toContain(NULL_INTO_NONNULL);
});

test("an instanceof check narrows x to non-null", () => {
  expect(diagnose(NARROW("if (x instanceof String) { h(x); }"))).not.toContain(NULL_INTO_NONNULL);
});

test("Objects.requireNonNull narrows x for the rest of the block", () => {
  expect(diagnose(NARROW("Objects.requireNonNull(x); f(x);"))).not.toContain(NULL_INTO_NONNULL);
});

test("assert x != null narrows x for the rest of the block", () => {
  expect(diagnose(NARROW("assert x != null; f(x);"))).not.toContain(NULL_INTO_NONNULL);
});

test("reassigning x to a non-null value narrows it", () => {
  expect(diagnose(NARROW('x = "y"; f(x);'))).not.toContain(NULL_INTO_NONNULL);
});

test("a use before the guard is still flagged", () => {
  expect(diagnose(NARROW("f(x); if (x != null) {}"))).toContain(NULL_INTO_NONNULL);
});

test("reassigning x to null then using it is flagged", () => {
  expect(diagnose(NARROW("if (x == null) return; x = null; f(x);"))).toContain(NULL_INTO_NONNULL);
});

test("the wrong branch (then of x == null) does not narrow to non-null", () => {
  expect(diagnose(NARROW("if (x == null) { f(x); }"))).toContain(NULL_INTO_NONNULL);
});

test("a reassignment between a guard and the use invalidates the guard", () => {
  const code = NARROW("if (x != null) { x = src(); f(x); }");
  expect(diagnose(code)).toContain(NULL_INTO_NONNULL);
});

test("fields are not narrowed by a guard", () => {
  const code =
    "class C { @Nullable String fld; void f(@NonNull String s) {} void m() { if (fld != null) { f(fld); } } }";
  expect(diagnose(code)).toContain(NULL_INTO_NONNULL);
});

// --- narrowing: condition forms ----------------------------------------------------

const NARROWED_OK: ReadonlyArray<readonly [string, string]> = [
  ["negation !(x == null)", "if (!(x == null)) { f(x); }"],
  ["else of (x == null)", "if (x == null) {} else { f(x); }"],
  ["Objects.nonNull condition", "if (Objects.nonNull(x)) { f(x); }"],
  ["Objects.isNull else-branch", "if (Objects.isNull(x)) {} else { f(x); }"],
  ["null on the left (null != x)", "if (null != x) { f(x); }"],
  ["early-exit via throw", "if (x == null) throw new RuntimeException(); f(x);"],
  ["early-exit via break", "for (;;) { if (x == null) break; f(x); }"],
  ["early-exit via continue", "for (;;) { if (x == null) continue; f(x); }"],
  ["block-bodied early-exit", "if (x == null) { System.out.println(); return; } f(x);"],
  ["ternary whenFalse arm", 'String r = x == null ? "" : use(x);'],
  [
    "&&-chain of three operands",
    "@Nullable String y = src(); boolean b = y != null && x != null && ok(x);",
  ],
  ["requireNonNull with a message arg", 'Objects.requireNonNull(x, "m"); f(x);'],
  ["assert with a message", 'assert x != null : "m"; f(x);'],
];

for (const [name, body] of NARROWED_OK) {
  test(`narrowing accepts: ${name}`, () => {
    expect(diagnose(NARROW(body))).not.toContain(NULL_INTO_NONNULL);
  });
}

// --- narrowing: loop conditions ----------------------------------------------------

test("a while-loop condition narrows x in the body", () => {
  expect(diagnose(NARROW("while (x != null) { f(x); break; }"))).not.toContain(NULL_INTO_NONNULL);
});

test("a for-loop condition narrows x in the body", () => {
  expect(diagnose(NARROW("for (; x != null; ) { f(x); break; }"))).not.toContain(NULL_INTO_NONNULL);
});

test("a do-while condition does NOT narrow the body (it runs once first)", () => {
  // The first iteration runs before the condition is tested, so narrowing here
  // would be unsound - the warning is correct.
  expect(diagnose(NARROW("do { f(x); break; } while (x != null);"))).toContain(NULL_INTO_NONNULL);
});

test("a reassignment inside the loop body invalidates the loop-condition narrowing", () => {
  const code = NARROW("while (x != null) { x = src(); f(x); break; }");
  expect(diagnose(code)).toContain(NULL_INTO_NONNULL);
});

// --- narrowing: branch merge -------------------------------------------------------

test("if (x == null) x = default; merges both paths to non-null", () => {
  expect(diagnose(NARROW('if (x == null) x = "d"; f(x);'))).not.toContain(NULL_INTO_NONNULL);
});

test("if (x == null) { x = default; } (block form) merges to non-null", () => {
  expect(diagnose(NARROW('if (x == null) { x = "d"; } f(x);'))).not.toContain(NULL_INTO_NONNULL);
});

test("if (x == null) x = maybeNull(); does not prove non-null (still flagged)", () => {
  expect(diagnose(NARROW("if (x == null) x = src(); f(x);"))).toContain(NULL_INTO_NONNULL);
});

// --- dereference of a possibly-null value ------------------------------------------

test("calling a method on a @Nullable receiver is flagged", () => {
  expect(diagnose("class C { void m(@Nullable String x) { x.trim(); } }")).toContain(DEREF);
});

test("accessing a field on a @Nullable receiver is flagged", () => {
  const code = "class A { int v; }\nclass C { void m(@Nullable A a) { int n = a.v; } }";
  expect(diagnose(code)).toContain(DEREF);
});

test("indexing a @Nullable array is flagged", () => {
  expect(diagnose("class C { void m(@Nullable String[] arr) { String s = arr[0]; } }")).toContain(
    DEREF,
  );
});

test("a guard narrows the receiver so the dereference is accepted", () => {
  const code = "class C { void m(@Nullable String x) { if (x != null) x.trim(); } }";
  expect(diagnose(code)).not.toContain(DEREF);
});

test("an early-return narrows the receiver for a later dereference", () => {
  const code = "class C { void m(@Nullable String x) { if (x == null) return; x.trim(); } }";
  expect(diagnose(code)).not.toContain(DEREF);
});

test("a @NonNull receiver is not flagged", () => {
  expect(diagnose("class C { void m(@NonNull String x) { x.trim(); } }")).not.toContain(DEREF);
});

test("a this-qualified access is not flagged as a dereference", () => {
  expect(diagnose("class C { String fld; void m() { this.fld.trim(); } }")).not.toContain(DEREF);
});

test("throwing a @Nullable value is flagged", () => {
  expect(diagnose("class C { void m(@Nullable RuntimeException e) { throw e; } }")).toContain(
    DEREF,
  );
});

test("synchronizing on a @Nullable lock is flagged", () => {
  expect(
    diagnose("class C { void m(@Nullable Object lock) { synchronized (lock) {} } }"),
  ).toContain(DEREF);
});

test("iterating a @Nullable collection in an enhanced-for is flagged", () => {
  const code = "class C { void m(@Nullable java.util.List<String> xs) { for (String s : xs) {} } }";
  expect(diagnose(code)).toContain(DEREF);
});

test("switching on a @Nullable selector without a `case null` is flagged", () => {
  const code =
    'class C { void m(@Nullable String x) { switch (x) { case "a" -> {} default -> {} } } }';
  expect(diagnose(code)).toContain(DEREF);
});

test("a `case null` label (JEP 441) handles null so the switch selector is accepted", () => {
  const code =
    'class C { void m(@Nullable String x) { switch (x) { case null -> {} case "a" -> {} default -> {} } } }';
  expect(diagnose(code)).not.toContain(DEREF);
});

test("a switch expression on a @Nullable selector without `case null` is flagged", () => {
  const code =
    'class C { int m(@Nullable String x) { return switch (x) { case "a" -> 1; default -> 0; }; } }';
  expect(diagnose(code)).toContain(DEREF);
});

test("a guard narrows a thrown @Nullable value", () => {
  const code = "class C { void m(@Nullable RuntimeException e) { if (e != null) throw e; } }";
  expect(diagnose(code)).not.toContain(DEREF);
});

test("dereference checks are off when nullness is disabled", () => {
  const code = "class C { void m(@Nullable String x) { x.trim(); } }";
  expect(diagnose(code, null)).not.toContain(DEREF);
});

// --- final-field narrowing ---------------------------------------------------------

test("a guard on a final @Nullable field narrows this.f for a later dereference", () => {
  const code =
    "class C { final @Nullable String f = null; void m() { if (this.f != null) this.f.trim(); } }";
  expect(diagnose(code)).not.toContain(DEREF);
});

test("a guard on a final @Nullable field narrows the bare field reference", () => {
  const code = "class C { final @Nullable String f = null; void m() { if (f != null) f.trim(); } }";
  expect(diagnose(code)).not.toContain(DEREF);
});

test("a non-final @Nullable field is not narrowed by a guard (could be reassigned)", () => {
  const code =
    "class C { @Nullable String f = null; void m() { if (this.f != null) this.f.trim(); } }";
  expect(diagnose(code)).toContain(DEREF);
});

test("a final @Nullable field dereferenced without a guard is still flagged", () => {
  const code = "class C { final @Nullable String f = null; void m() { this.f.trim(); } }";
  expect(diagnose(code)).toContain(DEREF);
});

test("a guard on one receiver's final field does not narrow another receiver's", () => {
  // a.f and b.f share the field symbol; narrowing must stay this-receiver only.
  const code =
    "class C { final @Nullable String f = null; void m(C a, C b) { if (a.f != null) b.f.trim(); } }";
  expect(diagnose(code)).toContain(DEREF);
});

test("a final @Nullable record component is narrowed after a guard inside the record", () => {
  const code =
    "record R(@Nullable String name) { void m() { if (this.name != null) this.name.trim(); } }";
  expect(diagnose(code)).not.toContain(DEREF);
});

// --- record construction -----------------------------------------------------------

test("new R(null) into a non-null record component is flagged", () => {
  const code = "@NullMarked record R(String x) {}\nclass C { void m() { new R(null); } }";
  expect(diagnose(code)).toContain(NULL_INTO_NONNULL);
});

test("new R(null) into a @Nullable record component is accepted", () => {
  const code = "record R(@Nullable String x) {}\nclass C { void m() { new R(null); } }";
  expect(diagnose(code)).not.toContain(NULL_INTO_NONNULL);
});

test("a non-null argument into a non-null record component is accepted", () => {
  const code = '@NullMarked record R(String x) {}\nclass C { void m() { new R("a"); } }';
  expect(diagnose(code)).not.toContain(NULL_INTO_NONNULL);
});

// --- try/catch flow ----------------------------------------------------------------

test("requireNonNull in a try whose catch exits narrows after the try", () => {
  const code =
    "class C { void m(@Nullable String x) { try { java.util.Objects.requireNonNull(x); } catch (RuntimeException e) { return; } x.trim(); } }";
  expect(diagnose(code)).not.toContain(DEREF);
});

test("a catch that falls through does not narrow after the try", () => {
  const code =
    "class C { void m(@Nullable String x) { try { java.util.Objects.requireNonNull(x); } catch (RuntimeException e) {} x.trim(); } }";
  expect(diagnose(code)).toContain(DEREF);
});

test("an assignment in a try whose catch exits narrows after the try", () => {
  // Reaching after the try means it completed normally, so the assignment ran.
  const code =
    'class C { void m(@Nullable String x) { try { x = "a"; } catch (RuntimeException e) { return; } x.trim(); } }';
  expect(diagnose(code)).not.toContain(DEREF);
});

test("an assignment in a try with a fall-through catch does not narrow after the try", () => {
  const code =
    'class C { void m(@Nullable String x) { try { x = "a"; } catch (RuntimeException e) {} x.trim(); } }';
  expect(diagnose(code)).toContain(DEREF);
});

test("a guard before a try narrows inside the try body", () => {
  const code =
    "class C { void m(@Nullable String x) { if (x == null) return; try { x.trim(); } catch (RuntimeException e) {} } }";
  expect(diagnose(code)).not.toContain(DEREF);
});

// --- examples/nullness-app ---------------------------------------------------------

test("examples/nullness-app flags exactly the one documented line", () => {
  const main = readFileSync(
    join(import.meta.dirname, "../../examples/nullness-app/src/main/java/example/Main.java"),
    "utf8",
  );
  const codes = diagnose(main);
  // shout(lookup("greeting")) is the single intended warning; the narrowed branch
  // and everything else stay quiet.
  expect(codes.filter(c => c === NULL_INTO_NONNULL)).toHaveLength(1);
});
