// Java lexical scanner. Mirrors the TypeScript compiler scanner: a closure over
// mutable state, returning a Scanner with scan()/getToken()/reScan helpers, and
// reporting problems through an ErrorCallback rather than throwing.
//
// Trivia (whitespace, line breaks, comments) is always skipped; a line break
// before a token is recorded as TokenFlags.PrecedingLineBreak.
//
// Like the TS scanner, '>' is always scanned as a single GreaterThanToken so
// that nested generics ("List<List<T>>") parse as a sequence of single '>'
// tokens. reScanGreaterToken merges it into '>>', '>>>', '>=', '>>=' or '>>>='
// on demand when parsing expressions.

import {
  type DiagnosticMessage,
  type ErrorCallback,
  type Scanner,
  SyntaxKind,
  TokenFlags,
} from "./types.ts";
import { Diagnostics } from "./diagnostics.ts";
import { textToKeyword } from "./utilities.ts";

const enum Char {
  Tab = 0x09,
  LineFeed = 0x0a,
  VerticalTab = 0x0b,
  FormFeed = 0x0c,
  CarriageReturn = 0x0d,
  Space = 0x20,
  Exclamation = 0x21, // !
  DoubleQuote = 0x22, // "
  Dollar = 0x24, // $
  Percent = 0x25, // %
  Ampersand = 0x26, // &
  SingleQuote = 0x27, // '
  OpenParen = 0x28,
  CloseParen = 0x29,
  Asterisk = 0x2a, // *
  Plus = 0x2b, // +
  Comma = 0x2c,
  Minus = 0x2d, // -
  Dot = 0x2e, // .
  Slash = 0x2f, // /
  _0 = 0x30,
  _1 = 0x31,
  _7 = 0x37,
  _9 = 0x39,
  Colon = 0x3a,
  Semicolon = 0x3b,
  LessThan = 0x3c, // <
  Equals = 0x3d, // =
  GreaterThan = 0x3e, // >
  Question = 0x3f, // ?
  At = 0x40, // @
  A = 0x41,
  B = 0x42,
  D = 0x44,
  E = 0x45,
  F = 0x46,
  L = 0x4c,
  X = 0x58,
  Z = 0x5a,
  OpenBracket = 0x5b,
  Backslash = 0x5c,
  CloseBracket = 0x5d,
  Caret = 0x5e, // ^
  Underscore = 0x5f, // _
  a = 0x61,
  b = 0x62,
  d = 0x64,
  e = 0x65,
  f = 0x66,
  l = 0x6c,
  n = 0x6e,
  r = 0x72,
  s = 0x73,
  t = 0x74,
  u = 0x75,
  x = 0x78,
  z = 0x7a,
  OpenBrace = 0x7b,
  Bar = 0x7c, // |
  CloseBrace = 0x7d,
  Tilde = 0x7e, // ~
}

function isLineBreak(ch: number): boolean {
  return ch === Char.LineFeed || ch === Char.CarriageReturn;
}

function isDigit(ch: number): boolean {
  return ch >= Char._0 && ch <= Char._9;
}

function isOctalDigit(ch: number): boolean {
  return ch >= Char._0 && ch <= Char._7;
}

function isHexDigit(ch: number): boolean {
  return isDigit(ch) || (ch >= Char.A && ch <= Char.F) || (ch >= Char.a && ch <= Char.f);
}

function isBinaryDigit(ch: number): boolean {
  return ch === Char._0 || ch === Char._1;
}

function hexValue(ch: number): number {
  if (isDigit(ch)) return ch - Char._0;
  if (ch >= Char.A && ch <= Char.F) return ch - Char.A + 10;
  return ch - Char.a + 10;
}

// Java identifier rules (approximation): ASCII letters, '_', '$', and any
// non-ASCII code point (covering most Unicode letters). Full
// Character.isJavaIdentifierStart semantics are deferred.
function isIdentifierStart(ch: number): boolean {
  return (
    (ch >= Char.A && ch <= Char.Z) ||
    (ch >= Char.a && ch <= Char.z) ||
    ch === Char.Underscore ||
    ch === Char.Dollar ||
    ch > 0x7f
  );
}

function isIdentifierPart(ch: number): boolean {
  return isIdentifierStart(ch) || isDigit(ch);
}

