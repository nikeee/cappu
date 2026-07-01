import { test } from "node:test";

import { expect } from "expect";

import { isParseableInteger } from "./numberParse.ts";

test("valid decimal integers", () => {
  for (const s of ["0", "42", "-7", "+15", "2147483648"]) {
    expect(isParseableInteger(s, 10)).toBe(true);
  }
});

test("invalid decimal integers", () => {
  for (const s of ["", "+", "-", "12a", "1.5", "0x1F", "1_000", " 3"]) {
    expect(isParseableInteger(s, 10)).toBe(false);
  }
});

test("radix respects the digit set", () => {
  expect(isParseableInteger("FF", 16)).toBe(true);
  expect(isParseableInteger("ff", 16)).toBe(true);
  expect(isParseableInteger("1010", 2)).toBe(true);
  expect(isParseableInteger("2", 2)).toBe(false); // 2 is not a binary digit
  expect(isParseableInteger("8", 8)).toBe(false);
});
