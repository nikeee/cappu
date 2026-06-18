package compiler

import (
	"reflect"
	"strings"
)

// Java lexical scanner. Follows tsgo's structure: a Scanner struct with methods
// (the TS original is a closure), reporting problems through an ErrorCallback
// rather than panicking. Positions are byte offsets (tsgo's model); the source
// is ASCII for all current fixtures, so they match the Node build's UTF-16
// offsets. Port of src/compiler/scanner.ts.
//
// '>' is always scanned as a single GreaterThanToken so nested generics
// ("List<List<T>>") parse as single '>' tokens; ReScanGreaterToken merges it
// into '>>', '>>>', '>=', '>>=' or '>>>=' on demand.

// character code constants (ASCII)
const (
	chTab            = 0x09
	chLineFeed       = 0x0a
	chVerticalTab    = 0x0b
	chFormFeed       = 0x0c
	chCarriageReturn = 0x0d
	chSpace          = 0x20
	chExclamation    = 0x21
	chDoubleQuote    = 0x22
	chDollar         = 0x24
	chPercent        = 0x25
	chAmpersand      = 0x26
	chSingleQuote    = 0x27
	chOpenParen      = 0x28
	chCloseParen     = 0x29
	chAsterisk       = 0x2a
	chPlus           = 0x2b
	chComma          = 0x2c
	chMinus          = 0x2d
	chDot            = 0x2e
	chSlash          = 0x2f
	ch0              = 0x30
	ch1              = 0x31
	ch7              = 0x37
	ch9              = 0x39
	chColon          = 0x3a
	chSemicolon      = 0x3b
	chLessThan       = 0x3c
	chEquals         = 0x3d
	chGreaterThan    = 0x3e
	chQuestion       = 0x3f
	chAt             = 0x40
	chUpperA         = 0x41
	chUpperB         = 0x42
	chUpperD         = 0x44
	chUpperE         = 0x45
	chUpperF         = 0x46
	chUpperL         = 0x4c
	chUpperP         = 0x50
	chUpperX         = 0x58
	chUpperZ         = 0x5a
	chOpenBracket    = 0x5b
	chBackslash      = 0x5c
	chCloseBracket   = 0x5d
	chCaret          = 0x5e
	chUnderscore     = 0x5f
	chLowerA         = 0x61
	chLowerB         = 0x62
	chLowerD         = 0x64
	chLowerE         = 0x65
	chLowerF         = 0x66
	chLowerL         = 0x6c
	chLowerN         = 0x6e
	chLowerP         = 0x70
	chLowerR         = 0x72
	chLowerS         = 0x73
	chLowerT         = 0x74
	chLowerU         = 0x75
	chLowerX         = 0x78
	chLowerZ         = 0x7a
	chOpenBrace      = 0x7b
	chBar            = 0x7c
	chCloseBrace     = 0x7d
	chTilde          = 0x7e
)

func isLineBreak(ch int) bool  { return ch == chLineFeed || ch == chCarriageReturn }
func isDigit(ch int) bool      { return ch >= ch0 && ch <= ch9 }
func isOctalDigit(ch int) bool { return ch >= ch0 && ch <= ch7 }
func isHexDigit(ch int) bool {
	return isDigit(ch) || (ch >= chUpperA && ch <= chUpperF) || (ch >= chLowerA && ch <= chLowerF)
}
func isBinaryDigit(ch int) bool { return ch == ch0 || ch == ch1 }

func hexValue(ch int) int {
	switch {
	case isDigit(ch):
		return ch - ch0
	case ch >= chUpperA && ch <= chUpperF:
		return ch - chUpperA + 10
	default:
		return ch - chLowerA + 10
	}
}

// isIdentifierStart approximates Java identifier rules: ASCII letters, '_', '$',
// and any non-ASCII byte (covering UTF-8 letters). Full
// Character.isJavaIdentifierStart semantics are deferred.
func isIdentifierStart(ch int) bool {
	return (ch >= chUpperA && ch <= chUpperZ) ||
		(ch >= chLowerA && ch <= chLowerZ) ||
		ch == chUnderscore || ch == chDollar || ch > 0x7f
}