export function createScanner(textInitial = "", onErrorInitial?: ErrorCallback): Scanner {
  let text = textInitial;
  let end = text.length;
  let pos = 0;
  let fullStartPos = 0;
  let tokenStart = 0;
  let token = SyntaxKind.Unknown;
  let tokenValue = "";
  let tokenFlags = TokenFlags.None;
  let onError = onErrorInitial;

  function error(message: DiagnosticMessage, errPos: number, length: number): void {
    onError?.(message, errPos, length);
  }

  function scan(): SyntaxKind {
    fullStartPos = pos;
    tokenFlags = TokenFlags.None;
    tokenValue = "";

    while (true) {
      tokenStart = pos;
      if (pos >= end) {
        return (token = SyntaxKind.EndOfFileToken);
      }

      const ch = text.charCodeAt(pos);
      switch (ch) {
        case Char.LineFeed:
        case Char.CarriageReturn:
          tokenFlags |= TokenFlags.PrecedingLineBreak;
          pos++;
          continue;
        case Char.Space:
        case Char.Tab:
        case Char.FormFeed:
        case Char.VerticalTab:
          pos++;
          continue;

        case Char.Slash: {
          const next = text.charCodeAt(pos + 1);
          if (next === Char.Slash) {
            pos += 2;
            while (pos < end && !isLineBreak(text.charCodeAt(pos))) pos++;
            continue;
          }
          if (next === Char.Asterisk) {
            pos += 2;
            let closed = false;
            while (pos < end) {
              if (
                text.charCodeAt(pos) === Char.Asterisk &&
                text.charCodeAt(pos + 1) === Char.Slash
              ) {
                pos += 2;
                closed = true;
                break;
              }
              if (isLineBreak(text.charCodeAt(pos))) tokenFlags |= TokenFlags.PrecedingLineBreak;
              pos++;
            }
            if (!closed) {
              tokenFlags |= TokenFlags.Unterminated;
              error(Diagnostics.Unterminated_comment, tokenStart, pos - tokenStart);
            }
            continue;
          }
          if (next === Char.Equals) {
            pos += 2;
            return (token = SyntaxKind.SlashEqualsToken);
          }
          pos++;
          return (token = SyntaxKind.SlashToken);
        }

        case Char.OpenBrace:
          pos++;
          return (token = SyntaxKind.OpenBraceToken);
        case Char.CloseBrace:
          pos++;
          return (token = SyntaxKind.CloseBraceToken);
        case Char.OpenParen:
          pos++;
          return (token = SyntaxKind.OpenParenToken);
        case Char.CloseParen:
          pos++;
          return (token = SyntaxKind.CloseParenToken);
        case Char.OpenBracket:
          pos++;
          return (token = SyntaxKind.OpenBracketToken);
        case Char.CloseBracket:
          pos++;
          return (token = SyntaxKind.CloseBracketToken);
        case Char.Semicolon:
          pos++;
          return (token = SyntaxKind.SemicolonToken);
        case Char.Comma:
          pos++;
          return (token = SyntaxKind.CommaToken);
        case Char.At:
          pos++;
          return (token = SyntaxKind.AtToken);
        case Char.Question:
          pos++;
          return (token = SyntaxKind.QuestionToken);
        case Char.Tilde:
          pos++;
          return (token = SyntaxKind.TildeToken);

        case Char.Colon:
          if (text.charCodeAt(pos + 1) === Char.Colon) {
            pos += 2;
            return (token = SyntaxKind.ColonColonToken);
          }
          pos++;
          return (token = SyntaxKind.ColonToken);

        case Char.Dot:
          if (isDigit(text.charCodeAt(pos + 1))) {
            return scanNumber();
          }
          if (text.charCodeAt(pos + 1) === Char.Dot && text.charCodeAt(pos + 2) === Char.Dot) {
            pos += 3;
            return (token = SyntaxKind.DotDotDotToken);
          }
          pos++;
          return (token = SyntaxKind.DotToken);

        case Char.Plus:
          if (text.charCodeAt(pos + 1) === Char.Plus) {
            pos += 2;
            return (token = SyntaxKind.PlusPlusToken);
          }
          if (text.charCodeAt(pos + 1) === Char.Equals) {
            pos += 2;
            return (token = SyntaxKind.PlusEqualsToken);
          }
          pos++;
          return (token = SyntaxKind.PlusToken);

        case Char.Minus:
          if (text.charCodeAt(pos + 1) === Char.Minus) {
            pos += 2;
            return (token = SyntaxKind.MinusMinusToken);
          }
          if (text.charCodeAt(pos + 1) === Char.Equals) {
            pos += 2;
            return (token = SyntaxKind.MinusEqualsToken);
          }
          if (text.charCodeAt(pos + 1) === Char.GreaterThan) {
            pos += 2;
            return (token = SyntaxKind.ArrowToken);
          }
          pos++;
          return (token = SyntaxKind.MinusToken);

        case Char.Asterisk:
          if (text.charCodeAt(pos + 1) === Char.Equals) {
            pos += 2;
            return (token = SyntaxKind.AsteriskEqualsToken);
          }
          pos++;
          return (token = SyntaxKind.AsteriskToken);

        case Char.Percent:
          if (text.charCodeAt(pos + 1) === Char.Equals) {
            pos += 2;
            return (token = SyntaxKind.PercentEqualsToken);
          }
          pos++;
          return (token = SyntaxKind.PercentToken);

        case Char.Equals:
          if (text.charCodeAt(pos + 1) === Char.Equals) {
            pos += 2;
            return (token = SyntaxKind.EqualsEqualsToken);
          }
          pos++;
          return (token = SyntaxKind.EqualsToken);

        case Char.Exclamation:
          if (text.charCodeAt(pos + 1) === Char.Equals) {
            pos += 2;
            return (token = SyntaxKind.ExclamationEqualsToken);
          }
          pos++;
          return (token = SyntaxKind.ExclamationToken);

        case Char.Ampersand:
          if (text.charCodeAt(pos + 1) === Char.Ampersand) {
            pos += 2;
            return (token = SyntaxKind.AmpersandAmpersandToken);
          }
          if (text.charCodeAt(pos + 1) === Char.Equals) {
            pos += 2;
            return (token = SyntaxKind.AmpersandEqualsToken);
          }
          pos++;
          return (token = SyntaxKind.AmpersandToken);

        case Char.Bar:
          if (text.charCodeAt(pos + 1) === Char.Bar) {
            pos += 2;
            return (token = SyntaxKind.BarBarToken);
          }
          if (text.charCodeAt(pos + 1) === Char.Equals) {
            pos += 2;
            return (token = SyntaxKind.BarEqualsToken);
          }
          pos++;
          return (token = SyntaxKind.BarToken);

        case Char.Caret:
          if (text.charCodeAt(pos + 1) === Char.Equals) {
            pos += 2;
            return (token = SyntaxKind.CaretEqualsToken);
          }
          pos++;
          return (token = SyntaxKind.CaretToken);

        case Char.LessThan:
          if (text.charCodeAt(pos + 1) === Char.LessThan) {
            if (text.charCodeAt(pos + 2) === Char.Equals) {
              pos += 3;
              return (token = SyntaxKind.LessThanLessThanEqualsToken);
            }
            pos += 2;
            return (token = SyntaxKind.LessThanLessThanToken);
          }
          if (text.charCodeAt(pos + 1) === Char.Equals) {
            pos += 2;
            return (token = SyntaxKind.LessThanEqualsToken);
          }
          pos++;
          return (token = SyntaxKind.LessThanToken);

        case Char.GreaterThan:
          // Always a single '>'; merged on demand via reScanGreaterToken.
          pos++;
          return (token = SyntaxKind.GreaterThanToken);

        case Char.DoubleQuote:
          return scanStringOrTextBlock();
        case Char.SingleQuote:
          return scanCharacterLiteral();

        default:
          if (isDigit(ch)) {
            return scanNumber();
          }
          if (isIdentifierStart(ch)) {
            return scanIdentifierOrKeyword();
          }
          // Unknown character: report and skip one code unit.
          error(Diagnostics.Invalid_character, pos, 1);
          pos++;
          return (token = SyntaxKind.Unknown);
      }
    }
  }

  function scanIdentifierOrKeyword(): SyntaxKind {
    while (pos < end && isIdentifierPart(text.charCodeAt(pos))) pos++;
    tokenValue = text.slice(tokenStart, pos);
    const keyword = textToKeyword.get(tokenValue);
    return (token = keyword ?? SyntaxKind.Identifier);
  }

  // Consume a run of digits permitting single underscores between them.
  function scanDigitsWithUnderscores(isDigitChar: (ch: number) => boolean): boolean {
    let any = false;
    while (pos < end) {
      const ch = text.charCodeAt(pos);
      if (isDigitChar(ch)) {
        any = true;
        pos++;
      } else if (ch === Char.Underscore) {
        tokenFlags |= TokenFlags.ContainsUnderscore;
        pos++;
      } else {
        break;
      }
    }
    return any;
  }

  function scanNumber(): SyntaxKind {
    const first = text.charCodeAt(pos);

    if (first === Char._0) {
      const next = text.charCodeAt(pos + 1);
      if (next === Char.x || next === Char.X) {
        tokenFlags |= TokenFlags.HexSpecifier;
        pos += 2;
        if (!scanDigitsWithUnderscores(isHexDigit)) {
          error(Diagnostics.Hexadecimal_digit_expected, pos, 0);
        }
        scanIntegerSuffix();
        return finishNumber();
      }
      if (next === Char.b || next === Char.B) {
        tokenFlags |= TokenFlags.BinarySpecifier;
        pos += 2;
        if (!scanDigitsWithUnderscores(isBinaryDigit)) {
          error(Diagnostics.Binary_digit_expected, pos, 0);
        }
        scanIntegerSuffix();
        return finishNumber();
      }
    }

    scanDigitsWithUnderscores(isDigit);

    let isFloat = false;
    if (text.charCodeAt(pos) === Char.Dot) {
      isFloat = true;
      pos++;
      scanDigitsWithUnderscores(isDigit);
    }
    const exp = text.charCodeAt(pos);
    if (exp === Char.e || exp === Char.E) {
      isFloat = true;
      pos++;
      if (text.charCodeAt(pos) === Char.Plus || text.charCodeAt(pos) === Char.Minus) pos++;
      if (!scanDigitsWithUnderscores(isDigit)) {
        error(Diagnostics.Digit_expected, pos, 0);
      }
    }

    const suffix = text.charCodeAt(pos);
    if (suffix === Char.f || suffix === Char.F) {
      tokenFlags |= TokenFlags.FloatSuffix;
      pos++;
    } else if (suffix === Char.d || suffix === Char.D) {
      tokenFlags |= TokenFlags.DoubleSuffix;
      pos++;
    } else if (!isFloat && (suffix === Char.l || suffix === Char.L)) {
      tokenFlags |= TokenFlags.LongSuffix;
      pos++;
    } else if (!isFloat && first === Char._0 && pos - tokenStart > 1) {
      // A leading-zero integer with no '.'/exponent/suffix is octal.
      tokenFlags |= TokenFlags.OctalSpecifier;
    }

    return finishNumber();
  }

  function scanIntegerSuffix(): void {
    const suffix = text.charCodeAt(pos);
    if (suffix === Char.l || suffix === Char.L) {
      tokenFlags |= TokenFlags.LongSuffix;
      pos++;
    }
  }

  function finishNumber(): SyntaxKind {
    tokenValue = text.slice(tokenStart, pos);
    return (token = SyntaxKind.NumericLiteral);
  }

  // Decode one escape sequence starting at the backslash (pos points at '\').
  // Returns the decoded text and advances pos past the escape.
  function scanEscapeSequence(): string {
    pos++; // consume backslash
    if (pos >= end) return "\\";
    const ch = text.charCodeAt(pos);
    pos++;
    switch (ch) {
      case Char.b:
        return "\b";
      case Char.t:
        return "\t";
      case Char.n:
        return "\n";
      case Char.f:
        return "\f";
      case Char.r:
        return "\r";
      case Char.s:
        return " "; // SE15 text-block escape; harmless elsewhere
      case Char.DoubleQuote:
        return '"';
      case Char.SingleQuote:
        return "'";
      case Char.Backslash:
        return "\\";
      case Char.u: {
        // One or more 'u' then 4 hex digits.
        while (text.charCodeAt(pos) === Char.u) pos++;
        let value = 0;
        let count = 0;
        while (count < 4 && isHexDigit(text.charCodeAt(pos))) {
          value = value * 16 + hexValue(text.charCodeAt(pos));
          pos++;
          count++;
        }
        if (count < 4) error(Diagnostics.Hexadecimal_digit_expected, pos, 0);
        return String.fromCharCode(value);
      }
      default:
        if (isOctalDigit(ch)) {
          let value = ch - Char._0;
          let count = 1;
          while (
            count < 3 &&
            isOctalDigit(text.charCodeAt(pos)) &&
            value * 8 + (text.charCodeAt(pos) - Char._0) <= 0xff
          ) {
            value = value * 8 + (text.charCodeAt(pos) - Char._0);
            pos++;
            count++;
          }
          return String.fromCharCode(value);
        }
        return String.fromCharCode(ch);
    }
  }

  function scanStringOrTextBlock(): SyntaxKind {
    // Text block: three double quotes. Full incidental-whitespace handling is
    // deferred (M12); here we tokenize it and store the raw inner text.
    if (
      text.charCodeAt(pos + 1) === Char.DoubleQuote &&
      text.charCodeAt(pos + 2) === Char.DoubleQuote
    ) {
      return scanTextBlock();
    }
    pos++; // opening quote
    let value = "";
    while (true) {
      if (pos >= end || isLineBreak(text.charCodeAt(pos))) {
        tokenFlags |= TokenFlags.Unterminated;
        error(Diagnostics.Unterminated_string_literal, tokenStart, pos - tokenStart);
        break;
      }
      const ch = text.charCodeAt(pos);
      if (ch === Char.DoubleQuote) {
        pos++;
        break;
      }
      if (ch === Char.Backslash) {
        value += scanEscapeSequence();
        continue;
      }
      value += text[pos]!;
      pos++;
    }
    tokenValue = value;
    return (token = SyntaxKind.StringLiteral);
  }

  function scanTextBlock(): SyntaxKind {
    pos += 3; // opening """
    while (pos < end) {
      if (
        text.charCodeAt(pos) === Char.DoubleQuote &&
        text.charCodeAt(pos + 1) === Char.DoubleQuote &&
        text.charCodeAt(pos + 2) === Char.DoubleQuote
      ) {
        pos += 3;
        tokenValue = text.slice(tokenStart, pos);
        return (token = SyntaxKind.TextBlockLiteral);
      }
      if (text.charCodeAt(pos) === Char.Backslash) {
        pos += 2;
        continue;
      }
      pos++;
    }
    tokenFlags |= TokenFlags.Unterminated;
    error(Diagnostics.Unterminated_string_literal, tokenStart, pos - tokenStart);
    tokenValue = text.slice(tokenStart, pos);
    return (token = SyntaxKind.TextBlockLiteral);
  }

  function scanCharacterLiteral(): SyntaxKind {
    pos++; // opening quote
    let value = "";
    while (true) {
      if (pos >= end || isLineBreak(text.charCodeAt(pos))) {
        tokenFlags |= TokenFlags.Unterminated;
        error(Diagnostics.Unterminated_character_literal, tokenStart, pos - tokenStart);
        break;
      }
      const ch = text.charCodeAt(pos);
      if (ch === Char.SingleQuote) {
        pos++;
        break;
      }
      if (ch === Char.Backslash) {
        value += scanEscapeSequence();
        continue;
      }
      value += text[pos]!;
      pos++;
    }
    tokenValue = value;
    return (token = SyntaxKind.CharacterLiteral);
  }

  function reScanGreaterToken(): SyntaxKind {
    if (token === SyntaxKind.GreaterThanToken) {
      if (text.charCodeAt(pos) === Char.GreaterThan) {
        if (text.charCodeAt(pos + 1) === Char.GreaterThan) {
          if (text.charCodeAt(pos + 2) === Char.Equals) {
            pos += 3;
            return (token = SyntaxKind.GreaterThanGreaterThanGreaterThanEqualsToken);
          }
          pos += 2;
          return (token = SyntaxKind.GreaterThanGreaterThanGreaterThanToken);
        }
        if (text.charCodeAt(pos + 1) === Char.Equals) {
          pos += 2;
          return (token = SyntaxKind.GreaterThanGreaterThanEqualsToken);
        }
        pos++;
        return (token = SyntaxKind.GreaterThanGreaterThanToken);
      }
      if (text.charCodeAt(pos) === Char.Equals) {
        pos++;
        return (token = SyntaxKind.GreaterThanEqualsToken);
      }
    }
    return token;
  }

  function speculate<T>(callback: () => T, isLookahead: boolean): T {
    const savePos = pos;
    const saveFullStartPos = fullStartPos;
    const saveTokenStart = tokenStart;
    const saveToken = token;
    const saveTokenValue = tokenValue;
    const saveTokenFlags = tokenFlags;

    const result = callback();

    if (!result || isLookahead) {
      pos = savePos;
      fullStartPos = saveFullStartPos;
      tokenStart = saveTokenStart;
      token = saveToken;
      tokenValue = saveTokenValue;
      tokenFlags = saveTokenFlags;
    }
    return result;
  }

  function resetTokenState(position: number): void {
    pos = position;
    fullStartPos = position;
    tokenStart = position;
    token = SyntaxKind.Unknown;
    tokenValue = "";
    tokenFlags = TokenFlags.None;
  }

  return {
    scan,
    getToken: () => token,
    getTokenText: () => text.slice(tokenStart, pos),
    getTokenValue: () => tokenValue,
    getTokenStart: () => tokenStart,
    getTokenFullStart: () => fullStartPos,
    getTokenEnd: () => pos,
    getTokenFlags: () => tokenFlags,
    hasPrecedingLineBreak: () => (tokenFlags & TokenFlags.PrecedingLineBreak) !== 0,
    setText(newText, start = 0, length) {
      text = newText;
      end = length === undefined ? text.length : start + length;
      resetTokenState(start);
    },
    setOnError(cb) {
      onError = cb;
    },
    resetTokenState,
    reScanGreaterToken,
    lookAhead: cb => speculate(cb, true),
    tryScan: cb => speculate(cb, false),
  };
}
