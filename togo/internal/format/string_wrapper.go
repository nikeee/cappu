// Port of src/format/string-wrapper.ts (google-java-format's StringWrapper).
//
// The post-pass that reflows string literals which run past the column limit.
// gjf runs it on its own OUTPUT: the formatter lays the code out, this pass
// re-splits any `"..." + "..."` chain whose line is too long, and the formatter
// runs again over the rewritten source. FormatSource does the same.
//
// Only the long-string reflow is ported; gjf's text-block re-indentation in the
// same file is already handled by the printer's own text-block path.

package format

import (
	"regexp"
	"sort"
	"strings"

	"github.com/nikeee/cappu/internal/compiler"
)

const stringColumnLimit = 100

const textBlockDelim = `"""`

// replacement is [start, end) of the original text and its new spelling.
type replacement struct {
	start, end int
	text       string
}

// wrapLongStrings reflows the string literals in text (already-formatted Java)
// that reach past the column limit. It returns text unchanged when there is
// nothing to do, when the source does not parse, or when the rewrite would
// change what the strings spell - this pass must never alter the program, only
// where its strings are cut.
func wrapLongStrings(text string) string {
	if !needsWrapping(text) {
		return text
	}
	sf := compiler.ParseSourceFile("wrap.java", text)
	if len(sf.AsSourceFile().ParseDiagnostics) > 0 {
		return text
	}
	reps := collectStringReplacements(sf, text)
	if len(reps) == 0 {
		return text
	}
	// Apply back-to-front so earlier offsets stay valid.
	sort.Slice(reps, func(i, j int) bool { return reps[i].start > reps[j].start })
	out := text
	for _, r := range reps {
		out = out[:r.start] + r.text + out[r.end:]
	}
	if stringPayload(out) != stringPayload(text) {
		return text
	}
	return out
}

// needsWrapping is gjf's fast path: only a file with an over-long line can need
// this pass.
func needsWrapping(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if utf16Len(line) > stringColumnLimit {
			return true
		}
	}
	return false
}

func collectStringReplacements(sf *compiler.Node, text string) []replacement {
	parents := map[*compiler.Node]*compiler.Node{}
	var literals []*compiler.Node
	var walk func(n *compiler.Node)
	walk = func(n *compiler.Node) {
		n.ForEachChild(func(child *compiler.Node) bool {
			parents[child] = n
			if isLongStringLiteral(child, parents, text) {
				literals = append(literals, child)
			}
			walk(child)
			return false
		})
	}
	walk(sf)

	var out []replacement
	seen := map[int]bool{}
	for _, literal := range literals {
		// The outermost contiguous `+` chain the literal belongs to.
		enclosing := literal
		for {
			parent, ok := parents[enclosing]
			if !ok || !isConcat(parent) {
				break
			}
			enclosing = parent
		}
		flat := flattenConcat(enclosing)
		idx := -1
		for i, n := range flat {
			if n == literal {
				idx = i
				break
			}
		}
		if idx < 0 {
			continue
		}
		// Only the run of adjacent string literals around this one is reflowed.
		lo, hi := idx, idx
		for lo > 0 && isPlainString(flat[lo-1], text) {
			lo--
		}
		for hi+1 < len(flat) && isPlainString(flat[hi+1], text) {
			hi++
		}
		run := flat[lo : hi+1]
		start := stringStartOf(run[0], text)
		if seen[start] { // two literals of one chain produce one edit
			continue
		}
		seen[start] = true
		end := run[len(run)-1].End

		components := stringComponents(run, text)
		if len(components) == 0 {
			continue
		}
		out = append(out, replacement{
			start: start,
			end:   end,
			text: reflowString(
				components,
				utf16Len(text[lineStartAt(text, start):start]),
				utf16Len(text[end:lineEndAt(text, end)]),
				lo == 0,
			),
		})
	}
	return out
}

// isLongStringLiteral reports whether the literal's line runs past the column
// limit and gjf would reflow it: not a text block, and not the receiver of a
// dereference (`"...".length()`), whose line the wrap could not shorten anyway.
func isLongStringLiteral(n *compiler.Node, parents map[*compiler.Node]*compiler.Node, text string) bool {
	if n.Kind != compiler.StringLiteral {
		return false
	}
	if strings.HasPrefix(text[stringStartOf(n, text):], textBlockDelim) {
		return false
	}
	if parent, ok := parents[n]; ok && parent.Kind == compiler.PropertyAccessExpression &&
		parent.AsPropertyAccessExpression().Expression == n {
		return false
	}
	return utf16Len(text[lineStartAt(text, n.End):lineEndAt(text, n.End)]) > stringColumnLimit
}

func isConcat(n *compiler.Node) bool {
	return n.Kind == compiler.BinaryExpression &&
		n.AsBinaryExpression().OperatorToken == compiler.PlusToken
}

