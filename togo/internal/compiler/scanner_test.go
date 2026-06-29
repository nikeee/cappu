package compiler

import (
	"reflect"
	"testing"
)

type scannedToken struct {
	kind               SyntaxKind
	value              string
	text               string
	start, end         int
	flags              TokenFlags
	precedingLineBreak bool
}

func tokenize(src string) ([]scannedToken, []int) {
	var codes []int
	s := NewScanner(src, func(m DiagnosticMessage, pos, length int) { codes = append(codes, int(m.Code)) })
	var tokens []scannedToken
	for kind := s.Scan(); kind != EndOfFileToken; kind = s.Scan() {
		tokens = append(tokens, scannedToken{
			kind: kind, value: s.TokenValue(), text: s.TokenText(),
			start: s.TokenStart(), end: s.TokenEnd(), flags: s.TokenFlags(),
			precedingLineBreak: s.HasPrecedingLineBreak(),
		})
	}
	return tokens, codes
}

func kindsOf(src string) []SyntaxKind {
	tokens, _ := tokenize(src)
	ks := make([]SyntaxKind, len(tokens))
	for i, t := range tokens {
		ks[i] = t.kind
	}
	return ks
}

func eq(t *testing.T, label string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %v, want %v", label, got, want)
	}
}

// --- port of scanner.test.ts -------------------------------------------------

func TestKeywordsVsIdentifiers(t *testing.T) {
	tokens, _ := tokenize("class Foo int var")
	eq(t, "kinds", []SyntaxKind{tokens[0].kind, tokens[1].kind, tokens[2].kind, tokens[3].kind},
		[]SyntaxKind{ClassKeyword, Identifier, IntKeyword, Identifier})
	eq(t, "Foo", tokens[1].value, "Foo")
	eq(t, "var", tokens[3].value, "var")
}

func TestIdentifiersDollarUnderscore(t *testing.T) {
	eq(t, "kinds", kindsOf("$x _y a1 _"), []SyntaxKind{Identifier, Identifier, Identifier, Identifier})
}

func TestOperatorsMaximalMunch(t *testing.T) {
	eq(t, "kinds", kindsOf("+ ++ += -> :: ... < <= << <<= == != >>>="), []SyntaxKind{
		PlusToken, PlusPlusToken, PlusEqualsToken, ArrowToken, ColonColonToken, DotDotDotToken,
		LessThanToken, LessThanEqualsToken, LessThanLessThanToken, LessThanLessThanEqualsToken,
		EqualsEqualsToken, ExclamationEqualsToken,
		GreaterThanToken, GreaterThanToken, GreaterThanToken, EqualsToken,
	})
}

func TestNestedGenericsCloseCleanly(t *testing.T) {
	ks := kindsOf("List<List<T>>")
	eq(t, "last two", ks[len(ks)-2:], []SyntaxKind{GreaterThanToken, GreaterThanToken})
}

func TestReScanGreaterToken(t *testing.T) {
	cases := []struct {
		src      string
		expected SyntaxKind
	}{
		{">>", GreaterThanGreaterThanToken},
		{">>>", GreaterThanGreaterThanGreaterThanToken},
		{">=", GreaterThanEqualsToken},
		{">>=", GreaterThanGreaterThanEqualsToken},
		{">>>=", GreaterThanGreaterThanGreaterThanEqualsToken},
	}
	for _, c := range cases {
		s := NewScanner(c.src, nil)
		if s.Scan() != GreaterThanToken {
			t.Fatalf("%q: first token not '>'", c.src)
		}
		if got := s.ReScanGreaterToken(); got != c.expected {
			t.Errorf("%q: reScan = %v, want %v", c.src, got, c.expected)
		}
		if s.TokenEnd() != len(c.src) {
			t.Errorf("%q: end = %d, want %d", c.src, s.TokenEnd(), len(c.src))
		}
	}
}

func TestNumericFlags(t *testing.T) {
	tokens, _ := tokenize("0 42 0xFF 0b1010 0777 1_000 3.14 1.0f 2.0d 100L 1e10")
	for _, tok := range tokens {
		if tok.kind != NumericLiteral {
			t.Fatalf("non-numeric token: %v", tok.kind)
		}
	}
	check := func(i int, flag TokenFlags) {
		if tokens[i].flags&flag == 0 {
			t.Errorf("token %d (%q) missing flag %d", i, tokens[i].text, flag)
		}
	}
	check(2, HexSpecifier)
	check(3, BinarySpecifier)
	check(4, OctalSpecifier)
	check(5, ContainsUnderscore)
	check(7, FloatSuffix)
	check(8, DoubleSuffix)
	check(9, LongSuffix)
}

func TestLeadingDotFloat(t *testing.T) {
	tokens, _ := tokenize(".5")
	if len(tokens) != 1 || tokens[0].kind != NumericLiteral || tokens[0].value != ".5" {
		t.Errorf("got %+v", tokens)
	}
}

