// Compile-time constant folding for primitive constant expressions (JLS 15.28),
// matching javac: an expression built from literals and operators is evaluated at
// compile time so the emitter can push the folded constant. Integer arithmetic
// wraps in two's complement (32-bit int, 64-bit long). Float/double/char/String
// and `final` constant variables are not folded yet.

import {
  type BinaryExpression,
  type LiteralExpression,
  type Node,
  type PrefixUnaryExpression,
  SyntaxKind,
} from "./types.ts";

export type ConstValue =
  | { readonly kind: "int"; readonly value: bigint }
  | { readonly kind: "long"; readonly value: bigint }
  | { readonly kind: "boolean"; readonly value: boolean };

const int32 = (v: bigint): bigint => BigInt.asIntN(32, v);
const int64 = (v: bigint): bigint => BigInt.asIntN(64, v);

function parseIntLiteral(text: string): bigint | undefined {
  const t = text.replace(/_/g, "");
  try {
    if (/^0[0-7]+$/.test(t)) return BigInt(parseInt(t, 8)); // legacy octal
    return BigInt(t); // handles decimal, 0x..., 0b...
  } catch {
    return undefined;
  }
}

function num(value: ConstValue | undefined): { kind: "int" | "long"; value: bigint } | undefined {
  return value && value.kind !== "boolean" ? value : undefined;
}

function wrap(kind: "int" | "long", value: bigint): bigint {
  return kind === "long" ? int64(value) : int32(value);
}

function foldPrefix(node: PrefixUnaryExpression): ConstValue | undefined {
  const operand = foldConstant(node.operand);
  if (!operand) return undefined;
  switch (node.operator) {
    case SyntaxKind.PlusToken:
      return num(operand);
    case SyntaxKind.MinusToken: {
      const n = num(operand);
      return n && { kind: n.kind, value: wrap(n.kind, -n.value) };
    }
    case SyntaxKind.TildeToken: {
      const n = num(operand);
      return n && { kind: n.kind, value: wrap(n.kind, -n.value - 1n) };
    }
    case SyntaxKind.ExclamationToken:
      return operand.kind === "boolean" ? { kind: "boolean", value: !operand.value } : undefined;
    default:
      return undefined;
  }
}

const COMPARE = {
  [SyntaxKind.LessThanToken]: (a, b) => a < b,
  [SyntaxKind.LessThanEqualsToken]: (a, b) => a <= b,
  [SyntaxKind.GreaterThanToken]: (a, b) => a > b,
  [SyntaxKind.GreaterThanEqualsToken]: (a, b) => a >= b,
  [SyntaxKind.EqualsEqualsToken]: (a, b) => a === b,
  [SyntaxKind.ExclamationEqualsToken]: (a, b) => a !== b,
} as const satisfies Partial<Record<SyntaxKind, (a: bigint, b: bigint) => boolean>>;

function foldBinary(node: BinaryExpression): ConstValue | undefined {
  const left = foldConstant(node.left);
  const right = foldConstant(node.right);
  if (!left || !right) return undefined;
  const op = node.operatorToken;

  if (left.kind === "boolean" && right.kind === "boolean") {
    switch (op) {
      case SyntaxKind.AmpersandAmpersandToken:
      case SyntaxKind.AmpersandToken:
        return { kind: "boolean", value: left.value && right.value };
      case SyntaxKind.BarBarToken:
      case SyntaxKind.BarToken:
        return { kind: "boolean", value: left.value || right.value };
      case SyntaxKind.CaretToken:
        return { kind: "boolean", value: left.value !== right.value };
      case SyntaxKind.EqualsEqualsToken:
        return { kind: "boolean", value: left.value === right.value };
      case SyntaxKind.ExclamationEqualsToken:
        return { kind: "boolean", value: left.value !== right.value };
      default:
        return undefined;
    }
  }

  const a = num(left);
  const b = num(right);
  if (!a || !b) return undefined;

  const compare = COMPARE[op as keyof typeof COMPARE];
  if (compare) return { kind: "boolean", value: compare(a.value, b.value) };

  const kind = a.kind === "long" || b.kind === "long" ? "long" : "int";
  switch (op) {
    case SyntaxKind.PlusToken:
      return { kind, value: wrap(kind, a.value + b.value) };
    case SyntaxKind.MinusToken:
      return { kind, value: wrap(kind, a.value - b.value) };
    case SyntaxKind.AsteriskToken:
      return { kind, value: wrap(kind, a.value * b.value) };
    case SyntaxKind.SlashToken:
      return b.value === 0n ? undefined : { kind, value: wrap(kind, a.value / b.value) };
    case SyntaxKind.PercentToken:
      return b.value === 0n ? undefined : { kind, value: wrap(kind, a.value % b.value) };
    case SyntaxKind.AmpersandToken:
      return { kind, value: wrap(kind, a.value & b.value) };
    case SyntaxKind.BarToken:
      return { kind, value: wrap(kind, a.value | b.value) };
    case SyntaxKind.CaretToken:
      return { kind, value: wrap(kind, a.value ^ b.value) };
    case SyntaxKind.LessThanLessThanToken:
    case SyntaxKind.GreaterThanGreaterThanToken:
    case SyntaxKind.GreaterThanGreaterThanGreaterThanToken: {
      // A shift's result type and the distance mask come from the (promoted) left
      // operand only; the right operand never widens the result (JLS 15.19).
      const sk = a.kind;
      const sb = sk === "long" ? 64n : 32n;
      const dist = b.value & (sb - 1n);
      if (op === SyntaxKind.LessThanLessThanToken) {
        return { kind: sk, value: wrap(sk, a.value << dist) };
      }
      if (op === SyntaxKind.GreaterThanGreaterThanToken) {
        return { kind: sk, value: wrap(sk, a.value >> dist) };
      }
      return { kind: sk, value: wrap(sk, BigInt.asUintN(Number(sb), a.value) >> dist) };
    }
    default:
      return undefined;
  }
}

/** Evaluate a primitive constant expression, or undefined if it is not constant. */
export function foldConstant(node: Node): ConstValue | undefined {
  switch (node.kind) {
    case SyntaxKind.ParenthesizedExpression:
      return foldConstant((node as unknown as { expression: Node }).expression);
    case SyntaxKind.NumericLiteral: {
      const text = (node as LiteralExpression).value;
      const t = text.replace(/_/g, "");
      const isHexOrBin = /^0[xXbB]/.test(t);
      // Decimal float/double, or hex floating-point: not an integer constant.
      // (In hex/binary literals a-f are digits, so only guard them on a 'p' exponent.)
      if (isHexOrBin ? /[pP]/.test(t) : /[.eEfFdD]/.test(t)) return undefined;
      const isLong = /[lL]$/.test(text);
      const parsed = parseIntLiteral(isLong ? text.replace(/[lL]$/, "") : text);
      if (parsed === undefined) return undefined;
      return isLong
        ? { kind: "long", value: int64(parsed) }
        : { kind: "int", value: int32(parsed) };
    }
    case SyntaxKind.TrueKeyword:
      return { kind: "boolean", value: true };
    case SyntaxKind.FalseKeyword:
      return { kind: "boolean", value: false };
    case SyntaxKind.PrefixUnaryExpression:
      return foldPrefix(node as PrefixUnaryExpression);
    case SyntaxKind.BinaryExpression:
      return foldBinary(node as BinaryExpression);
    default:
      return undefined;
  }
}
