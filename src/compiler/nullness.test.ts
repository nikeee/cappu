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
