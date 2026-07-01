// Port of src/compiler/numberParse.test.ts
package compiler

import "testing"

func TestParseValidDecimal(t *testing.T) {
	for _, s := range []string{"0", "42", "-7", "+15", "2147483648"} {
		if !IsParseableInteger(s, 10) {
			t.Errorf("IsParseableInteger(%q,10) = false, want true", s)
		}
	}
}

func TestParseInvalidDecimal(t *testing.T) {
	for _, s := range []string{"", "+", "-", "12a", "1.5", "0x1F", "1_000", " 3"} {
		if IsParseableInteger(s, 10) {
			t.Errorf("IsParseableInteger(%q,10) = true, want false", s)
		}
	}
}

func TestParseRadix(t *testing.T) {
	cases := []struct {
		s     string
		radix int
		want  bool
	}{
		{"FF", 16, true},
		{"ff", 16, true},
		{"1010", 2, true},
		{"2", 2, false},
		{"8", 8, false},
	}
	for _, tc := range cases {
		if got := IsParseableInteger(tc.s, tc.radix); got != tc.want {
			t.Errorf("IsParseableInteger(%q,%d) = %v, want %v", tc.s, tc.radix, got, tc.want)
		}
	}
}
