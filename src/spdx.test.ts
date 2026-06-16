import { test } from "node:test";

import { expect } from "expect";

import { isValidSpdxExpression } from "./spdx.ts";

test("single SPDX ids are accepted", () => {
  for (const id of ["MIT", "Apache-2.0", "GPL-3.0-or-later", "EPL-2.0", "BSD-3-Clause", "ISC"]) {
    expect(isValidSpdxExpression(id)).toBe(true);
  }
});

test("compound expressions, +, parentheses and WITH are accepted", () => {
  expect(isValidSpdxExpression("MIT OR Apache-2.0")).toBe(true);
  expect(isValidSpdxExpression("(MIT OR Apache-2.0)")).toBe(true);
  expect(isValidSpdxExpression("Apache-2.0 AND MIT")).toBe(true);
  expect(isValidSpdxExpression("GPL-2.0-or-later+")).toBe(true);
  expect(isValidSpdxExpression("GPL-2.0-only WITH Classpath-exception-2.0")).toBe(true);
  expect(isValidSpdxExpression("(MIT OR (Apache-2.0 AND ISC))")).toBe(true);
});

test("free text, unknown ids and malformed expressions are rejected", () => {
  expect(isValidSpdxExpression("The Apache Software License, Version 2.0")).toBe(false);
  expect(isValidSpdxExpression("Definitely-Not-A-License")).toBe(false);
  expect(isValidSpdxExpression("")).toBe(false);
  expect(isValidSpdxExpression("MIT OR")).toBe(false);
  expect(isValidSpdxExpression("OR MIT")).toBe(false);
  expect(isValidSpdxExpression("(MIT")).toBe(false);
  expect(isValidSpdxExpression("MIT Apache-2.0")).toBe(false); // missing operator
  expect(isValidSpdxExpression("MIT WITH Bogus-exception")).toBe(false);
});
