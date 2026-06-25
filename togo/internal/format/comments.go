// Port of src/format/comments.ts.
//
// The parser discards trivia, so comments are not in the AST. To avoid losing
// them when reformatting, this pass recovers every comment from the source with
// its offset, classified as "own line" (stands on its own, attaches to the
// following construct) or "trailing" (sits after code on the same line). The
// printer re-emits them at member/statement granularity.
//
// Comments are found in the gaps between scanner tokens, so text inside string
// and character literals is never mistaken for a comment.

package format

import (
	"strings"

	"github.com/nikeee/cappu/internal/compiler"
)

// comment is a recovered source comment.
type comment struct {
	pos     int    // offset of the comment's first character
	end     int    // offset just past the comment
	text    string // the verbatim comment text: a line comment or a block/javadoc comment
	line    bool   // a // line comment (vs a block comment)
	ownLine bool   // true when only whitespace precedes the comment on its line
}

func collectComments(source string) []comment {
	var comments []comment
	scanner := compiler.NewScanner(source, func(compiler.DiagnosticMessage, int, int) {})
	prevEnd := 0
	for {
		kind := scanner.Scan()
		var start int
		if kind == compiler.EndOfFileToken {
			start = len(source)
		} else {
			start = scanner.TokenStart()
		}
		extractFromGap(source, prevEnd, start, &comments)
		if kind == compiler.EndOfFileToken {
			break
		}
		prevEnd = scanner.TokenEnd()
	}
	return comments
}

func extractFromGap(source string, from, to int, out *[]comment) {
	i := from
	// A comment is on its own line when nothing but whitespace precedes it back to
	// the last newline (or the gap began right after the previous token's line).
	sawNewlineSinceCode := from == 0
	for i < to {
		ch := source[i]
		if ch == '\n' {
			sawNewlineSinceCode = true
			i++
			continue
		}
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\f' || ch == '\v' {
			i++
			continue
		}
		if ch == '/' && i+1 < len(source) && source[i+1] == '/' {
			j := i + 2
			for j < to && source[j] != '\n' {
				j++
			}
			txt := strings.TrimRight(source[i:j], " \t\r\n\f\v")
			*out = append(*out, comment{pos: i, end: j, text: txt, line: true, ownLine: sawNewlineSinceCode})
			i = j
			sawNewlineSinceCode = false
			continue
		}
		if ch == '/' && i+1 < len(source) && source[i+1] == '*' {
			j := i + 2
			for j < to && (source[j] != '*' || j+1 >= len(source) || source[j+1] != '/') {
				j++
			}
			j = min(j+2, to)
			*out = append(*out, comment{pos: i, end: j, text: source[i:j], line: false, ownLine: sawNewlineSinceCode})
			i = j
			sawNewlineSinceCode = false
			continue
		}
		// Any other character would be code, which the scanner tokenizes; gaps only
		// hold whitespace and comments, so this is unreachable in practice.
		i++
	}
}
