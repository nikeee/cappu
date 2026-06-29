package compiler

import "testing"

// Port of src/compiler/constfold.test.ts. fold parses the expression in the
// `return <expr>;` of a tiny method and folds it.
func fold(t *testing.T, expr string) *ConstValue {
	t.Helper()
	sf := ParseSourceFile("T.java", "class T { Object m() { return "+expr+"; } }")
	var result *ConstValue
	var walk Visitor
	walk = func(n *Node) bool {
		if n.Kind == ReturnStatement {
			if e := n.AsReturnStatement().Expression; e != nil {
				result = FoldConstant(e)
			}
		}
		n.ForEachChild(walk)
		return false
	}
	sf.ForEachChild(walk)
	return result
}

func wantInt(t *testing.T, expr string, v int64) {
	t.Helper()
	got := fold(t, expr)
	if got == nil || got.Kind != ConstInt || got.Int != v {
		t.Errorf("fold(%q) = %+v, want int %d", expr, got, v)
	}
}

func wantLong(t *testing.T, expr string, v int64) {
	t.Helper()
	got := fold(t, expr)
	if got == nil || got.Kind != ConstLong || got.Int != v {
		t.Errorf("fold(%q) = %+v, want long %d", expr, got, v)
	}
}

func wantBool(t *testing.T, expr string, v bool) {
	t.Helper()
	got := fold(t, expr)
	if got == nil || got.Kind != ConstBool || got.Bool != v {
		t.Errorf("fold(%q) = %+v, want bool %v", expr, got, v)
	}
}

func wantNil(t *testing.T, expr string) {
	t.Helper()
	if got := fold(t, expr); got != nil {
		t.Errorf("fold(%q) = %+v, want nil", expr, got)
	}
}

func TestArithmeticIntWraparound(t *testing.T) {
	wantInt(t, "6 * 7", 42)
	wantInt(t, "10 / 3 + 7 % 4", 6)
	wantInt(t, "(1 + 2) * (3 + 4)", 21)
	wantInt(t, "-(2 + 3)", -5)
	wantInt(t, "2147483647 + 1", -2147483648)
}

func TestLongArithmetic64Bit(t *testing.T) {
	wantLong(t, "100L * 100L", 10000)
	wantLong(t, "1L << 40", 1099511627776)
}

func TestShiftsAndBitwise(t *testing.T) {
	wantInt(t, "1 << 10", 1024)
	wantInt(t, "-1 >>> 28", 15)
	wantInt(t, "12 & 10", 8)
	wantInt(t, "5 ^ 3", 6)
}

func TestComparisonsAndBooleanLogic(t *testing.T) {
	wantBool(t, "3 < 5", true)
	wantBool(t, "3 >= 5", false)
	wantBool(t, "true && false", false)
	wantBool(t, "true || false", true)
	wantBool(t, "!false", true)
}

func TestIntOverflow32Bit(t *testing.T) {
	wantInt(t, "2147483647 + 1", -2147483648)
	wantInt(t, "-2147483648 - 1", 2147483647)
	wantInt(t, "2147483647 * 2", -2)
	wantInt(t, "-2147483648", -2147483648)
	wantInt(t, "-(-2147483648)", -2147483648)
	wantInt(t, "-2147483648 / -1", -2147483648)
}

func TestLongOverflow64Bit(t *testing.T) {
	wantLong(t, "9223372036854775807L + 1L", -9223372036854775808)
	wantLong(t, "9223372036854775807L * 2L", -2)
	wantLong(t, "1L << 40", 1099511627776)
}

func TestShiftDistanceMasked(t *testing.T) {
	wantInt(t, "1 << 32", 1)
	wantInt(t, "1 << 33", 2)
	wantLong(t, "1L << 64", 1)
	wantInt(t, "-8 >> 1", -4)
	wantInt(t, "-8 >>> 1", 2147483644)
}

func TestMixedIntLongPromotes(t *testing.T) {
	wantLong(t, "1000000 * 1000000L", 1000000000000)
	wantLong(t, "2147483647 + 1L", 2147483648)
}

func TestHexAndBinaryLiterals(t *testing.T) {
	wantInt(t, "0xff", 255)
	wantInt(t, "0xe", 14)
	wantInt(t, "0xd", 13)
	wantInt(t, "0xff + 1", 256)
	wantInt(t, "0xFFFFFFFF", -1)
	wantLong(t, "0xFFL", 255)
	wantInt(t, "0b1010", 10)
	wantNil(t, "0x1.8p1")
}

func TestNonConstantAndDivByZero(t *testing.T) {
	wantNil(t, "m()")
	wantNil(t, "1 / 0")
	wantNil(t, "1.5 + 2.5")
}

func TestAllComparisonOperators(t *testing.T) {
	wantBool(t, "5 > 3", true)
	wantBool(t, "3 > 5", false)
	wantBool(t, "3 <= 3", true)
	wantBool(t, "5 == 5", true)
	wantBool(t, "5 == 6", false)
	wantBool(t, "5 != 3", true)
	wantBool(t, "5 != 5", false)
}

func TestPrefixPlusAndComplement(t *testing.T) {
	wantInt(t, "+5", 5)
	wantInt(t, "~5", -6)
	wantInt(t, "~0", -1)
	wantLong(t, "~5L", -6)
	// `~` and unary `-`/`+` are not constant on a boolean operand.
	wantNil(t, "~true")
	wantNil(t, "!5") // logical NOT requires a boolean operand
}

func TestBooleanBitwiseOperators(t *testing.T) {
	wantBool(t, "true | false", true)   // BarToken on booleans
	wantBool(t, "true & true", true)    // AmpersandToken on booleans
	wantBool(t, "true ^ true", false)   // CaretToken on booleans
	wantBool(t, "true ^ false", true)   //
	wantBool(t, "true == false", false) // EqualsEqualsToken on booleans
	wantBool(t, "true != false", true)  // ExclamationEqualsToken on booleans
}

func TestIntegerBitwiseOrAndModulo(t *testing.T) {
	wantInt(t, "5 | 3", 7)
	wantInt(t, "-10 % 3", -1) // Java %: result takes the sign of the dividend
	wantInt(t, "10 % -3", 1)
	wantInt(t, "5 << 0", 5) // shift distance of zero is a no-op
}

func TestMixedIntLongBitwisePromotes(t *testing.T) {
	wantLong(t, "5 & 3L", 1)
	wantLong(t, "5 | 3L", 7)
	wantLong(t, "5 ^ 3L", 6)
}
