package compiler

import (
	"strings"
	"testing"
)

// parseExpr parses text as an expression in statement position, through a real
// method body (matching the Node build's `expr` helper).
func parseExpr(t *testing.T, text string) *Node {
	t.Helper()
	return exprStmt(t, text)
}

func TestExpressionStatements(t *testing.T) {
	cases := map[string]SyntaxKind{
		"foo()":        CallExpression,
		"a = b":        AssignmentExpression,
		"i++":          PostfixUnaryExpression,
		"a.b.c":        PropertyAccessExpression,
		"a.b().c[0].d": PropertyAccessExpression,
	}
	for text, want := range cases {
		if got := parseExpr(t, text).Kind; got != want {
			t.Errorf("%q = %v, want %v", text, got, want)
		}
	}
}

func TestBinaryPrecedence(t *testing.T) {
	e := parseExpr(t, "a + b * c")
	if e.Kind != BinaryExpression || e.AsBinaryExpression().OperatorToken != PlusToken {
		t.Fatalf("top = %v op %v", e.Kind, e.AsBinaryExpression().OperatorToken)
	}
	if e.AsBinaryExpression().Right.AsBinaryExpression().OperatorToken != AsteriskToken {
		t.Error("right should be the multiplication")
	}
}

func TestLogicalPrecedence(t *testing.T) {
	e := parseExpr(t, "a || b && c")
	if e.AsBinaryExpression().OperatorToken != BarBarToken {
		t.Errorf("top op = %v, want ||", e.AsBinaryExpression().OperatorToken)
	}
	if e.AsBinaryExpression().Right.AsBinaryExpression().OperatorToken != AmpersandAmpersandToken {
		t.Error("&& should bind tighter than ||")
	}
}

func TestShiftVsRelational(t *testing.T) {
	e := parseExpr(t, "a << b < c")
	if e.AsBinaryExpression().OperatorToken != LessThanToken {
		t.Errorf("top op = %v, want <", e.AsBinaryExpression().OperatorToken)
	}
	if e.AsBinaryExpression().Left.AsBinaryExpression().OperatorToken != LessThanLessThanToken {
		t.Error("<< should bind tighter than <")
	}
}

func TestTernaryAndAssignRightAssociative(t *testing.T) {
	ternary := parseExpr(t, "a ? b : c ? d : e")
	if ternary.Kind != ConditionalExpression {
		t.Fatalf("kind = %v", ternary.Kind)
	}
	if ternary.AsConditionalExpression().WhenFalse.Kind != ConditionalExpression {
		t.Error("ternary should be right-associative")
	}
	assign := parseExpr(t, "a = b = c")
	if assign.AsAssignmentExpression().Right.Kind != AssignmentExpression {
		t.Error("assignment should be right-associative")
	}
}

func TestCompoundShiftAssignExpr(t *testing.T) {
	e := parseExpr(t, "a >>= b")
	if e.Kind != AssignmentExpression || e.AsAssignmentExpression().OperatorToken != GreaterThanGreaterThanEqualsToken {
		t.Errorf("got kind %v op %v", e.Kind, e.AsAssignmentExpression().OperatorToken)
	}
}

func TestInstanceofExpr(t *testing.T) {
	e := parseExpr(t, "o instanceof String")
	if e.Kind != InstanceofExpression {
		t.Fatalf("kind = %v", e.Kind)
	}
	if e.AsInstanceofExpression().Type.Kind != TypeReference {
		t.Error("instanceof type should be a TypeReference")
	}
}

func TestCastVsParenthesized(t *testing.T) {
	cases := map[string]SyntaxKind{
		"(int) x":   CastExpression,
		"(Foo) bar": CastExpression,
		"(a)":       ParenthesizedExpression,
		"(a) - b":   BinaryExpression, // not a cast
	}
	for text, want := range cases {
		if got := parseExpr(t, text).Kind; got != want {
			t.Errorf("%q = %v, want %v", text, got, want)
		}
	}
}

// Port of src/compiler/parser.test.ts "primitive array constructor references
// and primitive class literals parse cleanly".
func TestPrimitiveArrayConstructorRefAndClassLiteralOperand(t *testing.T) {
	expectNoErrors(t, strings.Join([]string{
		"class T {",
		"  int[][] m(java.util.stream.Stream<int[]> s) { return s.toArray(int[][]::new); }",
		"  java.util.function.IntFunction<int[]> g() { return int[]::new; }",
		"  Class<?> c() { return (Class<?>) int.class; }",
		"  Class<?> v() { return (Class<?>) void.class; }",
		"}",
	}, "\n"))
}

// Port of src/compiler/parser.test.ts "a cast in a case label is a constant,
// not a record pattern".
func TestCastInCaseLabel(t *testing.T) {
	expectNoErrors(t, strings.Join([]string{
		"class T {",
		"  void m(char c) {",
		"    switch (c) {",
		"      case (char) 0x00A0:",
		"        break;",
		"      case (char) 'a':",
		"        break;",
		"      default:",
		"    }",
		"  }",
		"}",
	}, "\n"))
}
