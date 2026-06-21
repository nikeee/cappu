package compiler

import "testing"

// Port of src/compiler/lineMap.test.ts.

func eqInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestComputeLineStarts(t *testing.T) {
	got := ComputeLineStarts("a\nbb\r\nccc\rd")
	if !eqInts(got, []int{0, 2, 6, 10}) {
		t.Errorf("got %v, want [0 2 6 10]", got)
	}
}

func TestOffsetToLineCharacter(t *testing.T) {
	text := "ab\ncd\nef"
	starts := ComputeLineStarts(text)
	cases := []struct {
		offset    int
		line, col int
	}{{0, 0, 0}, {1, 0, 1}, {3, 1, 0}, {7, 2, 1}}
	for _, tc := range cases {
		lc := GetLineAndCharacterOfPosition(text, starts, tc.offset)
		if lc.Line != tc.line || lc.Character != tc.col {
			t.Errorf("offset %d -> %v, want {%d %d}", tc.offset, lc, tc.line, tc.col)
		}
	}
}

func TestLineCharacterRoundTrip(t *testing.T) {
	text := "package p;\nclass C {\n  int x;\n}\n"
	starts := ComputeLineStarts(text)
	for offset := 0; offset <= len(text); offset++ {
		lc := GetLineAndCharacterOfPosition(text, starts, offset)
		if got := GetPositionOfLineAndCharacter(text, starts, lc.Line, lc.Character); got != offset {
			t.Errorf("round-trip offset %d -> %d", offset, got)
		}
	}
}

func TestEmptySourceLineStarts(t *testing.T) {
	if got := ComputeLineStarts(""); !eqInts(got, []int{0}) {
		t.Errorf("got %v, want [0]", got)
	}
	if lc := GetLineAndCharacterOfPosition("", []int{0}, 0); lc.Line != 0 || lc.Character != 0 {
		t.Errorf("got %v, want {0 0}", lc)
	}
}

// The character column counts Unicode code points, not bytes (JLS 3.1): a 2-byte
// 'é' and a 4-byte '😀' each advance the column by one, and the byte offset
// round-trips through that column.
func TestCodePointColumns(t *testing.T) {
	text := "café = 1;\n😀 x;"
	starts := ComputeLineStarts(text)
	// "café " is 5 code points but 6 bytes (é is 2 bytes); '=' sits at column 5.
	eqByte := len("café ")
	if lc := GetLineAndCharacterOfPosition(text, starts, eqByte); lc.Line != 0 || lc.Character != 5 {
		t.Errorf("'=' column: got %v, want {0 5}", lc)
	}
	if got := GetPositionOfLineAndCharacter(text, starts, 0, 5); got != eqByte {
		t.Errorf("column 5 -> byte %d, want %d", got, eqByte)
	}
	// '😀' is one code point (4 bytes); the 'x' after "😀 " is at column 2.
	xByte := starts[1] + len("😀 ")
	if lc := GetLineAndCharacterOfPosition(text, starts, xByte); lc.Line != 1 || lc.Character != 2 {
		t.Errorf("emoji column: got %v, want {1 2}", lc)
	}
	if got := GetPositionOfLineAndCharacter(text, starts, 1, 2); got != xByte {
		t.Errorf("emoji column 2 -> byte %d, want %d", got, xByte)
	}
}
