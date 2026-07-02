import { test } from "node:test";

import { expect } from "expect";

import { type ConstValue, foldConstant } from "./constfold.ts";
import { forEachChild } from "./parser.ts";
import { parseSourceFile } from "./parser.ts";
import { type Node, type ReturnStatement, SyntaxKind } from "./types.ts";

// Fold the expression in the `return <expr>;` of a tiny method.
function fold(expr: string): ConstValue | undefined {
  const sf = parseSourceFile("T.java", `class T { Object m() { return ${expr}; } }`);
  let result: ConstValue | undefined;
  const walk = (n: Node): void => {
    if (n.kind === SyntaxKind.ReturnStatement) {
      const e = (n as ReturnStatement).expression;
      if (e) result = foldConstant(e);
    }
    forEachChild(n, child => {
      walk(child);
      return undefined;
    });
  };
  walk(sf);
  return result;
}

test("arithmetic folds with int wraparound", () => {
  expect(fold("6 * 7")).toEqual({ kind: "int", value: 42n });
  expect(fold("10 / 3 + 7 % 4")).toEqual({ kind: "int", value: 6n });
  expect(fold("(1 + 2) * (3 + 4)")).toEqual({ kind: "int", value: 21n });
  expect(fold("-(2 + 3)")).toEqual({ kind: "int", value: -5n });
  expect(fold("2147483647 + 1")).toEqual({ kind: "int", value: -2147483648n }); // overflow wraps
});

test("long arithmetic stays 64-bit", () => {
  expect(fold("100L * 100L")).toEqual({ kind: "long", value: 10000n });
  expect(fold("1L << 40")).toEqual({ kind: "long", value: 1099511627776n });
});

test("shifts and bitwise", () => {
  expect(fold("1 << 10")).toEqual({ kind: "int", value: 1024n });
  expect(fold("-1 >>> 28")).toEqual({ kind: "int", value: 15n });
  expect(fold("12 & 10")).toEqual({ kind: "int", value: 8n });
  expect(fold("5 ^ 3")).toEqual({ kind: "int", value: 6n });
});

test("comparisons and boolean logic fold to boolean", () => {
  expect(fold("3 < 5")).toEqual({ kind: "boolean", value: true });
  expect(fold("3 >= 5")).toEqual({ kind: "boolean", value: false });
  expect(fold("true && false")).toEqual({ kind: "boolean", value: false });
  expect(fold("true || false")).toEqual({ kind: "boolean", value: true });
  expect(fold("!false")).toEqual({ kind: "boolean", value: true });
});

test("int arithmetic overflows in 32-bit two's complement", () => {
  expect(fold("2147483647 + 1")).toEqual({ kind: "int", value: -2147483648n }); // MAX+1 -> MIN
  expect(fold("-2147483648 - 1")).toEqual({ kind: "int", value: 2147483647n }); // MIN-1 -> MAX
  expect(fold("2147483647 * 2")).toEqual({ kind: "int", value: -2n });
  expect(fold("-2147483648")).toEqual({ kind: "int", value: -2147483648n }); // the MIN literal form
  expect(fold("-(-2147483648)")).toEqual({ kind: "int", value: -2147483648n }); // negating MIN wraps
  expect(fold("-2147483648 / -1")).toEqual({ kind: "int", value: -2147483648n }); // overflow, no throw
});

test("long arithmetic overflows in 64-bit two's complement", () => {
  expect(fold("9223372036854775807L + 1L")).toEqual({
    kind: "long",
    value: -9223372036854775808n,
  });
  expect(fold("9223372036854775807L * 2L")).toEqual({ kind: "long", value: -2n });
  expect(fold("1L << 40")).toEqual({ kind: "long", value: 1099511627776n });
});

test("shift distance is masked (low 5 bits for int, 6 for long)", () => {
  expect(fold("1 << 32")).toEqual({ kind: "int", value: 1n }); // 32 & 31 == 0
  expect(fold("1 << 33")).toEqual({ kind: "int", value: 2n });
  expect(fold("1L << 64")).toEqual({ kind: "long", value: 1n }); // 64 & 63 == 0
  expect(fold("-8 >> 1")).toEqual({ kind: "int", value: -4n }); // arithmetic: keeps sign
  expect(fold("-8 >>> 1")).toEqual({ kind: "int", value: 2147483644n }); // logical: zero-fills
  expect(fold("-8L >> 1")).toEqual({ kind: "long", value: -4n }); // arithmetic over 64 bits
  expect(fold("-8L >>> 1")).toEqual({ kind: "long", value: 9223372036854775804n }); // logical, 64-bit
});

