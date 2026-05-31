import { test } from "node:test";
import { expect } from "expect";

import { createScanner } from "./scanner.ts";
import { SyntaxKind, TokenFlags } from "./types.ts";

interface ScannedToken {
  kind: SyntaxKind;
  value: string;
  text: string;
  start: number;
  end: number;
  flags: TokenFlags;
  precedingLineBreak: boolean;
}

function tokenize(src: string): { tokens: ScannedToken[]; errorCodes: number[] } {
  const errorCodes: number[] = [];
  const scanner = createScanner(src, message => errorCodes.push(message.code));
  const tokens: ScannedToken[] = [];
  let kind = scanner.scan();
  while (kind !== SyntaxKind.EndOfFileToken) {
    tokens.push({
      kind,
      value: scanner.getTokenValue(),
      text: scanner.getTokenText(),
      start: scanner.getTokenStart(),
      end: scanner.getTokenEnd(),
      flags: scanner.getTokenFlags(),
      precedingLineBreak: scanner.hasPrecedingLineBreak(),
    });
    kind = scanner.scan();
  }
  return { tokens, errorCodes };
}

function kinds(src: string): SyntaxKind[] {
  return tokenize(src).tokens.map(t => t.kind);
}

test("keywords vs identifiers", () => {
  const { tokens } = tokenize("class Foo int var");
  expect(tokens.map(t => t.kind)).toEqual([
    SyntaxKind.ClassKeyword,
    SyntaxKind.Identifier,
    SyntaxKind.IntKeyword,
    // 'var' is contextual -> scanned as an identifier
    SyntaxKind.Identifier,
  ]);
  expect(tokens[1]!.value).toBe("Foo");
  expect(tokens[3]!.value).toBe("var");
});

test("identifiers allow $ and _", () => {
  expect(kinds("$x _y a1 _")).toEqual([
    SyntaxKind.Identifier,
    SyntaxKind.Identifier,
    SyntaxKind.Identifier,
    SyntaxKind.Identifier,
  ]);
});

test("operators use maximal munch", () => {
  expect(kinds("+ ++ += -> :: ... < <= << <<= == != >>>=")).toEqual([
    SyntaxKind.PlusToken,
    SyntaxKind.PlusPlusToken,
    SyntaxKind.PlusEqualsToken,
    SyntaxKind.ArrowToken,
    SyntaxKind.ColonColonToken,
    SyntaxKind.DotDotDotToken,
    SyntaxKind.LessThanToken,
    SyntaxKind.LessThanEqualsToken,
    SyntaxKind.LessThanLessThanToken,
    SyntaxKind.LessThanLessThanEqualsToken,
    SyntaxKind.EqualsEqualsToken,
    SyntaxKind.ExclamationEqualsToken,
    // '>' family stays single until reScanGreaterToken, so ">>>=" is four tokens
    SyntaxKind.GreaterThanToken,
    SyntaxKind.GreaterThanToken,
    SyntaxKind.GreaterThanToken,
    SyntaxKind.EqualsToken,
  ]);
});

test("'>' is scanned one at a time so nested generics close cleanly", () => {
  // List<List<T>> -> ... T > >  (two single GreaterThanToken)
  const ks = kinds("List<List<T>>");
  expect(ks.slice(-2)).toEqual([SyntaxKind.GreaterThanToken, SyntaxKind.GreaterThanToken]);
});

test("reScanGreaterToken merges the '>' family on demand", () => {
  const cases: Array<[string, SyntaxKind]> = [
    [">>", SyntaxKind.GreaterThanGreaterThanToken],
    [">>>", SyntaxKind.GreaterThanGreaterThanGreaterThanToken],
    [">=", SyntaxKind.GreaterThanEqualsToken],
    [">>=", SyntaxKind.GreaterThanGreaterThanEqualsToken],
    [">>>=", SyntaxKind.GreaterThanGreaterThanGreaterThanEqualsToken],
  ];
  for (const [src, expected] of cases) {
    const scanner = createScanner(src);
    expect(scanner.scan()).toBe(SyntaxKind.GreaterThanToken);
    expect(scanner.reScanGreaterToken()).toBe(expected);
    expect(scanner.getTokenEnd()).toBe(src.length);
  }
});

test("numeric literals and their flags", () => {
  const { tokens } = tokenize("0 42 0xFF 0b1010 0777 1_000 3.14 1.0f 2.0d 100L 1e10");
  expect(tokens.every(t => t.kind === SyntaxKind.NumericLiteral)).toBe(true);
  const flag = (i: number) => tokens[i]!.flags;
  expect(flag(2) & TokenFlags.HexSpecifier).toBeTruthy();
  expect(flag(3) & TokenFlags.BinarySpecifier).toBeTruthy();
  expect(flag(4) & TokenFlags.OctalSpecifier).toBeTruthy();
  expect(flag(5) & TokenFlags.ContainsUnderscore).toBeTruthy();
  expect(flag(7) & TokenFlags.FloatSuffix).toBeTruthy();
  expect(flag(8) & TokenFlags.DoubleSuffix).toBeTruthy();
  expect(flag(9) & TokenFlags.LongSuffix).toBeTruthy();
});

