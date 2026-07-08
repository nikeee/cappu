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
const OPTIONAL_NULL_CHECK =
  Diagnostics.Optional_ofNullable_ifPresent_can_be_replaced_with_a_null_check.code;
const OPTIONAL_GET_UNGUARDED = Diagnostics.Optional_get_0_called_without_an_isPresent_guard.code;
const COUNT_CHECK = Diagnostics.Count_check_0_can_be_replaced_with_1.code;
const STRING_EQ = Diagnostics.Strings_should_be_compared_with_equals_not_0.code;
const BOXING_CTOR = Diagnostics.Boxing_constructor_new_0_is_deprecated.code;
const INDEXOF_CHECK = Diagnostics.IndexOf_check_0_can_be_replaced_with_1.code;
const NEW_STRING = Diagnostics.New_String_0_can_be_replaced_with_1.code;
const EQUALS_EMPTY = Diagnostics.Equals_empty_0_can_be_replaced_with_1.code;
const SELF_COMPARISON = Diagnostics.Suspicious_self_comparison_0.code;
const BOXED_EQ = Diagnostics.Boxed_types_should_be_compared_with_equals_not_0.code;
const EMPTY_CATCH = Diagnostics.Empty_catch_block_for_0.code;
const OPTIONAL_OF_NULL = Diagnostics.Optional_of_null_always_throws.code;
const BOOL_COMPARISON = Diagnostics.Redundant_boolean_comparison_0_can_be_replaced_with_1.code;
const IF_ELSE_BOOL = Diagnostics.If_else_returning_booleans_0_can_be_replaced_with_1.code;
const TERNARY_BOOL = Diagnostics.Ternary_with_boolean_literals_0_can_be_replaced_with_1.code;
const COLLAPSIBLE_IF = Diagnostics.Nested_if_can_be_collapsed_to_if_0.code;
const OPTIONAL_TYPE = Diagnostics._0_1_should_not_be_of_type_Optional.code;
const INDEXED_LOOP = Diagnostics.Indexed_loop_over_0_can_be_a_for_each_loop.code;

const WANTED = new Set<number>([
  BAD_REGEX,
  BAD_LETTER,
  FOOTGUN,
  BAD_NUMBER,
  BAD_RADIX,
  TOO_FEW,
  OPTIONAL_NULL_CHECK,
  OPTIONAL_GET_UNGUARDED,
  COUNT_CHECK,
  STRING_EQ,
  BOXING_CTOR,
  INDEXOF_CHECK,
  NEW_STRING,
  EQUALS_EMPTY,
  SELF_COMPARISON,
  BOXED_EQ,
  EMPTY_CATCH,
  OPTIONAL_OF_NULL,
  BOOL_COMPARISON,
  IF_ELSE_BOOL,
  TERNARY_BOOL,
  COLLAPSIBLE_IF,
  OPTIONAL_TYPE,
  INDEXED_LOOP,
]);

const IMPORTS =
  "import java.util.regex.Pattern; import java.time.format.DateTimeFormatter;" +
  " import java.util.Optional; import java.util.List; import java.util.ArrayList;";

function diagnose(body: string): number[] {
  const program = createProgram();
  loadJdkStub(program);
  const uri = "file:///T.java" as Uri;
  const src = `${IMPORTS} class C { void m() { ${body} } }`;
  program.setOpenDocument(uri, src, 1);
  const checker = createChecker(program);
  return checker
    .getSemanticDiagnostics(program.getSourceFile(uri)!)
    .map(d => d.code)
    .filter(c => WANTED.has(c));
}

