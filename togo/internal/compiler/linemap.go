package compiler

// Mapping between character offsets (used throughout the parser) and LSP
// line/character positions (0-based). Mirrors the TS compiler's
// computeLineStarts / getLineAndCharacter. Port of src/compiler/lineMap.ts.
//
// Offsets are byte offsets into the source (the Go port's internal model, used
// for slicing). The character COLUMN, however, is a count of Unicode code
// points, not bytes: the JLS treats a program as a sequence of Unicode
// characters (JLS 3.1) and classifies tokens over code points (JLS 3.8, the
// int-taking Character.isJavaIdentifier* methods), so a column is "how many
// characters into the line". For ASCII this equals the byte column; for
// multi-byte UTF-8 it counts characters (LSP utf-32 semantics). The TS build
// reports UTF-16 code units instead (an artifact of JS string indices); the two
// agree on the Basic Multilingual Plane and differ only for supplementary
// characters, where the JLS code-point view is the correct one.

import "unicode/utf8"

// LineAndCharacter is a 0-based line + character (column) pair.
type LineAndCharacter struct {
	Line      int
	Character int
}

// LineStarts returns the file's line starts, computed once per parsed file
// (edits produce a new SourceFile). Callers must not mutate the slice. Port of
// lineStartsOf in src/compiler/lineMap.ts.
func (d *SourceFileData) LineStarts() []int {
	if d.lineStarts == nil {
		d.lineStarts = ComputeLineStarts(d.Text)
	}
	return d.lineStarts
}

// ComputeLineStarts returns the offsets at which each line begins (line 0 at 0).
func ComputeLineStarts(text string) []int {
	result := []int{0}
	pos := 0
	for pos < len(text) {
		ch := text[pos]
		pos++
		switch ch {
		case 0x0d: // carriage return
			if pos < len(text) && text[pos] == 0x0a {
				pos++
			}
			result = append(result, pos)
		case 0x0a: // line feed
			result = append(result, pos)
		}
	}
	return result
}

// lineIndexOf returns the greatest index i with lineStarts[i] <= offset.
func lineIndexOf(lineStarts []int, offset int) int {
	low, high := 0, len(lineStarts)-1
	for low < high {
		mid := (low + high + 1) >> 1
		if lineStarts[mid] <= offset {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return low
}

// GetLineAndCharacterOfPosition converts a byte offset to a line + character,
// where character counts Unicode code points from the line start (JLS 3.1).
func GetLineAndCharacterOfPosition(text string, lineStarts []int, offset int) LineAndCharacter {
	if offset > len(text) {
		offset = len(text)
	}
	line := lineIndexOf(lineStarts, offset)
	return LineAndCharacter{Line: line, Character: utf8.RuneCountInString(text[lineStarts[line]:offset])}
}

// GetPositionOfLineAndCharacter converts a line + code-point character to a byte
// offset, walking character code points forward from the start of the line.
func GetPositionOfLineAndCharacter(text string, lineStarts []int, line, character int) int {
	if line < 0 {
		return 0
	}
	if line >= len(lineStarts) {
		return lineStarts[len(lineStarts)-1]
	}
	b := lineStarts[line]
	for i := 0; i < character && b < len(text); i++ {
		_, size := utf8.DecodeRuneInString(text[b:])
		b += size
	}
	return b
}