test("leading-dot float", () => {
  const { tokens } = tokenize(".5");
  expect(tokens).toHaveLength(1);
  expect(tokens[0]!.kind).toBe(SyntaxKind.NumericLiteral);
  expect(tokens[0]!.value).toBe(".5");
});

test("string literal decodes escapes into the token value", () => {
  const { tokens } = tokenize('"a\\tb\\n\\"c\\u0041"');
  expect(tokens[0]!.kind).toBe(SyntaxKind.StringLiteral);
  expect(tokens[0]!.value).toBe('a\tb\n"cA');
});

test("character literal", () => {
  const { tokens } = tokenize("'a' '\\n'");
  expect(tokens.map(t => t.kind)).toEqual([
    SyntaxKind.CharacterLiteral,
    SyntaxKind.CharacterLiteral,
  ]);
  expect(tokens[0]!.value).toBe("a");
  expect(tokens[1]!.value).toBe("\n");
});

test("text block", () => {
  const src = '"""\nhello\n"""';
  const { tokens, errorCodes } = tokenize(src);
  expect(errorCodes).toEqual([]);
  expect(tokens).toHaveLength(1);
  expect(tokens[0]!.kind).toBe(SyntaxKind.TextBlockLiteral);
});

test("trivia is skipped and line breaks are flagged", () => {
  const { tokens } = tokenize("a // comment\nb /* multi\nline */ c");
  expect(tokens.map(t => t.kind)).toEqual([
    SyntaxKind.Identifier,
    SyntaxKind.Identifier,
    SyntaxKind.Identifier,
  ]);
  expect(tokens[0]!.precedingLineBreak).toBe(false);
  expect(tokens[1]!.precedingLineBreak).toBe(true);
  expect(tokens[2]!.precedingLineBreak).toBe(true); // newline inside the block comment
});

test("token positions exclude leading trivia", () => {
  const { tokens } = tokenize("  foo");
  expect(tokens[0]!.start).toBe(2);
  expect(tokens[0]!.end).toBe(5);
});

test("unterminated string reports an error", () => {
  const { tokens, errorCodes } = tokenize('"abc');
  expect(tokens[0]!.kind).toBe(SyntaxKind.StringLiteral);
  expect(tokens[0]!.flags & TokenFlags.Unterminated).toBeTruthy();
  expect(errorCodes).toHaveLength(1);
});

test("unterminated block comment reports an error", () => {
  const { errorCodes } = tokenize("/* never closed");
  expect(errorCodes).toHaveLength(1);
});

test("invalid character reports an error and does not loop", () => {
  const { tokens, errorCodes } = tokenize("a # b");
  expect(errorCodes).toHaveLength(1);
  expect(tokens.map(t => t.kind)).toEqual([
    SyntaxKind.Identifier,
    SyntaxKind.Unknown,
    SyntaxKind.Identifier,
  ]);
});

test("lookAhead restores scanner state", () => {
  const scanner = createScanner("a b");
  expect(scanner.scan()).toBe(SyntaxKind.Identifier);
  const peeked = scanner.lookAhead(() => scanner.scan());
  expect(peeked).toBe(SyntaxKind.Identifier);
  // after lookAhead we are still positioned on 'a'
  expect(scanner.getTokenValue()).toBe("a");
  expect(scanner.scan()).toBe(SyntaxKind.Identifier);
  expect(scanner.getTokenValue()).toBe("b");
});

test("unicode escapes and underscores in literals", () => {
  const { tokens } = tokenize('"\\u0041\\u0042" 0xFF_FF 0b1010_1010 1_000_000L');
  expect(tokens[0]!.value).toBe("AB");
  expect(tokens[1]!.flags & TokenFlags.HexSpecifier).toBeTruthy();
  expect(tokens[1]!.flags & TokenFlags.ContainsUnderscore).toBeTruthy();
  expect(tokens[3]!.flags & TokenFlags.LongSuffix).toBeTruthy();
});

test("CRLF line breaks set the preceding-line-break flag", () => {
  const { tokens } = tokenize("a\r\nb");
  expect(tokens[1]!.precedingLineBreak).toBe(true);
});

test("character literal with octal and unicode escapes", () => {
  const { tokens } = tokenize("'\\101' '\\u0041'");
  expect(tokens[0]!.value).toBe("A"); // \101 octal = 65 = 'A'
  expect(tokens[1]!.value).toBe("A");
});

test("consecutive '<' are scanned as separate tokens", () => {
  expect(kinds("a < < b")).toEqual([
    SyntaxKind.Identifier,
    SyntaxKind.LessThanToken,
    SyntaxKind.LessThanToken,
    SyntaxKind.Identifier,
  ]);
});

test("hexadecimal floating-point literals (0x1p1023, 0x1.8p-3)", () => {
  const { tokens, errorCodes } = tokenize("0x1p1023 0x1.8p-3 0x1.0p0d");
  expect(errorCodes).toEqual([]);
  expect(tokens.map(t => t.kind)).toEqual([
    SyntaxKind.NumericLiteral,
    SyntaxKind.NumericLiteral,
    SyntaxKind.NumericLiteral,
  ]);
  expect(tokens[2]!.flags & TokenFlags.DoubleSuffix).toBeTruthy();
});