func TestStringEscapes(t *testing.T) {
	tokens, _ := tokenize(`"a\tb\n\"c\u0041"`)
	eq(t, "kind", tokens[0].kind, StringLiteral)
	eq(t, "value", tokens[0].value, "a\tb\n\"cA")
}

func TestCharacterLiteral(t *testing.T) {
	tokens, _ := tokenize(`'a' '\n'`)
	eq(t, "kinds", []SyntaxKind{tokens[0].kind, tokens[1].kind}, []SyntaxKind{CharacterLiteral, CharacterLiteral})
	eq(t, "a", tokens[0].value, "a")
	eq(t, "newline", tokens[1].value, "\n")
}

func TestTextBlock(t *testing.T) {
	tokens, codes := tokenize("\"\"\"\nhello\n\"\"\"")
	eq(t, "codes", codes, []int(nil))
	if len(tokens) != 1 || tokens[0].kind != TextBlockLiteral {
		t.Errorf("got %+v", tokens)
	}
}

func TestTriviaAndLineBreaks(t *testing.T) {
	tokens, _ := tokenize("a // comment\nb /* multi\nline */ c")
	eq(t, "kinds", []SyntaxKind{tokens[0].kind, tokens[1].kind, tokens[2].kind}, []SyntaxKind{Identifier, Identifier, Identifier})
	eq(t, "t0 lb", tokens[0].precedingLineBreak, false)
	eq(t, "t1 lb", tokens[1].precedingLineBreak, true)
	eq(t, "t2 lb", tokens[2].precedingLineBreak, true)
}

func TestTokenPositionsExcludeTrivia(t *testing.T) {
	tokens, _ := tokenize("  foo")
	eq(t, "start", tokens[0].start, 2)
	eq(t, "end", tokens[0].end, 5)
}

func TestUnterminatedString(t *testing.T) {
	tokens, codes := tokenize(`"abc`)
	eq(t, "kind", tokens[0].kind, StringLiteral)
	if tokens[0].flags&Unterminated == 0 {
		t.Error("expected Unterminated flag")
	}
	eq(t, "codes len", len(codes), 1)
}

func TestUnterminatedBlockComment(t *testing.T) {
	_, codes := tokenize("/* never closed")
	eq(t, "codes len", len(codes), 1)
}

func TestInvalidCharacter(t *testing.T) {
	tokens, codes := tokenize("a # b")
	eq(t, "codes len", len(codes), 1)
	eq(t, "kinds", []SyntaxKind{tokens[0].kind, tokens[1].kind, tokens[2].kind}, []SyntaxKind{Identifier, Unknown, Identifier})
}

func TestLookAheadRestores(t *testing.T) {
	s := NewScanner("a b", nil)
	eq(t, "first", s.Scan(), Identifier)
	peeked := LookAhead(s, func() SyntaxKind { return s.Scan() })
	eq(t, "peeked", peeked, Identifier)
	eq(t, "still a", s.TokenValue(), "a")
	eq(t, "next", s.Scan(), Identifier)
	eq(t, "now b", s.TokenValue(), "b")
}

func TestUnicodeEscapesAndUnderscores(t *testing.T) {
	tokens, _ := tokenize(`"\u0041\u0042" 0xFF_FF 0b1010_1010 1_000_000L`)
	eq(t, "AB", tokens[0].value, "AB")
	if tokens[1].flags&HexSpecifier == 0 || tokens[1].flags&ContainsUnderscore == 0 {
		t.Error("hex/underscore flags missing")
	}
	if tokens[3].flags&LongSuffix == 0 {
		t.Error("long suffix missing")
	}
}

func TestCRLFLineBreak(t *testing.T) {
	tokens, _ := tokenize("a\r\nb")
	eq(t, "t1 lb", tokens[1].precedingLineBreak, true)
}

func TestCharOctalUnicodeEscapes(t *testing.T) {
	tokens, _ := tokenize(`'\101' '\u0041'`)
	eq(t, "octal A", tokens[0].value, "A")
	eq(t, "unicode A", tokens[1].value, "A")
}

func TestConsecutiveLessThan(t *testing.T) {
	eq(t, "kinds", kindsOf("a < < b"), []SyntaxKind{Identifier, LessThanToken, LessThanToken, Identifier})
}

func TestStringEscapeEdgeCases(t *testing.T) {
	// \s is a space (SE15); multiple u's may precede the 4 hex digits; an
	// unknown escape passes the char through; \' decodes to a single quote.
	tokens, codes := tokenize(`"a\sb\uuuu0041\q\'"`)
	eq(t, "codes", codes, []int(nil))
	eq(t, "value", tokens[0].value, "a bAq'")
}

func TestStringEscapeOctalCutoff(t *testing.T) {
	// \777 exceeds 0xff, so only \77 (= 0x3f = '?') is consumed and the
	// trailing 7 is a literal character.
	tokens, _ := tokenize(`"\777"`)
	eq(t, "value", tokens[0].value, "?7")
}

