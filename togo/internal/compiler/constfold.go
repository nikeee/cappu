package compiler

// Compile-time constant folding for primitive constant expressions (JLS 15.28),
// matching javac: an expression built from literals and operators is evaluated at
// compile time so the emitter can push the folded constant. Integer arithmetic
// wraps in two's complement (32-bit int, 64-bit long). Float/double/char/String
// and `final` constant variables are not folded yet. Port of src/compiler/constfold.ts.

import (
	"strconv"
	"strings"
)

type ConstKind int

const (
	ConstInt ConstKind = iota
	ConstLong
	ConstBool
)

// ConstValue is a folded primitive constant. For int/long the value lives in Int;
// for boolean in Bool. A nil *ConstValue means "not a constant expression".
type ConstValue struct {
	Kind ConstKind
	Int  int64
	Bool bool
}

func wrap(kind ConstKind, v int64) int64 {
	if kind == ConstLong {
		return v
	}
	return int64(int32(v))
}

// parseIntLiteral parses a Java integer literal magnitude into its 64-bit pattern.
func parseIntLiteral(text string) (uint64, bool) {
	t := strings.ReplaceAll(text, "_", "")
	// Legacy octal: 0 followed by octal digits.
	if len(t) >= 2 && t[0] == '0' && isOctalDigits(t[1:]) {
		v, err := strconv.ParseUint(t[1:], 8, 64)
		return v, err == nil
	}
	if len(t) >= 2 && (t[1] == 'x' || t[1] == 'X') && t[0] == '0' {
		v, err := strconv.ParseUint(t[2:], 16, 64)
		return v, err == nil
	}
	if len(t) >= 2 && (t[1] == 'b' || t[1] == 'B') && t[0] == '0' {
		v, err := strconv.ParseUint(t[2:], 2, 64)
		return v, err == nil
	}
	v, err := strconv.ParseUint(t, 10, 64)
	return v, err == nil
}

func isOctalDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '7' {
			return false
		}
	}
	return len(s) > 0
}

func numKind(v *ConstValue) (ConstValue, bool) {
	if v != nil && v.Kind != ConstBool {
		return *v, true
	}
	return ConstValue{}, false
}

func foldPrefix(node *Node) *ConstValue {
	d := node.AsPrefixUnaryExpression()
	operand := FoldConstant(d.Operand)
	if operand == nil {
		return nil
	}
	switch d.Operator {
	case PlusToken:
		if n, ok := numKind(operand); ok {
			return &n
		}
		return nil
	case MinusToken:
		if n, ok := numKind(operand); ok {
			return &ConstValue{Kind: n.Kind, Int: wrap(n.Kind, -n.Int)}
		}
		return nil
	case TildeToken:
		if n, ok := numKind(operand); ok {
			return &ConstValue{Kind: n.Kind, Int: wrap(n.Kind, -n.Int-1)}
		}
		return nil
	case ExclamationToken:
		if operand.Kind == ConstBool {
			return &ConstValue{Kind: ConstBool, Bool: !operand.Bool}
		}
		return nil
	default:
		return nil
	}
}

func foldCompare(op SyntaxKind, a, b int64) (bool, bool) {
	switch op {
	case LessThanToken:
		return a < b, true
	case LessThanEqualsToken:
		return a <= b, true
	case GreaterThanToken:
		return a > b, true
	case GreaterThanEqualsToken:
		return a >= b, true
	case EqualsEqualsToken:
		return a == b, true
	case ExclamationEqualsToken:
		return a != b, true
	default:
		return false, false
	}
}

