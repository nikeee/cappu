import { test } from "node:test";

import { expect } from "expect";

import { conversionAccepts, parseFormatString } from "./formatString.ts";

test("counts ordinary specifiers", () => {
  expect(parseFormatString("%s %s %d")?.maxIndex).toBe(3);
  expect(parseFormatString("hello")?.maxIndex).toBe(0);
});

test("%% and %n consume no argument", () => {
  expect(parseFormatString("100%% done%n%s")?.maxIndex).toBe(1);
});

test("flags, width and precision are skipped", () => {
  expect(parseFormatString("%-10.2f [%03d] %+,d")?.maxIndex).toBe(3);
});

test("explicit argument index", () => {
  const p = parseFormatString("%2$s %1$s");
  expect(p?.maxIndex).toBe(2);
  expect(p?.consumers.map(c => c.argIndex)).toEqual([2, 1]);
});

test("relative '<' reuses the previous index", () => {
  const p = parseFormatString("%s %<S");
  expect(p?.maxIndex).toBe(1);
  expect(p?.consumers.map(c => c.argIndex)).toEqual([1, 1]);
});

test("date/time conversions take a suffix and one argument", () => {
  const p = parseFormatString("%tY-%tm");
  expect(p?.maxIndex).toBe(2);
  expect(p?.consumers.map(c => c.conversion)).toEqual(["t", "t"]);
});

test("malformed strings return undefined", () => {
  expect(parseFormatString("trailing %")).toBeUndefined();
  expect(parseFormatString("%z bad")).toBeUndefined(); // unknown conversion
  expect(parseFormatString("%t")).toBeUndefined(); // missing date suffix
  expect(parseFormatString("%<s")).toBeUndefined(); // '<' with no previous
  expect(parseFormatString("%0$s")).toBeUndefined(); // zero index
});

test("conversionAccepts: general conversions accept anything", () => {
  expect(conversionAccepts("s", { fqn: "java.lang.Object" })).toBe("unknown");
  expect(conversionAccepts("b", { primitive: "int" })).toBe("unknown");
});

test("conversionAccepts: primitives are fully decidable", () => {
  expect(conversionAccepts("d", { primitive: "int" })).toBe("yes");
  expect(conversionAccepts("d", { primitive: "double" })).toBe("no");
  expect(conversionAccepts("f", { primitive: "double" })).toBe("yes");
  expect(conversionAccepts("f", { primitive: "int" })).toBe("no");
  expect(conversionAccepts("c", { primitive: "char" })).toBe("yes");
  expect(conversionAccepts("c", { primitive: "boolean" })).toBe("no");
});

test("conversionAccepts: reference types only when provably a leaf", () => {
  expect(conversionAccepts("d", { fqn: "java.lang.Integer" })).toBe("yes");
  expect(conversionAccepts("d", { fqn: "java.lang.String" })).toBe("no");
  expect(conversionAccepts("d", { fqn: "java.lang.Double" })).toBe("no");
  expect(conversionAccepts("f", { fqn: "java.lang.Integer" })).toBe("no");
  // supertypes and user types stay unknown (runtime type could satisfy it)
  expect(conversionAccepts("d", { fqn: "java.lang.Object" })).toBe("unknown");
  expect(conversionAccepts("d", { fqn: "com.example.Money" })).toBe("unknown");
});
