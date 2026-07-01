import { test } from "node:test";

import { expect } from "expect";

import { createChecker } from "./checker.ts";
import { Diagnostics } from "./diagnostics.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { createProgram } from "./program.ts";
import type { Uri } from "./program.ts";

const TOO_FEW = Diagnostics.Format_not_enough_arguments_0_1.code;
const TOO_MANY = Diagnostics.Format_too_many_arguments_0_1.code;
const BAD_TYPE = Diagnostics.Format_conversion_incompatible_0_1.code;

// Wrap a snippet in a method body so the format call resolves against the stub.
function diagnose(body: string): number[] {
  const program = createProgram();
  loadJdkStub(program);
  const uri = "file:///T.java" as Uri;
  const src = `import java.io.PrintWriter; import java.util.Locale; class C { void m(PrintWriter pw) { ${body} } }`;
  program.setOpenDocument(uri, src, 1);
  const checker = createChecker(program);
  const format = new Set<number>([TOO_FEW, TOO_MANY, BAD_TYPE]);
  return checker
    .getSemanticDiagnostics(program.getSourceFile(uri)!)
    .map(d => d.code)
    .filter(c => format.has(c));
}

test("String.format with too few arguments is flagged", () => {
  expect(diagnose('String.format("%s %s", "a");')).toContain(TOO_FEW);
});

test("String.format with too many arguments is flagged", () => {
  expect(diagnose('String.format("%s", "a", "b");')).toContain(TOO_MANY);
});

test("String.format with the right count is silent", () => {
  expect(diagnose('String.format("%s %s", "a", "b");')).toEqual([]);
});

test("%% and %n do not consume arguments", () => {
  expect(diagnose('String.format("%s 100%%%n", "a");')).toEqual([]);
});

test("positional index counts the highest reference", () => {
  expect(diagnose('String.format("%2$s %1$s", "a");')).toContain(TOO_FEW);
  expect(diagnose('String.format("%2$s %1$s", "a", "b");')).toEqual([]);
});

test("%d with a String argument is a type mismatch", () => {
  expect(diagnose('String.format("%d", "a");')).toContain(BAD_TYPE);
});

test("%d with an int argument is silent", () => {
  expect(diagnose('String.format("%d", 1);')).toEqual([]);
});

test("%s accepts any type", () => {
  expect(diagnose('String.format("%s", 1);')).toEqual([]);
});

test("System.out.printf is checked like format", () => {
  expect(diagnose('System.out.printf("%s %s", "a");')).toContain(TOO_FEW);
});

test("PrintWriter.format is checked", () => {
  expect(diagnose('pw.format("%d", "a");')).toContain(BAD_TYPE);
});

test("String.formatted uses the receiver as the format string", () => {
  expect(diagnose('"%s %s".formatted("a");')).toContain(TOO_FEW);
  expect(diagnose('"%s".formatted("a", "b");')).toContain(TOO_MANY);
});

test("Locale-first String.format overload is handled", () => {
  expect(diagnose('String.format(Locale.US, "%s %s", "a");')).toContain(TOO_FEW);
  expect(diagnose('String.format(Locale.US, "%s", "a");')).toEqual([]);
});

test("a non-literal format string is not analyzed", () => {
  expect(diagnose('String fmt = "%s %s"; String.format(fmt, "a");')).toEqual([]);
});

test("a malformed format string is not analyzed", () => {
  expect(diagnose('String.format("%z", "a");')).toEqual([]);
});
