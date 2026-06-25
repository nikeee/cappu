// Port of src/format/javadoc/token.ts.
//
// Javadoc token taxonomy. The lexer produces these; the writer renders them.

package javadoc

// Kind is the token type. It replaces gjf's sealed record hierarchy.
type Kind int

const (
	beginJavadoc Kind = iota
	endJavadoc
	footerJavadocTagStart
	snippetBegin
	snippetEnd
	listOpen
	listClose
	listItemOpen
	listItemClose
	headerOpen
	headerClose
	paragraphOpen
	paragraphClose
	blockquoteOpen
	blockquoteClose
	preOpen
	preClose
	codeOpen
	codeClose
	tableOpen
	tableClose
	moeBeginStrip
	moeEndStrip
	htmlComment
	br
	markdownCodeSpanStart
	markdownCodeSpanEnd
	markdownFencedCodeBlock
	markdownTable
	whitespace
	forcedNewline
	markdownHardLineBreak
	optionalLineBreak
	literal
)

// Token is a javadoc token; `value` is its text.
type Token struct {
	kind  Kind
	value string
}

func tok(kind Kind, value string) Token { return Token{kind: kind, value: value} }

// isStartOfLine reports tokens always pinned to the following token (no break or
// space after them): `<p>`, `<li>`, headers.
func isStartOfLine(k Kind) bool {
	return k == listItemOpen || k == headerOpen || k == paragraphOpen
}
