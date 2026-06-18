package compiler

import "testing"

// Port of the "exotic" expression cases in src/compiler/parser.test.ts:
// creation, class literals, lambdas, method references, switch expressions,
// records, sealed types, instanceof/switch patterns and guards.

// valueExpr parses text in value position (a local variable initializer).
func valueExpr(t *testing.T, text string) *Node {
	t.Helper()
	stmt := firstStatement(t, "var v = "+text+";")
	return stmt.AsLocalVariableDeclarationStatement().Declarators.Nodes[0].AsVariableDeclarator().Initializer
}

func TestCreation(t *testing.T) {
	if parseExpr(t, "new Foo(1, 2)").Kind != ObjectCreationExpression {
		t.Error("new Foo(1, 2) should be ObjectCreationExpression")
	}
	if parseExpr(t, "new int[3][]").Kind != ArrayCreationExpression {
		t.Error("new int[3][] should be ArrayCreationExpression")
	}
	arr := parseExpr(t, "new int[]{1, 2, 3}")
	if arr.AsArrayCreationExpression().Initializer == nil {
		t.Error("array creation should have an initializer")
	}
	anon := parseExpr(t, "new Runnable() { public void run() {} }")
	if anon.AsObjectCreationExpression().ClassBody == nil {
		t.Error("anonymous class should have a class body")
	}
}

func TestClassLiterals(t *testing.T) {
	if parseExpr(t, "String.class").Kind != ClassLiteralExpression {
		t.Error("String.class should be ClassLiteralExpression")
	}
	if parseExpr(t, "int.class").Kind != ClassLiteralExpression {
		t.Error("int.class should be ClassLiteralExpression")
	}
	if parseExpr(t, "this.<String>doIt()").Kind != CallExpression {
		t.Error("this.<String>doIt() should be a CallExpression")
	}
}

func TestLambdas(t *testing.T) {
	if parseExpr(t, "x -> x + 1").Kind != LambdaExpression {
		t.Error("x -> x + 1 should be LambdaExpression")
	}
	if parseExpr(t, "() -> 42").Kind != LambdaExpression {
		t.Error("() -> 42 should be LambdaExpression")
	}
	if parseExpr(t, "(a, b) -> a + b").AsLambdaExpression().Parameters.Len() != 2 {
		t.Error("(a, b) -> ... should have 2 parameters")
	}
	typed := parseExpr(t, "(int a, String b) -> { return a; }").AsLambdaExpression()
	if typed.Parameters.Len() != 2 || typed.Body.Kind != Block {
		t.Errorf("typed lambda params=%d body=%v", typed.Parameters.Len(), typed.Body.Kind)
	}
}

func TestParenNotLambda(t *testing.T) {
	if parseExpr(t, "(a + b) * c").Kind != BinaryExpression {
		t.Error("(a + b) * c should be a BinaryExpression, not a lambda")
	}
}

func TestMethodReferences(t *testing.T) {
	m := parseExpr(t, "Foo::bar")
	if m.Kind != MethodReferenceExpression || m.AsMethodReferenceExpression().IsConstructorRef {
		t.Error("Foo::bar should be a non-constructor method reference")
	}
	if !parseExpr(t, "ArrayList::new").AsMethodReferenceExpression().IsConstructorRef {
		t.Error("ArrayList::new should be a constructor reference")
	}
	if parseExpr(t, "this::handle").Kind != MethodReferenceExpression {
		t.Error("this::handle should be a method reference")
	}
	if parseExpr(t, "java.util.Objects::requireNonNull").Kind != MethodReferenceExpression {
		t.Error("qualified method reference")
	}
}

func TestDefaultStaticInterfaceMethods(t *testing.T) {
	expectNoErrors(t, "interface I { default int x() { return 1; } static int y() { return 2; } }")
}

func TestTypeUseAnnotations(t *testing.T) {
	expectNoErrors(t, "class C { java.util.List<@NonNull String> xs; }")
}

func TestLambdaAsFieldInitializer(t *testing.T) {
	expectNoErrors(t, "class C { Runnable r = (int a) -> { int b = a; }; }")
}

func TestTextBlockField(t *testing.T) {
	sf := expectNoErrors(t, "class C { String s = \"\"\"\n  hello\n  \"\"\"; }")
	field := sf.Statements.Nodes[0].AsClassDeclaration().Members.Nodes[0].AsFieldDeclaration()
	if field.Declarators.Nodes[0].AsVariableDeclarator().Initializer.Kind != TextBlockLiteral {
		t.Error("initializer should be a TextBlockLiteral")
	}
}