func isIdentifierPart(ch int) bool { return isIdentifierStart(ch) || isDigit(ch) }

// Scanner tokenizes Java source.
type Scanner struct {
	text         string
	end          int
	pos          int
	fullStartPos int
	tokenStart   int
	token        SyntaxKind
	tokenValue   string
	tokenFlags   TokenFlags
	onError      ErrorCallback
}

// NewScanner creates a scanner over text (onError may be nil).
func NewScanner(text string, onError ErrorCallback) *Scanner {
	return &Scanner{text: text, end: len(text), token: Unknown, onError: onError}
}

// charCodeAt returns the byte at i, or -1 past the end of the text (mirroring
// JS charCodeAt returning NaN, whose comparisons are all false).
func (s *Scanner) charCodeAt(i int) int {
	if i < 0 || i >= len(s.text) {
		return -1
	}
	return int(s.text[i])
}

func (s *Scanner) error(message DiagnosticMessage, pos, length int) {
	if s.onError != nil {
		s.onError(message, pos, length)
	}
}

// Token returns the kind of the last scanned token.
func (s *Scanner) Token() SyntaxKind { return s.token }

// TokenText is the raw source text of the last token.
func (s *Scanner) TokenText() string { return s.text[s.tokenStart:s.pos] }

// TokenValue is the decoded value (identifier/literal text with escapes applied).
func (s *Scanner) TokenValue() string { return s.tokenValue }

// TokenStart is the start offset (after leading trivia).
func (s *Scanner) TokenStart() int { return s.tokenStart }

// TokenFullStart is the offset including leading trivia.
func (s *Scanner) TokenFullStart() int { return s.fullStartPos }

// TokenEnd is the end offset.
func (s *Scanner) TokenEnd() int { return s.pos }

// TokenFlags are the lexical flags of the last token.
func (s *Scanner) TokenFlags() TokenFlags { return s.tokenFlags }

// HasPrecedingLineBreak reports whether a line break preceded the token.
func (s *Scanner) HasPrecedingLineBreak() bool { return s.tokenFlags&PrecedingLineBreak != 0 }

// SetOnError replaces the error callback.
func (s *Scanner) SetOnError(cb ErrorCallback) { s.onError = cb }

// SetText resets the scanner over newText[start:start+length] (length < 0 = to end).
func (s *Scanner) SetText(newText string, start, length int) {
	s.text = newText
	if length < 0 {
		s.end = len(newText)
	} else {
		s.end = start + length
	}
	s.ResetTokenState(start)
}

// ResetTokenState moves the scanner to position.
func (s *Scanner) ResetTokenState(position int) {
	s.pos = position
	s.fullStartPos = position
	s.tokenStart = position
	s.token = Unknown
	s.tokenValue = ""
	s.tokenFlags = TokenFlagsNone
}