// flattenConcat flattens the `+` chain left to right (gjf's pre-order walk).
func flattenConcat(root *compiler.Node) []*compiler.Node {
	var flat []*compiler.Node
	todo := []*compiler.Node{root}
	for len(todo) > 0 {
		n := todo[0]
		todo = todo[1:]
		if isConcat(n) {
			b := n.AsBinaryExpression()
			todo = append([]*compiler.Node{b.Left, b.Right}, todo...)
			continue
		}
		flat = append(flat, n)
	}
	return flat
}

func isPlainString(n *compiler.Node, text string) bool {
	return n.Kind == compiler.StringLiteral &&
		!strings.HasPrefix(text[stringStartOf(n, text):], textBlockDelim)
}

// stringComponents splits the run into gjf's "words": each literal's text
// without its quotes, cut BEFORE whitespace and after an escaped newline, so a
// line can only be cut where gjf would cut it.
func stringComponents(run []*compiler.Node, text string) []string {
	var result []string
	piece := ""
	for _, n := range run {
		if !isPlainString(n, text) {
			return nil
		}
		body := text[stringStartOf(n, text)+1 : n.End-1]
		start := 0
		for idx := 0; idx < len(body); idx++ {
			switch {
			case isSpaceByte(body[idx]), strings.HasPrefix(body[idx:], `\t`):
				// Cut BEFORE the whitespace: it begins the next piece.
			default:
				k := escapedNewlineAt(body, idx)
				if k == 0 {
					continue
				}
				for k > 0 {
					idx += k
					k = escapedNewlineAt(body, idx)
				}
			}
			piece += body[start:idx]
			result = append(result, piece)
			piece = ""
			start = idx
		}
		// gjf flushes at the end of each literal BEFORE carrying its tail over, so
		// a literal with no cut point of its own still becomes its own component -
		// the source's existing `"..." + "..."` cuts are kept when nothing else
		// fits.
		if piece != "" {
			result = append(result, piece)
			piece = ""
		}
		if start < len(body) {
			piece += body[start:]
		}
	}
	if piece != "" {
		result = append(result, piece)
	}
	return result
}

func escapedNewlineAt(body string, i int) int {
	n := 0
	if strings.HasPrefix(body[i:], `\r`) {
		n += 2
	}
	if i+n <= len(body) && strings.HasPrefix(body[i+n:], `\n`) {
		n += 2
	}
	return n
}

// reflowString is gjf's greedy fill: words go on a line while they fit in width,
// which is the room left by the start column and the two quotes; the last line
// also has to leave room for trailing, and every line after the first pays for
// the +4 continuation indent and the `+ `.
func reflowString(components []string, startColumn, trailing int, first0 bool) string {
	width := stringColumnLimit - startColumn - 2
	input := append([]string(nil), components...)
	var lines []string
	first := first0
	for len(input) > 0 {
		length := 0
		var line []string
		if totalLengthAtMost(input, width) {
			width -= trailing
		}
		for len(input) > 0 && (length <= 4 || length+utf16Len(input[0]) <= width) {
			word := input[0]
			input = input[1:]
			line = append(line, word)
			length += utf16Len(word)
			if strings.HasSuffix(word, `\n`) || strings.HasSuffix(word, `\r`) {
				break
			}
		}
		if len(line) == 0 {
			line = append(line, input[0])
			input = input[1:]
		}
		lines = append(lines, strings.Join(line, ""))
		if first {
			width -= 6
			first = false
		}
	}
	pad := startColumn - 2
	if first0 {
		pad = startColumn + 4
	}
	if pad < 0 {
		pad = 0
	}
	sep := `"` + "\n" + strings.Repeat(" ", pad) + `+ "`
	return `"` + strings.Join(lines, sep) + `"`
}

func totalLengthAtMost(input []string, length int) bool {
	total := 0
	for _, s := range input {
		total += utf16Len(s)
		if total > length {
			return false
		}
	}
	return true
}

var stringLiteralRe = regexp.MustCompile(`"(?:\\.|[^"\\\n])*"`)

// stringPayload is every literal's body in order with whitespace removed: what
// the rewrite must not change (gjf compares re-parsed ASTs for the same reason).
func stringPayload(text string) string {
	var b strings.Builder
	for _, m := range stringLiteralRe.FindAllString(text, -1) {
		b.WriteString(m[1 : len(m)-1])
	}
	// No normalization: re-cutting a literal never moves a character, so the
	// concatenated bodies must come out byte-for-byte identical.
	return b.String()
}

func lineStartAt(text string, pos int) int {
	return strings.LastIndex(text[:pos], "\n") + 1
}

func lineEndAt(text string, pos int) int {
	if i := strings.Index(text[pos:], "\n"); i >= 0 {
		return pos + i
	}
	return len(text)
}

func stringStartOf(n *compiler.Node, text string) int {
	return compiler.SkipTrivia(text, n.Pos)
}

// utf16Len is the width of s in UTF-16 code units - the TypeScript build's
// string indices and gjf's Java chars. Counting bytes made every line holding a
// non-ASCII character look too long and wrap early.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == '\v'
}