func foldBinary(node *Node) *ConstValue {
	d := node.AsBinaryExpression()
	left := FoldConstant(d.Left)
	right := FoldConstant(d.Right)
	if left == nil || right == nil {
		return nil
	}
	op := d.OperatorToken

	if left.Kind == ConstBool && right.Kind == ConstBool {
		switch op {
		case AmpersandAmpersandToken, AmpersandToken:
			return &ConstValue{Kind: ConstBool, Bool: left.Bool && right.Bool}
		case BarBarToken, BarToken:
			return &ConstValue{Kind: ConstBool, Bool: left.Bool || right.Bool}
		case CaretToken, ExclamationEqualsToken:
			return &ConstValue{Kind: ConstBool, Bool: left.Bool != right.Bool}
		case EqualsEqualsToken:
			return &ConstValue{Kind: ConstBool, Bool: left.Bool == right.Bool}
		default:
			return nil
		}
	}

	a, aok := numKind(left)
	b, bok := numKind(right)
	if !aok || !bok {
		return nil
	}

	if result, ok := foldCompare(op, a.Int, b.Int); ok {
		return &ConstValue{Kind: ConstBool, Bool: result}
	}

	kind := ConstInt
	if a.Kind == ConstLong || b.Kind == ConstLong {
		kind = ConstLong
	}
	switch op {
	case PlusToken:
		return &ConstValue{Kind: kind, Int: wrap(kind, a.Int+b.Int)}
	case MinusToken:
		return &ConstValue{Kind: kind, Int: wrap(kind, a.Int-b.Int)}
	case AsteriskToken:
		return &ConstValue{Kind: kind, Int: wrap(kind, a.Int*b.Int)}
	case SlashToken:
		if b.Int == 0 {
			return nil
		}
		return &ConstValue{Kind: kind, Int: wrap(kind, a.Int/b.Int)}
	case PercentToken:
		if b.Int == 0 {
			return nil
		}
		return &ConstValue{Kind: kind, Int: wrap(kind, a.Int%b.Int)}
	case AmpersandToken:
		return &ConstValue{Kind: kind, Int: wrap(kind, a.Int&b.Int)}
	case BarToken:
		return &ConstValue{Kind: kind, Int: wrap(kind, a.Int|b.Int)}
	case CaretToken:
		return &ConstValue{Kind: kind, Int: wrap(kind, a.Int^b.Int)}
	case LessThanLessThanToken, GreaterThanGreaterThanToken, GreaterThanGreaterThanGreaterThanToken:
		// A shift's result type and the distance mask come from the (promoted) left
		// operand only; the right operand never widens the result (JLS 15.19).
		sk := a.Kind
		var sb int64 = 32
		if sk == ConstLong {
			sb = 64
		}
		dist := uint64(b.Int & (sb - 1))
		switch op {
		case LessThanLessThanToken:
			return &ConstValue{Kind: sk, Int: wrap(sk, a.Int<<dist)}
		case GreaterThanGreaterThanToken:
			return &ConstValue{Kind: sk, Int: wrap(sk, a.Int>>dist)}
		default: // >>> logical: zero-fill from the unsigned operand
			if sk == ConstLong {
				return &ConstValue{Kind: sk, Int: wrap(sk, int64(uint64(a.Int)>>dist))}
			}
			return &ConstValue{Kind: sk, Int: wrap(sk, int64(uint32(a.Int)>>dist))}
		}
	default:
		return nil
	}
}

// FoldConstant evaluates a primitive constant expression, or returns nil if it
// is not constant.
func FoldConstant(node *Node) *ConstValue {
	switch node.Kind {
	case ParenthesizedExpression:
		return FoldConstant(node.AsParenthesizedExpression().Expression)
	case NumericLiteral:
		text := node.AsLiteralExpression().Value
		t := strings.ReplaceAll(text, "_", "")
		isHexOrBin := len(t) >= 2 && t[0] == '0' && (t[1] == 'x' || t[1] == 'X' || t[1] == 'b' || t[1] == 'B')
		// Decimal float/double, or hex floating-point: not an integer constant.
		// (In hex/binary literals a-f are digits, so only guard them on a 'p' exponent.)
		if isHexOrBin {
			if strings.ContainsAny(t, "pP") {
				return nil
			}
		} else if strings.ContainsAny(t, ".eEfFdD") {
			return nil
		}
		isLong := len(text) > 0 && (text[len(text)-1] == 'l' || text[len(text)-1] == 'L')
		lit := text
		if isLong {
			lit = text[:len(text)-1]
		}
		parsed, ok := parseIntLiteral(lit)
		if !ok {
			return nil
		}
		if isLong {
			return &ConstValue{Kind: ConstLong, Int: int64(parsed)}
		}
		return &ConstValue{Kind: ConstInt, Int: int64(int32(uint32(parsed)))}
	case TrueKeyword:
		return &ConstValue{Kind: ConstBool, Bool: true}
	case FalseKeyword:
		return &ConstValue{Kind: ConstBool, Bool: false}
	case PrefixUnaryExpression:
		return foldPrefix(node)
	case BinaryExpression:
		return foldBinary(node)
	default:
		return nil
	}
}
