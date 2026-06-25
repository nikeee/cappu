// Port of src/format/comment-rewrite.ts (gjf's JavaCommentsHelper.rewrite path).
//
// Outer comment normalization, run at write time on every comment given the
// column it starts at. Javadoc first goes through the full reflow engine.

package format

import (
	"regexp"
	"strings"

	"github.com/nikeee/cappu/internal/format/javadoc"
)

const commentMaxLineLength = 100

// rewriteComment rewrites a comment for output at column0. isLine is true for
// `//` comments. Mirrors JavaCommentsHelper.rewrite.
func rewriteComment(text string, column0 int, isLine bool) string {
	if strings.HasPrefix(text, "/**") {
		text = javadoc.FormatJavadoc(text, column0)
	}
	rawLines := strings.Split(text, "\n")
	lines := make([]string, len(rawLines))
	for i, l := range rawLines {
		if isLine {
			lines[i] = strings.TrimSpace(l)
		} else {
			lines[i] = strings.TrimRight(l, " \t\r\n\f\v")
		}
	}
	if isLine {
		return indentLineComments(lines, column0)
	}
	if pc, ok := reformatParamComment(text); ok {
		return pc
	}
	if javadocShaped(lines) {
		return indentJavadoc(lines, column0)
	}
	return preserveIndentation(lines, column0)
}

var paramCommentRe = regexp.MustCompile(`^/\*\s*([\p{L}_$][\p{L}\p{N}_$]*(?:\.\.\.)?)\s*=\s*\*/$`)

// reformatParamComment normalizes `/*name=*/` -> `/* name= */`; ok=false if not
// a parameter comment.
func reformatParamComment(text string) (string, bool) {
	m := paramCommentRe.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	return "/* " + m[1] + "= */", true
}

func preserveIndentation(lines []string, column0 int) string {
	startCol := -1
	for i := 1; i < len(lines); i++ {
		idx := strings.IndexFunc(lines[i], func(r rune) bool { return !isSpaceRune(r) })
		if idx >= 0 && (startCol == -1 || idx < startCol) {
			startCol = idx
		}
	}
	var b strings.Builder
	b.WriteString(lines[0])
	pad := strings.Repeat(" ", column0)
	for i := 1; i < len(lines); i++ {
		b.WriteString("\n")
		b.WriteString(pad)
		if startCol >= 0 && len(lines[i]) >= startCol {
			b.WriteString(lines[i][startCol:])
		} else {
			b.WriteString(lines[i])
		}
	}
	return b.String()
}

func indentJavadoc(lines []string, column0 int) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(lines[0]))
	pad := strings.Repeat(" ", column0+1)
	for i := 1; i < len(lines); i++ {
		b.WriteString("\n")
		b.WriteString(pad)
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "*") {
			b.WriteString("* ")
		}
		b.WriteString(line)
	}
	return b.String()
}

func indentLineComments(lines []string, column0 int) string {
	lines = wrapLineComments(lines, column0)
	var b strings.Builder
	b.WriteString(strings.TrimSpace(lines[0]))
	pad := strings.Repeat(" ", column0)
	for i := 1; i < len(lines); i++ {
		b.WriteString("\n")
		b.WriteString(pad)
		b.WriteString(strings.TrimSpace(lines[i]))
	}
	return b.String()
}

var missingSpacePrefix = regexp.MustCompile(`^(//+)(?:[^\s/])`)
var allowedNoSpace = regexp.MustCompile(`^//(?:noinspection|\$NON-NLS-\d+\$)`)

func wrapLineComments(lines []string, column0 int) []string {
	var result []string
	for _, line := range lines {
		if m := missingSpacePrefix.FindStringSubmatch(line); m != nil && !allowedNoSpace.MatchString(line) {
			length := len(m[1])
			line = strings.Repeat("/", length) + " " + line[length:]
		}
		if strings.HasPrefix(line, "// MOE:") {
			result = append(result, line)
			continue
		}
		for len(line)+column0 > commentMaxLineLength {
			idx := commentMaxLineLength - column0
			for idx >= 2 && !isSpaceByte(line[idx]) {
				idx--
			}
			if idx <= 2 {
				break
			}
			result = append(result, line[:idx])
			line = "//" + line[idx:]
		}
		result = append(result, line)
	}
	return result
}

func javadocShaped(lines []string) bool {
	if len(lines) == 0 {
		return false
	}
	first := strings.TrimSpace(lines[0])
	if strings.HasPrefix(first, "/**") {
		return true
	}
	if !strings.HasPrefix(first, "/*") {
		return false
	}
	for i := 1; i < len(lines); i++ {
		if !strings.HasPrefix(strings.TrimSpace(lines[i]), "*") {
			return false
		}
	}
	return true
}

func isSpaceRune(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == '\v'
}
func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == '\v'
}
