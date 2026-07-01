import { test } from "node:test";

import { expect } from "expect";

import { createChecker } from "./checker.ts";
import { Diagnostics } from "./diagnostics.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { createProgram } from "./program.ts";
import type { Uri } from "../workspace.ts";

const BAD_REGEX = Diagnostics.Invalid_regular_expression_0.code;
const BAD_LETTER = Diagnostics.Invalid_date_time_pattern_letter_0.code;
const FOOTGUN = Diagnostics.Suspicious_date_time_pattern_letter_0_1_2.code;
const BAD_NUMBER = Diagnostics.String_0_is_not_a_valid_1.code;
const BAD_RADIX = Diagnostics.Radix_0_out_of_range.code;
const TOO_FEW = Diagnostics.Format_not_enough_arguments_0_1.code;

const WANTED = new Set<number>([BAD_REGEX, BAD_LETTER, FOOTGUN, BAD_NUMBER, BAD_RADIX, TOO_FEW]);

function diagnose(body: string): number[] {
  const program = createProgram();
  loadJdkStub(program);
  const uri = "file:///T.java" as Uri;
  const src =
    "import java.util.regex.Pattern; import java.time.format.DateTimeFormatter;" +
    ` class C { void m() { ${body} } }`;
  program.setOpenDocument(uri, src, 1);
  const checker = createChecker(program);
  return checker
    .getSemanticDiagnostics(program.getSourceFile(uri)!)
    .map(d => d.code)
    .filter(c => WANTED.has(c));
}

// --- regex --------------------------------------------------------------------

test("an unbalanced regex literal is flagged", () => {
  expect(diagnose('Pattern.compile("(foo");')).toContain(BAD_REGEX);
  expect(diagnose('"x".split("[");')).toContain(BAD_REGEX);
  expect(diagnose('"x".replaceAll("a(", "b");')).toContain(BAD_REGEX);
});

test("a valid regex literal is silent", () => {
  expect(diagnose('Pattern.compile("(foo)+");')).toEqual([]);
});

test("a non-literal regex is not analyzed", () => {
  expect(diagnose('String r = "(foo"; Pattern.compile(r);')).toEqual([]);
});

// --- date/time ----------------------------------------------------------------

test("an unknown date/time pattern letter is flagged", () => {
  expect(diagnose('DateTimeFormatter.ofPattern("yyyy-jj");')).toContain(BAD_LETTER);
});

test("the Y/D/h footguns are flagged", () => {
  expect(diagnose('DateTimeFormatter.ofPattern("YYYY-MM-dd");')).toContain(FOOTGUN);
});

test("a correct date/time pattern is silent", () => {
  expect(diagnose('DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm:ss");')).toEqual([]);
});

// --- number parsing -----------------------------------------------------------

test("a non-numeric literal is flagged", () => {
  expect(diagnose('Integer.parseInt("12a");')).toContain(BAD_NUMBER);
  expect(diagnose('Long.parseLong("x");')).toContain(BAD_NUMBER);
});

test("radix is respected", () => {
  expect(diagnose('Integer.parseInt("FF");')).toContain(BAD_NUMBER); // F invalid base 10
  expect(diagnose('Integer.parseInt("FF", 16);')).toEqual([]);
});

test("an out-of-range radix is flagged", () => {
  expect(diagnose('Integer.parseInt("1", 99);')).toContain(BAD_RADIX);
});

test("a valid parse is silent", () => {
  expect(diagnose('Integer.parseInt("-42");')).toEqual([]);
});

// --- format family extension --------------------------------------------------

test("Console.printf is checked like format", () => {
  expect(diagnose('System.console().printf("%s %s", "a");')).toContain(TOO_FEW);
});
