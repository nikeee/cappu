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
	starts := ComputeLineStarts("ab\ncd\nef")
	cases := []struct {
		offset    int
		line, col int
	}{{0, 0, 0}, {1, 0, 1}, {3, 1, 0}, {7, 2, 1}}
	for _, tc := range cases {
		lc := GetLineAndCharacterOfPosition(starts, tc.offset)
		if lc.Line != tc.line || lc.Character != tc.col {
			t.Errorf("offset %d -> %v, want {%d %d}", tc.offset, lc, tc.line, tc.col)
		}
	}
}

func TestLineCharacterRoundTrip(t *testing.T) {
	text := "package p;\nclass C {\n  int x;\n}\n"
	starts := ComputeLineStarts(text)
	for offset := 0; offset <= len(text); offset++ {
		lc := GetLineAndCharacterOfPosition(starts, offset)
		if got := GetPositionOfLineAndCharacter(starts, lc.Line, lc.Character); got != offset {
			t.Errorf("round-trip offset %d -> %d", offset, got)
		}
	}
}

func TestEmptySourceLineStarts(t *testing.T) {
	if got := ComputeLineStarts(""); !eqInts(got, []int{0}) {
		t.Errorf("got %v, want [0]", got)
	}
	if lc := GetLineAndCharacterOfPosition([]int{0}, 0); lc.Line != 0 || lc.Character != 0 {
		t.Errorf("got %v, want {0 0}", lc)
	}
}