func TestIncompleteUnicodeEscape(t *testing.T) {
	// Fewer than 4 hex digits after \u reports a diagnostic.
	tokens, codes := tokenize(`"\u12"`)
	eq(t, "kind", tokens[0].kind, StringLiteral)
	eq(t, "codes", codes, []int{1104}) // HexadecimalDigitExpected
}

func TestTrailingBackslashAtEOF(t *testing.T) {
	// A backslash with nothing after it decodes to a literal backslash; the
	// string is also unterminated.
	tokens, _ := tokenize("\"\\")
	eq(t, "value", tokens[0].value, "\\")
	if tokens[0].flags&Unterminated == 0 {
		t.Error("expected Unterminated flag")
	}
}

func TestHexFloats(t *testing.T) {
	tokens, codes := tokenize("0x1p1023 0x1.8p-3 0x1.0p0d")
	eq(t, "codes", codes, []int(nil))
	eq(t, "kinds", []SyntaxKind{tokens[0].kind, tokens[1].kind, tokens[2].kind}, []SyntaxKind{NumericLiteral, NumericLiteral, NumericLiteral})
	if tokens[2].flags&DoubleSuffix == 0 {
		t.Error("double suffix missing")
	}
}

// --- port of scanner.edge.test.ts --------------------------------------------

func TestHexFloatNegativeExponent(t *testing.T) {
	tokens, codes := tokenize("0x1p-3 0x.8p1 0x1.8p3f")
	eq(t, "codes", codes, []int(nil))
	for _, tok := range tokens {
		if tok.kind != NumericLiteral {
			t.Fatalf("non-numeric: %v", tok.kind)
		}
	}
	if tokens[2].flags&FloatSuffix == 0 {
		t.Error("float suffix missing")
	}
}

func TestUnderscoresInFractionExponent(t *testing.T) {
	tokens, codes := tokenize("1_000.000_1 1_0e1_0")
	eq(t, "codes", codes, []int(nil))
	eq(t, "len", len(tokens), 2)
	if tokens[0].flags&ContainsUnderscore == 0 {
		t.Error("underscore flag missing")
	}
}

func TestLeadingZeroOctal(t *testing.T) {
	tokens, _ := tokenize("0 00 0777 0L")
	if tokens[0].flags&OctalSpecifier != 0 {
		t.Error("plain 0 should not be octal")
	}
	if tokens[1].flags&OctalSpecifier == 0 || tokens[2].flags&OctalSpecifier == 0 {
		t.Error("00 / 0777 should be octal")
	}
	if tokens[3].flags&LongSuffix == 0 {
		t.Error("0L should be long")
	}
}

func TestCharEscapesDecode(t *testing.T) {
	tokens, _ := tokenize(`'A' '\377' '\0'`)
	eq(t, "u0041", tokens[0].value, "A")
	eq(t, "377", tokens[1].value, string(rune(255)))
	eq(t, "0", tokens[2].value, string(rune(0))) // \0 octal = NUL

}

func TestTextBlockEmbeddedQuotes(t *testing.T) {
	tokens, codes := tokenize("\"\"\"\nsay \"hi\" there\n\"\"\"")
	eq(t, "codes", codes, []int(nil))
	if len(tokens) != 1 || tokens[0].kind != TextBlockLiteral {
		t.Errorf("got %+v", tokens)
	}
}

func TestBlockCommentNoNesting(t *testing.T) {
	eq(t, "kinds", kindsOf("/* a /* still */ x */ y"), []SyntaxKind{Identifier, AsteriskToken, SlashToken, Identifier})
}

func TestCompoundShiftAssign(t *testing.T) {
	eq(t, "kinds", kindsOf("<<="), []SyntaxKind{LessThanLessThanEqualsToken})
}

func TestUnderscoreDollarIdentifiers(t *testing.T) {
	want := []SyntaxKind{Identifier, Identifier, Identifier, Identifier, Identifier}
	eq(t, "kinds", kindsOf("_ $ $a a$b _x_"), want)
}

func TestArrowColonVarargsAt(t *testing.T) {
	eq(t, "kinds", kindsOf("-> :: ... @"), []SyntaxKind{ArrowToken, ColonColonToken, DotDotDotToken, AtToken})
}

func TestStringSimpleEscapes(t *testing.T) {
	tokens, _ := tokenize(`"\b\t\n\f\r\"\\"`)
	eq(t, "value", tokens[0].value, "\b\t\n\f\r\"\\")
}

func TestEmptyAndWhitespace(t *testing.T) {
	eq(t, "empty", kindsOf(""), []SyntaxKind{})
	eq(t, "whitespace", kindsOf("   \n\t  "), []SyntaxKind{})
}

func TestErrorCodes(t *testing.T) {
	cases := []struct {
		src  string
		want []int
	}{
		{"'a", []int{1101}}, {"'", []int{1101}},
		{"0x", []int{1104}}, {"0xG", []int{1104}},
		{"0b", []int{1105}}, {"0b2", []int{1105}},
		{"1e", []int{1103}}, {"0x1p", []int{1103}},
	}
	for _, c := range cases {
		_, codes := tokenize(c.src)
		eq(t, c.src, codes, c.want)
	}
}
