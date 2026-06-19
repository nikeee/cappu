package compiler

import "testing"

// Port of src/compiler/types.test.ts (the range-marker, keyword round-trip,
// punctuation, predicate and diagnostic-format cases; the SyntaxKind name-table
// completeness and keyword-range cases live in kinds_test.go).

func TestRangeMarkersOrdered(t *testing.T) {
	pairs := [][2]SyntaxKind{
		{FirstLiteralToken, LastLiteralToken},
		{FirstPunctuation, LastPunctuation},
		{FirstAssignment, LastAssignment},
		{FirstKeyword, LastKeyword},
		{FirstReservedWord, LastReservedWord},
		{FirstTypeNode, LastTypeNode},
		{FirstStatement, LastStatement},
		{FirstExpression, LastExpression},
	}
	for _, p := range pairs {
		if p[0] > p[1] {
			t.Errorf("range marker out of order: %v > %v", p[0], p[1])
		}
	}
}

func TestEveryKeywordInRange(t *testing.T) {
	for _, kind := range textToKeyword {
		if kind < FirstKeyword || kind > LastKeyword || !IsKeyword(kind) {
			t.Errorf("keyword kind %v not inside the keyword range", kind)
		}
	}
}

func TestKeywordTextRoundTrips(t *testing.T) {
	for text, kind := range textToKeyword {
		if got := tokenToString(kind); got != text {
			t.Errorf("tokenToString(%v) = %q, want %q", kind, got, text)
		}
	}
}

func TestIdentifierNotKeyword(t *testing.T) {
	if IsKeyword(Identifier) {
		t.Error("Identifier should not be a keyword")
	}
}

func TestTokenToStringPunctuation(t *testing.T) {
	if got := tokenToString(OpenBraceToken); got != "{" {
		t.Errorf("OpenBraceToken = %q, want {", got)
	}
	if got := tokenToString(GreaterThanGreaterThanGreaterThanToken); got != ">>>" {
		t.Errorf("got %q, want >>>", got)
	}
	if got := tokenToString(ArrowToken); got != "->" {
		t.Errorf("got %q, want ->", got)
	}
	if got := tokenToString(Identifier); got != "" {
		t.Errorf("Identifier spelling = %q, want empty", got)
	}
}

func TestModifierAndPrimitivePredicates(t *testing.T) {
	if !isModifierKeyword(PublicKeyword) || isModifierKeyword(ClassKeyword) {
		t.Error("modifier predicate wrong")
	}
	if !isPrimitiveTypeKeyword(IntKeyword) || isPrimitiveTypeKeyword(VoidKeyword) {
		t.Error("primitive predicate wrong")
	}
}

func TestDiagnosticsFormat(t *testing.T) {
	if got := FormatMessage(Diagnostics.Expected0, ";"); got != "';' expected." {
		t.Errorf("formatMessage = %q", got)
	}
	d := CreateDiagnostic(5, 3, Diagnostics.Expected0, "}")
	if d.Pos != 5 || d.End != 8 || d.MessageText != "'}' expected." || d.Code != Diagnostics.Expected0.Code {
		t.Errorf("diagnostic = %+v", d)
	}
}