// Scan reads the next token and returns its kind.
func (s *Scanner) Scan() SyntaxKind {
	s.fullStartPos = s.pos
	s.tokenFlags = TokenFlagsNone
	s.tokenValue = ""

	for {
		s.tokenStart = s.pos
		if s.pos >= s.end {
			s.token = EndOfFileToken
			return s.token
		}
		ch := s.charCodeAt(s.pos)
		switch ch {
		case chLineFeed, chCarriageReturn:
			s.tokenFlags |= PrecedingLineBreak
			s.pos++
			continue
		case chSpace, chTab, chFormFeed, chVerticalTab:
			s.pos++
			continue
		case chSlash:
			next := s.charCodeAt(s.pos + 1)
			if next == chSlash {
				s.pos += 2
				for s.pos < s.end && !isLineBreak(s.charCodeAt(s.pos)) {
					s.pos++
				}
				continue
			}
			if next == chAsterisk {
				s.pos += 2
				closed := false
				for s.pos < s.end {
					if s.charCodeAt(s.pos) == chAsterisk && s.charCodeAt(s.pos+1) == chSlash {
						s.pos += 2
						closed = true
						break
					}
					if isLineBreak(s.charCodeAt(s.pos)) {
						s.tokenFlags |= PrecedingLineBreak
					}
					s.pos++
				}
				if !closed {
					s.tokenFlags |= Unterminated
					s.error(Diagnostics.UnterminatedComment, s.tokenStart, s.pos-s.tokenStart)
				}
				continue
			}
			if next == chEquals {
				s.pos += 2
				return s.set(SlashEqualsToken)
			}
			s.pos++
			return s.set(SlashToken)
		case chOpenBrace:
			s.pos++
			return s.set(OpenBraceToken)
		case chCloseBrace:
			s.pos++
			return s.set(CloseBraceToken)
		case chOpenParen:
			s.pos++
			return s.set(OpenParenToken)
		case chCloseParen:
			s.pos++
			return s.set(CloseParenToken)
		case chOpenBracket:
			s.pos++
			return s.set(OpenBracketToken)
		case chCloseBracket:
			s.pos++
			return s.set(CloseBracketToken)
		case chSemicolon:
			s.pos++
			return s.set(SemicolonToken)
		case chComma:
			s.pos++
			return s.set(CommaToken)
		case chAt:
			s.pos++
			return s.set(AtToken)
		case chQuestion:
			s.pos++
			return s.set(QuestionToken)
		case chTilde:
			s.pos++
			return s.set(TildeToken)
		case chColon:
			if s.charCodeAt(s.pos+1) == chColon {
				s.pos += 2
				return s.set(ColonColonToken)
			}
			s.pos++
			return s.set(ColonToken)
		case chDot:
			if isDigit(s.charCodeAt(s.pos + 1)) {
				return s.scanNumber()
			}
			if s.charCodeAt(s.pos+1) == chDot && s.charCodeAt(s.pos+2) == chDot {
				s.pos += 3
				return s.set(DotDotDotToken)
			}
			s.pos++
			return s.set(DotToken)
		case chPlus:
			if s.charCodeAt(s.pos+1) == chPlus {
				s.pos += 2
				return s.set(PlusPlusToken)
			}
			if s.charCodeAt(s.pos+1) == chEquals {
				s.pos += 2
				return s.set(PlusEqualsToken)
			}
			s.pos++
			return s.set(PlusToken)
		case chMinus:
			if s.charCodeAt(s.pos+1) == chMinus {
				s.pos += 2
				return s.set(MinusMinusToken)
			}
			if s.charCodeAt(s.pos+1) == chEquals {
				s.pos += 2
				return s.set(MinusEqualsToken)
			}
			if s.charCodeAt(s.pos+1) == chGreaterThan {
				s.pos += 2
				return s.set(ArrowToken)
			}
			s.pos++
			return s.set(MinusToken)
		case chAsterisk:
			if s.charCodeAt(s.pos+1) == chEquals {
				s.pos += 2
				return s.set(AsteriskEqualsToken)
			}
			s.pos++
			return s.set(AsteriskToken)
		case chPercent:
			if s.charCodeAt(s.pos+1) == chEquals {
				s.pos += 2
				return s.set(PercentEqualsToken)
			}
			s.pos++
			return s.set(PercentToken)
		case chEquals:
			if s.charCodeAt(s.pos+1) == chEquals {
				s.pos += 2
				return s.set(EqualsEqualsToken)
			}
			s.pos++
			return s.set(EqualsToken)
		case chExclamation:
			if s.charCodeAt(s.pos+1) == chEquals {
				s.pos += 2
				return s.set(ExclamationEqualsToken)
			}
			s.pos++
			return s.set(ExclamationToken)
		case chAmpersand:
			if s.charCodeAt(s.pos+1) == chAmpersand {
				s.pos += 2
				return s.set(AmpersandAmpersandToken)
			}
			if s.charCodeAt(s.pos+1) == chEquals {
				s.pos += 2
				return s.set(AmpersandEqualsToken)
			}
			s.pos++
			return s.set(AmpersandToken)
		case chBar:
			if s.charCodeAt(s.pos+1) == chBar {
				s.pos += 2
				return s.set(BarBarToken)
			}
			if s.charCodeAt(s.pos+1) == chEquals {
				s.pos += 2
				return s.set(BarEqualsToken)
			}
			s.pos++
			return s.set(BarToken)
		case chCaret:
			if s.charCodeAt(s.pos+1) == chEquals {
				s.pos += 2
				return s.set(CaretEqualsToken)
			}
			s.pos++
			return s.set(CaretToken)
		case chLessThan:
			if s.charCodeAt(s.pos+1) == chLessThan {
				if s.charCodeAt(s.pos+2) == chEquals {
					s.pos += 3
					return s.set(LessThanLessThanEqualsToken)
				}
				s.pos += 2
				return s.set(LessThanLessThanToken)
			}
			if s.charCodeAt(s.pos+1) == chEquals {
				s.pos += 2
				return s.set(LessThanEqualsToken)
			}
			s.pos++
			return s.set(LessThanToken)
		case chGreaterThan:
			// Always a single '>'; merged on demand via ReScanGreaterToken.
			s.pos++
			return s.set(GreaterThanToken)
		case chDoubleQuote:
			return s.scanStringOrTextBlock()
		case chSingleQuote:
			return s.scanCharacterLiteral()
		default:
			if isDigit(ch) {
				return s.scanNumber()
			}
			if isIdentifierStart(ch) {
				return s.scanIdentifierOrKeyword()
			}
			s.error(Diagnostics.InvalidCharacter, s.pos, 1)
			s.pos++
			return s.set(Unknown)
		}
	}
}

