import { test } from "node:test";

import { expect } from "expect";

import { validateRegex } from "./regexValidate.ts";

test("valid patterns return undefined", () => {
  for (const re of ["a.c", "[a-z]+", "(foo|bar)*", "\\d{3}", "a\\(b", "[a-z&&[^bc]]", "]"]) {
    expect(validateRegex(re)).toBeUndefined();
  }
});

test("unbalanced groups and classes are reported", () => {
  expect(validateRegex("(foo")).toMatch(/unclosed group/);
  expect(validateRegex("foo)")).toMatch(/unmatched/);
  expect(validateRegex("[abc")).toMatch(/unclosed character class/);
});

test("a trailing backslash is reported", () => {
  expect(validateRegex("abc\\")).toMatch(/trailing backslash/);
});

test("parentheses inside a class are literal, not groups", () => {
  expect(validateRegex("[()]")).toBeUndefined();
});

test("\\Q...\\E literal regions are skipped", () => {
  expect(validateRegex("\\Q(unbalanced\\E")).toBeUndefined();
});