func TestSwitchExpressionArrow(t *testing.T) {
	e := valueExpr(t, "switch (day) { case MON, TUE -> 1; default -> 0; }")
	if e.Kind != SwitchExpression {
		t.Fatalf("kind = %v", e.Kind)
	}
	clauses := e.AsSwitchExpression().Clauses
	if clauses.Len() != 2 {
		t.Fatalf("clauses = %d, want 2", clauses.Len())
	}
	c0 := clauses.Nodes[0].AsSwitchClause()
	if !c0.IsArrow || c0.Labels.Len() != 2 {
		t.Errorf("clause 0 arrow=%v labels=%d", c0.IsArrow, c0.Labels.Len())
	}
	if !clauses.Nodes[1].AsSwitchClause().IsDefault {
		t.Error("clause 1 should be default")
	}
}

func TestSwitchExprBlockYield(t *testing.T) {
	expectNoErrors(t, "class C { int m(int x) { return switch (x) { case 1 -> { yield 10; } default -> 0; }; } }")
}

func TestArrowSwitchStatement(t *testing.T) {
	sw := firstStatement(t, "switch (x) { case 1 -> a(); case 2 -> { b(); } default -> throw new E(); }")
	clauses := sw.AsSwitchStatement().Clauses
	if clauses.Len() != 3 {
		t.Fatalf("clauses = %d, want 3", clauses.Len())
	}
	for i, c := range clauses.Nodes {
		if !c.AsSwitchClause().IsArrow {
			t.Errorf("clause %d should be arrow", i)
		}
	}
}

func TestClassicColonSwitch(t *testing.T) {
	sw := firstStatement(t, "switch (x) { case 1: a(); break; default: }")
	if sw.AsSwitchStatement().Clauses.Nodes[0].AsSwitchClause().IsArrow {
		t.Error("colon clause should not be arrow")
	}
}

func TestYieldVsIdentifier(t *testing.T) {
	if firstStatement(t, "yield 42;").Kind != YieldStatement {
		t.Error("yield 42; should be a YieldStatement")
	}
	if firstStatement(t, "yield (a + b);").Kind != YieldStatement {
		t.Error("yield (a + b); should be a YieldStatement")
	}
	if firstStatement(t, "yield () -> x;").Kind != YieldStatement {
		t.Error("yield () -> x; should be a YieldStatement")
	}
	if parseExpr(t, "this.yield()").Kind != CallExpression {
		t.Error("this.yield() should be a CallExpression")
	}
}

func TestRecordCompactCtor(t *testing.T) {
	sf := expectNoErrors(t, "record Point(int x, int y) implements Comparable<Point> { Point { if (x < 0) throw new E(); } }")
	rec := sf.Statements.Nodes[0]
	if rec.Kind != RecordDeclaration {
		t.Fatalf("kind = %v", rec.Kind)
	}
	rd := rec.AsRecordDeclaration()
	if rd.RecordComponents.Len() != 2 || rd.ImplementsTypes.Len() != 1 {
		t.Errorf("components=%d implements=%d", rd.RecordComponents.Len(), rd.ImplementsTypes.Len())
	}
	if rd.Members.Nodes[0].Kind != CompactConstructorDeclaration {
		t.Errorf("member 0 = %v, want CompactConstructorDeclaration", rd.Members.Nodes[0].Kind)
	}
}

func TestGenericRecord(t *testing.T) {
	rd := expectNoErrors(t, "record Box<T>(T value) {}").Statements.Nodes[0].AsRecordDeclaration()
	if rd.TypeParameters.Len() != 1 || rd.RecordComponents.Len() != 1 {
		t.Errorf("typeParams=%d components=%d", rd.TypeParameters.Len(), rd.RecordComponents.Len())
	}
}

func TestSealedPermits(t *testing.T) {
	cls := expectNoErrors(t, "public sealed class Shape permits Circle, Square {}").Statements.Nodes[0].AsClassDeclaration()
	if cls.PermitsTypes.Len() != 2 {
		t.Errorf("permits = %d, want 2", cls.PermitsTypes.Len())
	}
}

func TestNonSealed(t *testing.T) {
	expectNoErrors(t, "non-sealed class Sub extends Shape {}")
}

func TestSealedInterface(t *testing.T) {
	itf := expectNoErrors(t, "sealed interface I permits A, B {}").Statements.Nodes[0].AsInterfaceDeclaration()
	if itf.PermitsTypes.Len() != 2 {
		t.Errorf("permits = %d, want 2", itf.PermitsTypes.Len())
	}
}

func TestInstanceofTypePattern(t *testing.T) {
	e := parseExpr(t, "o instanceof String s")
	if e.Kind != InstanceofExpression {
		t.Fatalf("kind = %v", e.Kind)
	}
	if e.AsInstanceofExpression().Name.AsIdentifier().Text != "s" {
		t.Error("instanceof binding name should be 's'")
	}
	if parseExpr(t, "o instanceof String").AsInstanceofExpression().Name != nil {
		t.Error("plain instanceof should have no binding name")
	}
}

func TestRecordAsIdentifier(t *testing.T) {
	expectNoErrors(t, "class C { int record = 1; }")
}