test("mixed int/long promotes to long", () => {
  expect(fold("1000000 * 1000000L")).toEqual({ kind: "long", value: 1000000000000n });
  expect(fold("2147483647 + 1L")).toEqual({ kind: "long", value: 2147483648n }); // no int overflow
});

test("hex and binary literals fold (a-f are digits, not float suffixes)", () => {
  expect(fold("0xff")).toEqual({ kind: "int", value: 255n });
  expect(fold("0xe")).toEqual({ kind: "int", value: 14n }); // not an exponent
  expect(fold("0xd")).toEqual({ kind: "int", value: 13n }); // not a double suffix
  expect(fold("0xff + 1")).toEqual({ kind: "int", value: 256n });
  expect(fold("0xFFFFFFFF")).toEqual({ kind: "int", value: -1n }); // 32-bit wrap
  expect(fold("0xFFL")).toEqual({ kind: "long", value: 255n });
  expect(fold("0b1010")).toEqual({ kind: "int", value: 10n });
  expect(fold("0x1.8p1")).toBeUndefined(); // hex floating-point: not an int constant
});

test("non-constant expressions and divide-by-zero do not fold", () => {
  expect(fold("m()")).toBeUndefined();
  expect(fold("1 / 0")).toBeUndefined(); // left for the JVM to throw at runtime
  expect(fold("1.5 + 2.5")).toBeUndefined(); // float folding not implemented
});

test("every comparison operator folds", () => {
  expect(fold("5 > 3")).toEqual({ kind: "boolean", value: true });
  expect(fold("3 > 5")).toEqual({ kind: "boolean", value: false });
  expect(fold("3 <= 3")).toEqual({ kind: "boolean", value: true });
  expect(fold("5 == 5")).toEqual({ kind: "boolean", value: true });
  expect(fold("5 == 6")).toEqual({ kind: "boolean", value: false });
  expect(fold("5 != 3")).toEqual({ kind: "boolean", value: true });
  expect(fold("5 != 5")).toEqual({ kind: "boolean", value: false });
});

test("unary plus and bitwise complement fold; non-numeric operands do not", () => {
  expect(fold("+5")).toEqual({ kind: "int", value: 5n });
  expect(fold("~5")).toEqual({ kind: "int", value: -6n });
  expect(fold("~0")).toEqual({ kind: "int", value: -1n });
  expect(fold("~5L")).toEqual({ kind: "long", value: -6n });
  expect(fold("~true")).toBeUndefined(); // complement needs a numeric operand
  expect(fold("+true")).toBeUndefined(); // unary plus needs a numeric operand
  expect(fold("-true")).toBeUndefined(); // unary minus needs a numeric operand
  expect(fold("!5")).toBeUndefined(); // logical NOT needs a boolean operand
});

test("boolean bitwise operators fold to boolean", () => {
  expect(fold("true | false")).toEqual({ kind: "boolean", value: true });
  expect(fold("true & true")).toEqual({ kind: "boolean", value: true });
  expect(fold("true ^ true")).toEqual({ kind: "boolean", value: false });
  expect(fold("true ^ false")).toEqual({ kind: "boolean", value: true });
  expect(fold("true == false")).toEqual({ kind: "boolean", value: false });
  expect(fold("true != false")).toEqual({ kind: "boolean", value: true });
});

test("integer bitwise-or and signed modulo fold", () => {
  expect(fold("5 | 3")).toEqual({ kind: "int", value: 7n });
  expect(fold("-10 % 3")).toEqual({ kind: "int", value: -1n }); // sign of the dividend
  expect(fold("10 % -3")).toEqual({ kind: "int", value: 1n });
  expect(fold("5 << 0")).toEqual({ kind: "int", value: 5n }); // no-op shift
});

test("mixed int/long bitwise promotes to long", () => {
  expect(fold("5 & 3L")).toEqual({ kind: "long", value: 1n });
  expect(fold("5 | 3L")).toEqual({ kind: "long", value: 7n });
  expect(fold("5 ^ 3L")).toEqual({ kind: "long", value: 6n });
});

test("large octal long literal folds exactly (no float round-trip)", () => {
  // 0777777777777777777777 = 2^63 - 1; Number.parseInt would round it.
  expect(fold("0777777777777777777777L")).toEqual({ kind: "long", value: 9223372036854775807n });
  // 2^62 + 1: one above float53 precision, still positive in int64.
  expect(fold("0400000000000000000001L")).toEqual({ kind: "long", value: 4611686018427387905n });
});