func (s *Scanner) set(kind SyntaxKind) SyntaxKind {
	s.token = kind
	return kind
}

func (s *Scanner) scanIdentifierOrKeyword() SyntaxKind {
	for s.pos < s.end && isIdentifierPart(s.charCodeAt(s.pos)) {
		s.pos++
	}
	s.tokenValue = s.text[s.tokenStart:s.pos]
	if kw, ok := textToKeyword[s.tokenValue]; ok {
		return s.set(kw)
	}
	return s.set(Identifier)
}

// scanDigitsWithUnderscores consumes a run of digits permitting single
// underscores between them; returns whether any digit was seen.
func (s *Scanner) scanDigitsWithUnderscores(isDigitChar func(int) bool) bool {
	any := false
	for s.pos < s.end {
		ch := s.charCodeAt(s.pos)
		switch {
		case isDigitChar(ch):
			any = true
			s.pos++
		case ch == chUnderscore:
			s.tokenFlags |= ContainsUnderscore
			s.pos++
		default:
			return any
		}
	}
	return any
}

func (s *Scanner) scanNumber() SyntaxKind {
	first := s.charCodeAt(s.pos)
	if first == ch0 {
		next := s.charCodeAt(s.pos + 1)
		if next == chLowerX || next == chUpperX {
			s.tokenFlags |= HexSpecifier
			s.pos += 2
			hadDigits := s.scanDigitsWithUnderscores(isHexDigit)
			isHexFloat := false
			if s.charCodeAt(s.pos) == chDot {
				isHexFloat = true
				s.pos++
				s.scanDigitsWithUnderscores(isHexDigit)
			} else if !hadDigits {
				s.error(Diagnostics.HexadecimalDigitExpected, s.pos, 0)
			}
			if s.charCodeAt(s.pos) == chLowerP || s.charCodeAt(s.pos) == chUpperP {
				isHexFloat = true
				s.pos++
				if s.charCodeAt(s.pos) == chPlus || s.charCodeAt(s.pos) == chMinus {
					s.pos++
				}
				if !s.scanDigitsWithUnderscores(isDigit) {
					s.error(Diagnostics.DigitExpected, s.pos, 0)
				}
			}
			if isHexFloat {
				switch s.charCodeAt(s.pos) {
				case chLowerF, chUpperF:
					s.tokenFlags |= FloatSuffix
					s.pos++
				case chLowerD, chUpperD:
					s.tokenFlags |= DoubleSuffix
					s.pos++
				}
			} else {
				s.scanIntegerSuffix()
			}
			return s.finishNumber()
		}
		if next == chLowerB || next == chUpperB {
			s.tokenFlags |= BinarySpecifier
			s.pos += 2
			if !s.scanDigitsWithUnderscores(isBinaryDigit) {
				s.error(Diagnostics.BinaryDigitExpected, s.pos, 0)
			}
			s.scanIntegerSuffix()
			return s.finishNumber()
		}
	}

	s.scanDigitsWithUnderscores(isDigit)

	isFloat := false
	if s.charCodeAt(s.pos) == chDot {
		isFloat = true
		s.pos++
		s.scanDigitsWithUnderscores(isDigit)
	}
	exp := s.charCodeAt(s.pos)
	if exp == chLowerE || exp == chUpperE {
		isFloat = true
		s.pos++
		if s.charCodeAt(s.pos) == chPlus || s.charCodeAt(s.pos) == chMinus {
			s.pos++
		}
		if !s.scanDigitsWithUnderscores(isDigit) {
			s.error(Diagnostics.DigitExpected, s.pos, 0)
		}
	}

	suffix := s.charCodeAt(s.pos)
	switch {
	case suffix == chLowerF || suffix == chUpperF:
		s.tokenFlags |= FloatSuffix
		s.pos++
	case suffix == chLowerD || suffix == chUpperD:
		s.tokenFlags |= DoubleSuffix
		s.pos++
	case !isFloat && (suffix == chLowerL || suffix == chUpperL):
		s.tokenFlags |= LongSuffix
		s.pos++
	case !isFloat && first == ch0 && s.pos-s.tokenStart > 1:
		s.tokenFlags |= OctalSpecifier
	}
	return s.finishNumber()
}

