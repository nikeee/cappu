// Port of src/compiler/numberParse.ts
//
// Helpers for validating a literal string passed to Integer.parseInt and its
// siblings. Only definite failures are reported: a radix outside [2, 36], or a
// digit not valid in the radix. Overflow is intentionally not checked.

package compiler

const (
	MinRadix = 2
	MaxRadix = 36
)

// digitValue returns the value of a Java digit character, or -1.
func digitValue(ch byte) int {
	switch {
	case ch >= '0' && ch <= '9':
		return int(ch - '0')
	case ch >= 'A' && ch <= 'Z':
		return int(ch-'A') + 10
	case ch >= 'a' && ch <= 'z':
		return int(ch-'a') + 10
	default:
		return -1
	}
}

// IsParseableInteger reports whether s parses as an integer in radix (Java
// Integer.parseInt rules).
func IsParseableInteger(s string, radix int) bool {
	body := s
	if len(s) > 0 && (s[0] == '+' || s[0] == '-') {
		body = s[1:]
	}
	if len(body) == 0 {
		return false // "", "+", "-"
	}
	for i := 0; i < len(body); i++ {
		d := digitValue(body[i])
		if d < 0 || d >= radix {
			return false
		}
	}
	return true
}
