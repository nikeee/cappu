package compiler

import "testing"

// The name table must cover exactly the real kinds (markers excluded).
func TestSyntaxKindNamesComplete(t *testing.T) {
	if len(syntaxKindNames) != int(kindCount) {
		t.Fatalf("syntaxKindNames has %d entries, want %d (kindCount)", len(syntaxKindNames), int(kindCount))
	}
	spot := map[SyntaxKind]string{
		Unknown:         "Unknown",
		NumericLiteral:  "NumericLiteral",
		ClassKeyword:    "ClassKeyword",
		Identifier:      "Identifier",
		SourceFile:      "SourceFile",
		MatchAllPattern: "MatchAllPattern",
	}
	for k, want := range spot {
		if k.String() != want {
			t.Errorf("%d.String() = %q, want %q", int(k), k.String(), want)
		}
	}
}

func TestKeywordRanges(t *testing.T) {
	if !IsKeyword(ClassKeyword) || !IsKeyword(NullKeyword) {
		t.Error("class/null should be keywords")
	}
	if IsKeyword(Identifier) || IsKeyword(PlusToken) {
		t.Error("identifier/plus should not be keywords")
	}
	if !IsReservedWord(WhileKeyword) || IsReservedWord(NullKeyword) {
		t.Error("reserved-word range wrong (null is a literal, not a reserved word)")
	}
}