func TestSwitchTypePatternsGuards(t *testing.T) {
	expectNoErrors(t, "class C { String f(Object o) {\n"+
		"  return switch (o) {\n"+
		"    case Integer i when i > 0 -> \"pos\";\n"+
		"    case String s -> s;\n"+
		"    case null -> \"null\";\n"+
		"    default -> \"other\";\n"+
		"  };\n"+
		"} }")
}

func TestRecordDeconstruction(t *testing.T) {
	sw := firstStatement(t, "switch (shape) { case Rect(Point(var x, var y), int w) -> use(x); case Pair(_, var b) -> use(b); default -> {} }")
	first := sw.AsSwitchStatement().Clauses.Nodes[0].AsSwitchClause()
	if first.Labels.Nodes[0].Kind != RecordPattern {
		t.Fatalf("label 0 = %v, want RecordPattern", first.Labels.Nodes[0].Kind)
	}
	rec := first.Labels.Nodes[0].AsRecordPattern()
	if rec.Patterns.Nodes[0].Kind != RecordPattern {
		t.Error("first component should be a nested RecordPattern")
	}
}

func TestGuardExpr(t *testing.T) {
	sw := firstStatement(t, "switch (o) { case Integer i when i > 0 -> a(); default -> b(); }")
	c0 := sw.AsSwitchStatement().Clauses.Nodes[0].AsSwitchClause()
	if c0.Guard.Kind != BinaryExpression {
		t.Error("guard should be a BinaryExpression")
	}
	if c0.Labels.Nodes[0].Kind != TypePattern {
		t.Error("label should be a TypePattern")
	}
}

func TestCaseNullDefault(t *testing.T) {
	sw := firstStatement(t, "switch (o) { case null, default -> x(); }")
	if sw.AsSwitchStatement().Clauses.Nodes[0].AsSwitchClause().Labels.Len() != 2 {
		t.Error("case null, default should have 2 labels")
	}
}

func TestRepeatedUnnamed(t *testing.T) {
	expectNoErrors(t, "class C { void m() { var _ = a(); var _ = b(); } }")
}

func TestPrecedenceLadder(t *testing.T) {
	e := parseExpr(t, "a || b && c | d ^ e & f == g < h << i + j * k")
	if e.AsBinaryExpression().OperatorToken != BarBarToken {
		t.Errorf("top op = %v, want ||", e.AsBinaryExpression().OperatorToken)
	}
}

func TestExplicitGenericInvocation(t *testing.T) {
	if parseExpr(t, "Collections.<String>emptyList()").Kind != CallExpression {
		t.Error("explicit generic invocation should be a CallExpression")
	}
}

func TestCastGenericType(t *testing.T) {
	if parseExpr(t, "(java.util.List<String>) x").Kind != CastExpression {
		t.Error("cast of a generic type should be a CastExpression")
	}
}

func TestCurriedLambdas(t *testing.T) {
	outer := parseExpr(t, "a -> b -> a + b")
	if outer.Kind != LambdaExpression || outer.AsLambdaExpression().Body.Kind != LambdaExpression {
		t.Error("curried lambda body should be a LambdaExpression")
	}
}

func TestAnnotationWithArgs(t *testing.T) {
	expectNoErrors(t, "@SuppressWarnings({\"unchecked\", \"rawtypes\"}) class C {}")
	expectNoErrors(t, "@Column(name = \"id\", nullable = false) class C { int id; }")
}

func TestMissingSemicolonRecovery(t *testing.T) {
	sf := parse("class C { int x = 1 int y = 2; }").AsSourceFile()
	if len(sf.ParseDiagnostics) < 1 {
		t.Error("expected at least one diagnostic")
	}
	if sf.Statements.Nodes[0].AsClassDeclaration().Members.Len() < 2 {
		t.Error("both fields should still be recovered")
	}
}

func TestChainedArrayCall(t *testing.T) {
	if parseExpr(t, "matrix[i][j].toString().length()").Kind != CallExpression {
		t.Error("chained array and call access should end in a CallExpression")
	}
}

func TestNestedConditionalInArrayInit(t *testing.T) {
	expectNoErrors(t, "class C { int[] a = { x > 0 ? 1 : 2, 3 }; }")
}

func TestDeeplyNestedBlocks(t *testing.T) {
	expectNoErrors(t, "class C { void m() { { { { int x = 1; } } } } }")
}

func TestArrayClassLiteral(t *testing.T) {
	if parseExpr(t, "String[].class").Kind != ClassLiteralExpression {
		t.Error("String[].class should be a ClassLiteralExpression")
	}
	if parseExpr(t, "Map.Entry[].class").Kind != ClassLiteralExpression {
		t.Error("Map.Entry[].class should be a ClassLiteralExpression")
	}
	if parseExpr(t, "int[][].class").Kind != ClassLiteralExpression {
		t.Error("int[][].class should be a ClassLiteralExpression")
	}
	if parseExpr(t, "a[0]").Kind != ElementAccessExpression {
		t.Error("a[0] should be an ElementAccessExpression")
	}
}