func (s *Scanner) scanIntegerSuffix() {
	suffix := s.charCodeAt(s.pos)
	if suffix == chLowerL || suffix == chUpperL {
		s.tokenFlags |= LongSuffix
		s.pos++
	}
}

func (s *Scanner) finishNumber() SyntaxKind {
	s.tokenValue = s.text[s.tokenStart:s.pos]
	return s.set(NumericLiteral)
}

// scanEscapeSequence decodes one escape starting at the backslash and advances
// past it.
func (s *Scanner) scanEscapeSequence() string {
	s.pos++ // consume backslash
	if s.pos >= len(s.text) {
		return "\\"
	}
	ch := s.charCodeAt(s.pos)
	s.pos++
	switch ch {
	case chLowerB:
		return "\b"
	case chLowerT:
		return "\t"
	case chLowerN:
		return "\n"
	case chLowerF:
		return "\f"
	case chLowerR:
		return "\r"
	case chLowerS:
		return " " // SE15 text-block escape; harmless elsewhere
	case chDoubleQuote:
		return "\""
	case chSingleQuote:
		return "'"
	case chBackslash:
		return "\\"
	case chLowerU:
		for s.charCodeAt(s.pos) == chLowerU {
			s.pos++
		}
		value, count := 0, 0
		for count < 4 && isHexDigit(s.charCodeAt(s.pos)) {
			value = value*16 + hexValue(s.charCodeAt(s.pos))
			s.pos++
			count++
		}
		if count < 4 {
			s.error(Diagnostics.HexadecimalDigitExpected, s.pos, 0)
		}
		return string(rune(value))
	default:
		if isOctalDigit(ch) {
			value, count := ch-ch0, 1
			for count < 3 && isOctalDigit(s.charCodeAt(s.pos)) && value*8+(s.charCodeAt(s.pos)-ch0) <= 0xff {
				value = value*8 + (s.charCodeAt(s.pos) - ch0)
				s.pos++
				count++
			}
			return string(rune(value))
		}
		return string(rune(ch))
	}
}

func (s *Scanner) scanStringOrTextBlock() SyntaxKind {
	if s.charCodeAt(s.pos+1) == chDoubleQuote && s.charCodeAt(s.pos+2) == chDoubleQuote {
		return s.scanTextBlock()
	}
	s.pos++ // opening quote
	var value strings.Builder
	for {
		if s.pos >= s.end || isLineBreak(s.charCodeAt(s.pos)) {
			s.tokenFlags |= Unterminated
			s.error(Diagnostics.UnterminatedStringLiteral, s.tokenStart, s.pos-s.tokenStart)
			break
		}
		ch := s.charCodeAt(s.pos)
		if ch == chDoubleQuote {
			s.pos++
			break
		}
		if ch == chBackslash {
			value.WriteString(s.scanEscapeSequence())
			continue
		}
		value.WriteByte(s.text[s.pos])
		s.pos++
	}
	s.tokenValue = value.String()
	return s.set(StringLiteral)
}

