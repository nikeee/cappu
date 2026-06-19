package compiler

// Mapping between character offsets (used throughout the parser) and LSP
// line/character positions (0-based). Mirrors the TS compiler's
// computeLineStarts / getLineAndCharacter. Port of src/compiler/lineMap.ts.
//
// Note: offsets here are byte offsets (the Go port's model), matching the rest
// of the front end; for ASCII fixtures these equal the TS UTF-16 indices.

// LineAndCharacter is a 0-based line + character (column) pair.
type LineAndCharacter struct {
	Line      int
	Character int
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

// GetLineAndCharacterOfPosition converts an offset to a line/character pair.
func GetLineAndCharacterOfPosition(lineStarts []int, offset int) LineAndCharacter {
	line := lineIndexOf(lineStarts, offset)
	return LineAndCharacter{Line: line, Character: offset - lineStarts[line]}
}

// GetPositionOfLineAndCharacter converts a line/character pair to an offset.
func GetPositionOfLineAndCharacter(lineStarts []int, line, character int) int {
	if line < 0 {
		return 0
	}
	if line >= len(lineStarts) {
		return lineStarts[len(lineStarts)-1]
	}
	return lineStarts[line] + character
}
