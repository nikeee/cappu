package javadoc

import "testing"

func TestFormatJavadoc(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		blockIndent int
		want        string
	}{
		{"collapse to one line", "/**\n* foo\n*   bar\n*/", 2, "/** foo bar */"},
		{"empty collapses", "/**\n */", 2, "/** */"},
		{"fitting one-liner unchanged", "/** Tests for foos. */", 0, "/** Tests for foos. */"},
		{
			"footer tags one per line",
			"/**\n * @param x the x\n * @return y\n */", 2,
			"/**\n   * @param x the x\n   * @return y\n   */",
		},
		{"bare tag stays multi-line", "/** @deprecated gone */", 2, "/**\n   * @deprecated gone\n   */"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FormatJavadoc(c.input, c.blockIndent); got != c.want {
				t.Errorf("FormatJavadoc(%q, %d):\n got %q\nwant %q", c.input, c.blockIndent, got, c.want)
			}
		})
	}
}

func TestFormatJavadocReturnsInputOnLexFailure(t *testing.T) {
	// Unbalanced <pre> -> LexException -> input returned unchanged.
	in := "/** <pre> unterminated */"
	if FormatJavadoc(in, 2) == "" {
		t.Errorf("expected a string result")
	}
}
