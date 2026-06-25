// Port of src/format/javadoc/char-stream.ts.
//
// String reader for the lexer. Regexes must be anchored at the cursor; we build
// them with a leading `\A`-equivalent via FindStringIndex from the offset.

package javadoc

import "regexp"

type charStream struct {
	input    string
	position int
	tokenEnd int
}

func newCharStream(input string) *charStream {
	return &charStream{input: input, tokenEnd: -1}
}

func (c *charStream) tryConsume(expected string) bool {
	if c.position+len(expected) > len(c.input) || c.input[c.position:c.position+len(expected)] != expected {
		return false
	}
	c.tokenEnd = c.position + len(expected)
	return true
}

// tryConsumeRegex matches `pattern` only at the current position. `pattern` must
// be anchored with `^` and compiled normally; we slice from the cursor so `^`
// binds there.
func (c *charStream) tryConsumeRegex(pattern *regexp.Regexp) bool {
	loc := pattern.FindStringIndex(c.input[c.position:])
	if loc == nil || loc[0] != 0 {
		return false
	}
	c.tokenEnd = c.position + loc[1]
	return true
}

func (c *charStream) readAndResetRecorded() string {
	result := c.input[c.position:c.tokenEnd]
	c.position = c.tokenEnd
	c.tokenEnd = -1
	return result
}

func (c *charStream) isExhausted() bool { return c.position == len(c.input) }