func (s *Scanner) scanTextBlock() SyntaxKind {
	s.pos += 3 // opening """
	for s.pos < s.end {
		if s.charCodeAt(s.pos) == chDoubleQuote && s.charCodeAt(s.pos+1) == chDoubleQuote && s.charCodeAt(s.pos+2) == chDoubleQuote {
			s.pos += 3
			s.tokenValue = s.text[s.tokenStart:s.pos]
			return s.set(TextBlockLiteral)
		}
		if s.charCodeAt(s.pos) == chBackslash {
			s.pos += 2
			continue
		}
		s.pos++
	}
	s.tokenFlags |= Unterminated
	s.error(Diagnostics.UnterminatedStringLiteral, s.tokenStart, s.pos-s.tokenStart)
	s.tokenValue = s.text[s.tokenStart:s.pos]
	return s.set(TextBlockLiteral)
}

func (s *Scanner) scanCharacterLiteral() SyntaxKind {
	s.pos++ // opening quote
	var value strings.Builder
	for {
		if s.pos >= s.end || isLineBreak(s.charCodeAt(s.pos)) {
			s.tokenFlags |= Unterminated
			s.error(Diagnostics.UnterminatedCharacterLiteral, s.tokenStart, s.pos-s.tokenStart)
			break
		}
		ch := s.charCodeAt(s.pos)
		if ch == chSingleQuote {
			s.pos++
			break
		}
		if ch == chBackslash {
			value.WriteString(s.scanEscapeSequence())
			continue
		}
		value.WriteByte(s.text[s.pos])
		s.pos++
	}
	s.tokenValue = value.String()
	return s.set(CharacterLiteral)
}

// ReScanGreaterToken merges a single '>' into the wider '>' family on demand.
func (s *Scanner) ReScanGreaterToken() SyntaxKind {
	if s.token == GreaterThanToken {
		if s.charCodeAt(s.pos) == chGreaterThan {
			if s.charCodeAt(s.pos+1) == chGreaterThan {
				if s.charCodeAt(s.pos+2) == chEquals {
					s.pos += 3
					return s.set(GreaterThanGreaterThanGreaterThanEqualsToken)
				}
				s.pos += 2
				return s.set(GreaterThanGreaterThanGreaterThanToken)
			}
			if s.charCodeAt(s.pos+1) == chEquals {
				s.pos += 2
				return s.set(GreaterThanGreaterThanEqualsToken)
			}
			s.pos++
			return s.set(GreaterThanGreaterThanToken)
		}
		if s.charCodeAt(s.pos) == chEquals {
			s.pos++
			return s.set(GreaterThanEqualsToken)
		}
	}
	return s.token
}

// scanState snapshots the mutable scanner state for speculation.
type scanState struct {
	pos, fullStartPos, tokenStart int
	token                         SyntaxKind
	tokenValue                    string
	tokenFlags                    TokenFlags
}

func (s *Scanner) snapshot() scanState {
	return scanState{s.pos, s.fullStartPos, s.tokenStart, s.token, s.tokenValue, s.tokenFlags}
}

func (s *Scanner) restore(st scanState) {
	s.pos, s.fullStartPos, s.tokenStart = st.pos, st.fullStartPos, st.tokenStart
	s.token, s.tokenValue, s.tokenFlags = st.token, st.tokenValue, st.tokenFlags
}

// LookAhead runs cb without consuming input (state is always restored).
func LookAhead[T any](s *Scanner, cb func() T) T {
	st := s.snapshot()
	result := cb()
	s.restore(st)
	return result
}

// TryScan runs cb, restoring state when the result is falsy (the zero value),
// matching the TS scanner's tryScan.
func TryScan[T any](s *Scanner, cb func() T) T {
	st := s.snapshot()
	result := cb()
	if reflect.ValueOf(&result).Elem().IsZero() {
		s.restore(st)
	}
	return result
}