// For rules that need custom method signatures/fields/params rather than the
// single fixed `void m()` body above.
function diagnoseClass(classBody: string): number[] {
  const program = createProgram();
  loadJdkStub(program);
  const uri = "file:///T.java" as Uri;
  const src = `${IMPORTS} class C { ${classBody} }`;
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

// --- Optional.ofNullable(x).ifPresent(...) (nikeee/cappu#42) --------------------

test("Optional.ofNullable(x).ifPresent(lambda) is flagged", () => {
  expect(
    diagnose('String s = "x"; Optional.ofNullable(s).ifPresent(v -> System.out.println(v));'),
  ).toContain(OPTIONAL_NULL_CHECK);
});

test("Optional.ofNullable with a non-variable argument is still flagged", () => {
  expect(
    diagnose(
      'String s = "x"; Optional.ofNullable(s.trim()).ifPresent(v -> System.out.println(v));',
    ),
  ).toContain(OPTIONAL_NULL_CHECK);
});

test("Optional.ofNullable(x).ifPresent(method ref) is flagged", () => {
  expect(
    diagnose('String s = "x"; Optional.ofNullable(s).ifPresent(System.out::println);'),
  ).toContain(OPTIONAL_NULL_CHECK);
});

test("Optional.of(x).ifPresent is silent", () => {
  expect(diagnose('String s = "x"; Optional.of(s).ifPresent(v -> System.out.println(v));')).toEqual(
    [],
  );
});

test("Optional.ofNullable without ifPresent is silent", () => {
  expect(diagnose('String s = "x"; Optional.ofNullable(s).map(v -> v);')).toEqual([]);
});

// --- Optional#get() without an isPresent()/isEmpty() guard (nikeee/cappu#42) ---

test("get() without any guard in the method is flagged", () => {
  expect(diagnose("Optional<String> o = Optional.empty(); o.get();")).toContain(
    OPTIONAL_GET_UNGUARDED,
  );
});

test("get() after an isPresent() check on the same variable is silent", () => {
  expect(
    diagnose("Optional<String> o = Optional.empty(); if (o.isPresent()) { o.get(); }"),
  ).toEqual([]);
});

test("get() after an isEmpty() check on the same variable is silent", () => {
  expect(diagnose("Optional<String> o = Optional.empty(); if (!o.isEmpty()) { o.get(); }")).toEqual(
    [],
  );
});

test("get() on a call-expression receiver is not analyzed (unprovable)", () => {
  expect(diagnose('Optional.ofNullable("x").get();')).toEqual([]);
});

test("isPresent() on a different variable does not guard", () => {
  expect(
    diagnose(
      "Optional<String> a = Optional.empty(); Optional<String> b = Optional.empty(); if (b.isPresent()) { a.get(); }",
    ),
  ).toContain(OPTIONAL_GET_UNGUARDED);
});

// --- size()/length() compared to 0/1 -> isEmpty()/!isEmpty() (nikeee/cappu#42) ---

test("size() == 0 is flagged", () => {
  expect(diagnose("List<String> xs = new ArrayList<>(); if (xs.size() == 0) {}")).toContain(
    COUNT_CHECK,
  );
});

test("size() != 0, > 0, < 1, >= 1, <= 0 are all flagged", () => {
  for (const cmp of ["!= 0", "> 0", "< 1", ">= 1", "<= 0"]) {
    expect(diagnose(`List<String> xs = new ArrayList<>(); if (xs.size() ${cmp}) {}`)).toContain(
      COUNT_CHECK,
    );
  }
});

test("literal-on-left is also flagged", () => {
  expect(diagnose("List<String> xs = new ArrayList<>(); if (0 == xs.size()) {}")).toContain(
    COUNT_CHECK,
  );
});

test("String.length() == 0 is flagged", () => {
  expect(diagnose('String s = "x"; if (s.length() == 0) {}')).toContain(COUNT_CHECK);
});

test("size() compared to a non-zero-or-one literal is silent", () => {
  expect(diagnose("List<String> xs = new ArrayList<>(); if (xs.size() == 2) {}")).toEqual([]);
});

test("size() > 1 is silent (not one of the recognized combos)", () => {
  expect(diagnose("List<String> xs = new ArrayList<>(); if (xs.size() > 1) {}")).toEqual([]);
});

// --- == / != on Strings -> equals() (nikeee/cappu#42) --------------------------

test("== on two String-typed operands is flagged", () => {
  expect(diagnose('String a = "x"; String b = "y"; if (a == b) {}')).toContain(STRING_EQ);
});

test("!= on two String-typed operands is flagged", () => {
  expect(diagnose('String a = "x"; String b = "y"; if (a != b) {}')).toContain(STRING_EQ);
});

test("== against a null literal is silent", () => {
  expect(diagnose('String a = "x"; if (a == null) {}')).toEqual([]);
});

test("== on non-String operands is silent", () => {
  expect(diagnose("int a = 1; int b = 2; if (a == b) {}")).toEqual([]);
});

test("== where only one operand is a String is silent", () => {
  expect(diagnose('String a = "x"; Object b = new Object(); if (a == b) {}')).toEqual([]);
});

// --- boxing constructors -> valueOf() (nikeee/cappu#42) ------------------------

test("new Integer(...) is flagged", () => {
  expect(diagnose("Integer i = new Integer(5);")).toContain(BOXING_CTOR);
});

test("new Boolean(...) and new Character(...) are flagged", () => {
  expect(diagnose("Boolean b = new Boolean(true);")).toContain(BOXING_CTOR);
  expect(diagnose("Character c = new Character('x');")).toContain(BOXING_CTOR);
});

test("Integer.valueOf(...) is silent", () => {
  expect(diagnose("Integer i = Integer.valueOf(5);")).toEqual([]);
});

test("new ArrayList<>() (a non-boxing type) is silent", () => {
  expect(diagnose("List<String> xs = new ArrayList<>();")).toEqual([]);
});

// --- indexOf(...) != -1 -> contains(...) (nikeee/cappu#42) ---------------------

test("String.indexOf(x) != -1 is flagged", () => {
  expect(diagnose('String s = "x"; if (s.indexOf("a") != -1) {}')).toContain(INDEXOF_CHECK);
});

test("String.indexOf(x) == -1 is flagged", () => {
  expect(diagnose('String s = "x"; if (s.indexOf("a") == -1) {}')).toContain(INDEXOF_CHECK);
});

test("List.indexOf(x) != -1 is flagged", () => {
  expect(diagnose('List<String> xs = new ArrayList<>(); if (xs.indexOf("a") != -1) {}')).toContain(
    INDEXOF_CHECK,
  );
});

test("literal-on-left -1 != indexOf(x) is flagged", () => {
  expect(diagnose('String s = "x"; if (-1 != s.indexOf("a")) {}')).toContain(INDEXOF_CHECK);
});

test("indexOf(x) compared to a non-negative-one literal is silent", () => {
  expect(diagnose('String s = "x"; if (s.indexOf("a") != 0) {}')).toEqual([]);
});

// --- redundant new String(...) (nikeee/cappu#42) -------------------------------

test("new String() is flagged", () => {
  expect(diagnose("String s = new String();")).toContain(NEW_STRING);
});

test("new String(anotherString) is flagged", () => {
  expect(diagnose('String a = "x"; String b = new String(a);')).toContain(NEW_STRING);
});

test("new String(byte[]) is silent (real conversion)", () => {
  expect(diagnose("byte[] bs = null; String s = new String(bs);")).toEqual([]);
});

// --- equals("") -> isEmpty() (nikeee/cappu#42) ----------------------------------

test('s.equals("") is flagged', () => {
  expect(diagnose('String s = "x"; if (s.equals("")) {}')).toContain(EQUALS_EMPTY);
});

test('"".equals(s) is flagged (warn only)', () => {
  expect(diagnose('String s = "x"; if ("".equals(s)) {}')).toContain(EQUALS_EMPTY);
});

test('s.equals("x") (non-empty literal) is silent', () => {
  expect(diagnose('String s = "x"; if (s.equals("x")) {}')).toEqual([]);
});

test("equals() on a non-String receiver is silent", () => {
  expect(diagnose('Object o = new Object(); if (o.equals("")) {}')).toEqual([]);
});

// --- self-comparison (nikeee/cappu#42) ------------------------------------------

test("x == x is flagged", () => {
  expect(diagnose("int x = 1; if (x == x) {}")).toContain(SELF_COMPARISON);
});

test("x.equals(x) is flagged", () => {
  expect(diagnose('String x = "a"; if (x.equals(x)) {}')).toContain(SELF_COMPARISON);
});

test("x.compareTo(x) is flagged", () => {
  expect(diagnose('String x = "a"; if (x.compareTo(x) == 0) {}')).toContain(SELF_COMPARISON);
});

test("field-access self-comparison this.a == this.a is flagged", () => {
  expect(diagnoseClass("int a; void m() { if (this.a == this.a) {} }")).toContain(SELF_COMPARISON);
});

test("x == y (different variables) is silent", () => {
  expect(diagnose("int x = 1; int y = 2; if (x == y) {}")).toEqual([]);
});

test("calls that happen to read the same text are not flagged (unprovable)", () => {
  expect(diagnoseClass("int next() { return 1; } void m() { if (next() == next()) {} }")).toEqual(
    [],
  );
});

// --- boxed reference == comparison -> equals() (nikeee/cappu#42 follow-up) -----

test("Integer == Integer is flagged", () => {
  expect(diagnose("Integer a = 1000; Integer b = 1000; if (a == b) {}")).toContain(BOXED_EQ);
});

test("Integer != Integer is flagged", () => {
  expect(diagnose("Integer a = 1000; Integer b = 1000; if (a != b) {}")).toContain(BOXED_EQ);
});

test("Integer == int (unboxes safely) is silent", () => {
  expect(diagnose("Integer a = 1000; int b = 1000; if (a == b) {}")).toEqual([]);
});

test("Integer == null is silent", () => {
  expect(diagnose("Integer a = 1000; if (a == null) {}")).toEqual([]);
});

// --- empty catch block (nikeee/cappu#42 follow-up) ------------------------------

test("an empty catch block is flagged", () => {
  expect(diagnose("try { m(); } catch (Exception e) {}")).toContain(EMPTY_CATCH);
});

test("a catch block with a statement is silent", () => {
  expect(diagnose("try { m(); } catch (Exception e) { e.printStackTrace(); }")).toEqual([]);
});

test("a catch block containing only a comment is silent (assumed intentional)", () => {
  expect(diagnose("try { m(); } catch (Exception e) { /* ignored intentionally */ }")).toEqual([]);
});

// --- Optional.of(null) -> ofNullable (nikeee/cappu#42 follow-up) ---------------

test("Optional.of(null) is flagged", () => {
  expect(diagnose("Optional.of(null);")).toContain(OPTIONAL_OF_NULL);
});

test("Optional.of(x) with a non-null literal is silent", () => {
  expect(diagnose('Optional.of("x");')).toEqual([]);
});

test("Optional.ofNullable(null) is silent", () => {
  expect(diagnose("Optional.ofNullable(null);")).toEqual([]);
});
