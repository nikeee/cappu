package compiler

import (
	"regexp"
	"testing"
)

// Port of src/compiler/utilities.test.ts.

func TestIsValidIdentifier(t *testing.T) {
	cases := map[string]bool{
		"foo": true, "_x$2": true, "$": true,
		"2foo": false, "a b": false, "": false, "class": false, "true": false,
	}
	for name, want := range cases {
		if got := IsValidIdentifier(name); got != want {
			t.Errorf("IsValidIdentifier(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestSkipTriviaWhitespace(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"   x", 3}, {"\n\t x", 3}, {"x", 0},
	}
	for _, tc := range cases {
		if got := SkipTrivia(tc.text, 0); got != tc.want {
			t.Errorf("SkipTrivia(%q) = %d, want %d", tc.text, got, tc.want)
		}
	}
}

func TestSkipTriviaComments(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"// note\nx", 8}, {"/* note */ x", 11}, {"  /*a*/ /*b*/  x", 15},
	}
	for _, tc := range cases {
		if got := SkipTrivia(tc.text, 0); got != tc.want {
			t.Errorf("SkipTrivia(%q) = %d, want %d", tc.text, got, tc.want)
		}
	}
}

func TestIdentifierRangeTrimmedBySkipTrivia(t *testing.T) {
	text := "class C {\n    int field;\n    int m() { return field; }\n}"
	sf := ParseSourceFile("T.java", text)
	var ids []*Node
	var walk Visitor
	walk = func(n *Node) bool {
		if n.Kind == Identifier && n.AsIdentifier().Text == "field" {
			ids = append(ids, n)
		}
		n.ForEachChild(walk)
		return false
	}
	sf.ForEachChild(walk)
	useNode := ids[len(ids)-1]
	if !regexp.MustCompile(`\s`).MatchString(string(text[useNode.Pos])) {
		t.Error("node.pos should sit on whitespace before the name")
	}
	start := SkipTrivia(text, useNode.Pos)
	if got := text[start:useNode.End]; got != "field" {
		t.Errorf("trimmed range = %q, want field", got)
	}
}
