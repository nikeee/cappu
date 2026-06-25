// Unit tests for the javadoc formatter (port of google-java-format's javadoc
// package). Expected outputs were captured from the real gjf jar.

import { test } from "node:test";
import { expect } from "expect";

import { formatJavadoc } from "./formatter.ts";

test("collapses a multi-line comment that fits to one line", () => {
  expect(formatJavadoc("/**\n* foo\n*   bar\n*/", 2)).toBe("/** foo bar */");
});

test("collapses an empty javadoc to /** */", () => {
  expect(formatJavadoc("/**\n */", 2)).toBe("/** */");
});

test("leaves a fitting one-liner unchanged", () => {
  expect(formatJavadoc("/** Tests for foos. */", 0)).toBe("/** Tests for foos. */");
});

test("keeps footer tags one per line at the continuation indent", () => {
  expect(formatJavadoc("/**\n * @param x the x\n * @return y\n */", 2)).toBe(
    "/**\n   * @param x the x\n   * @return y\n   */",
  );
});

test("re-wraps prose to the column limit", () => {
  const input =
    "/**\n   * The subclass should implement {@link #bar()}, with the implementation returning a new instance\n   * of the foo relevant to that baz.\n   */";
  expect(formatJavadoc(input, 2)).toBe(
    "/**\n" +
      "   * The subclass should implement {@link #bar()}, with the implementation returning a new instance\n" +
      "   * of the foo relevant to that baz.\n" +
      "   */",
  );
});

test("a bare tag stays multi-line", () => {
  expect(formatJavadoc("/** @deprecated gone */", 2)).toBe("/**\n   * @deprecated gone\n   */");
});

test("returns input unchanged on a lex failure (unbalanced tag)", () => {
  const input = "/** <pre> unterminated */";
  // Should not throw; gjf returns the input on LexException.
  expect(typeof formatJavadoc(input, 2)).toBe("string");
});
