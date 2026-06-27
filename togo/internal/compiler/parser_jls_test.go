package compiler

import "testing"

// Port of the "Remaining JLS-conformance constructs", generics/expression edge
// cases and pinned parser-diagnostic-code tests in src/compiler/parser.test.ts.

func codes(text string) []int {
	out := []int{}
	for _, d := range parse(text).AsSourceFile().ParseDiagnostics {
		out = append(out, int(d.Code))
	}
	return out
}

func hasCode(codes []int, want int) bool {
	for _, c := range codes {
		if c == want {
			return true
		}
	}
	return false
}

// --- remaining JLS-conformance constructs ------------------------------------

func TestAnnotationArgumentsAST(t *testing.T) {
	sf := expectNoErrors(t, "@Column(name = \"id\", nullable = false) class C {}")
	ann := sf.Statements.Nodes[0].AsClassDeclaration().Modifiers.Nodes[0]
	if ann.Kind != Annotation {
		t.Fatalf("modifier 0 = %v, want Annotation", ann.Kind)
	}
	if ann.AsAnnotation().Args.Len() != 2 {
		t.Fatalf("args = %d, want 2", ann.AsAnnotation().Args.Len())
	}
	if ann.AsAnnotation().Args.Nodes[0].AsAnnotationArgument().Name.AsIdentifier().Text != "name" {
		t.Error("first arg name should be 'name'")
	}
	sf2 := expectNoErrors(t, "@SuppressWarnings({\"a\", \"b\"}) class C {}")
	ann2 := sf2.Statements.Nodes[0].AsClassDeclaration().Modifiers.Nodes[0].AsAnnotation()
	if ann2.Args.Len() != 1 || ann2.Args.Nodes[0].AsAnnotationArgument().Value.Kind != ArrayInitializer {
		t.Error("single array-valued annotation arg should be an ArrayInitializer")
	}
	expectNoErrors(t, "@Wrap(@Inner(1)) class C {}")
}

func TestAnnotatedTypeParameter(t *testing.T) {
	sf := expectNoErrors(t, "class C<@NonNull T extends Object> {}")
	tp := sf.Statements.Nodes[0].AsClassDeclaration().TypeParameters.Nodes[0].AsTypeParameter()
	if tp.Annotations.Len() != 1 {
		t.Errorf("type parameter annotations = %d, want 1", tp.Annotations.Len())
	}
}

func TestIntersectionCast(t *testing.T) {
	e := parseExpr(t, "(Runnable & java.io.Serializable) x")
	if e.Kind != CastExpression {
		t.Fatalf("kind = %v", e.Kind)
	}
	if e.AsCastExpression().Bounds.Len() != 1 {
		t.Errorf("bounds = %d, want 1", e.AsCastExpression().Bounds.Len())
	}
}

func TestReceiverParameter(t *testing.T) {
	members := classMembers(t, "class C { void m(C this, int x) {} void n(Outer.C this) {} }")
	m := members[0].AsMethodDeclaration()
	if !m.Parameters.Nodes[0].AsParameter().IsReceiver || m.Parameters.Len() != 2 {
		t.Errorf("receiver=%v params=%d", m.Parameters.Nodes[0].AsParameter().IsReceiver, m.Parameters.Len())
	}
	if !members[1].AsMethodDeclaration().Parameters.Nodes[0].AsParameter().IsReceiver {
		t.Error("qualified receiver parameter")
	}
}

func TestAnnotationElementDefaultValue(t *testing.T) {
	sf := expectNoErrors(t, "@interface A { int count() default 1; String name() default \"x\"; }")
	el := sf.Statements.Nodes[0].AsAnnotationTypeDeclaration().Members.Nodes[0].AsMethodDeclaration()
	if el.DefaultValue == nil {
		t.Error("annotation element should have a default value")
	}
}

func TestQualifiedThisSuper(t *testing.T) {
	tn := parseExpr(t, "Outer.this")
	if tn.Kind != ThisExpression || tn.AsThisExpression().Qualifier == nil {
		t.Error("Outer.this should be a qualified ThisExpression")
	}
	if parseExpr(t, "Foo.super.m()").Kind != CallExpression {
		t.Error("Foo.super.m() should be a CallExpression")
	}
}

func TestForStatementExpressionInitList(t *testing.T) {
	f := firstStatement(t, "for (i = 0, j = n; i < j; i++, j--) step();").AsForStatement()
	if f.InitializerExpressions.Len() != 2 || f.Incrementors.Len() != 2 {
		t.Errorf("init=%d incr=%d", f.InitializerExpressions.Len(), f.Incrementors.Len())
	}
}

// --- generics edge cases -----------------------------------------------------

func TestDeepNestedGenericMixedArgs(t *testing.T) {
	typ, errs := extendsType("java.util.Map<String, java.util.List<java.util.Map<Integer, String>>>")
	if errs != 0 || typ.Kind != TypeReference {
		t.Errorf("errs=%d kind=%v", errs, typ.Kind)
	}
}

func TestFourLevelNestedGeneric(t *testing.T) {
	typ, errs := extendsType("A<B<C<D<E>>>>")
	if errs != 0 {
		t.Fatalf("errs = %d", errs)
	}
	depth := 0
	t2 := typ
	for t2.AsTypeReference().TypeArguments != nil && t2.AsTypeReference().TypeArguments.Len() > 0 {
		depth++
		t2 = t2.AsTypeReference().TypeArguments.Nodes[0]
	}
	if depth != 4 {
		t.Errorf("depth = %d, want 4", depth)
	}
}

func TestNestedWildcards(t *testing.T) {
	typ, errs := extendsType("A<? super java.util.List<? extends Number>>")
	if errs != 0 {
		t.Fatalf("errs = %d", errs)
	}
	outer := typ.AsTypeReference().TypeArguments.Nodes[0]
	if outer.Kind != WildcardType || !outer.AsWildcardType().HasSuper {
		t.Error("outer wildcard should be a `? super`")
	}
}

func TestMultipleBoundsGenericBound(t *testing.T) {
	sf := expectNoErrors(t, "class C<T extends Comparable<T> & java.io.Serializable & Cloneable> {}")
	if sf.Statements.Nodes[0].AsClassDeclaration().TypeParameters.Nodes[0].AsTypeParameter().Constraint.Len() != 3 {
		t.Error("type parameter should have 3 bounds")
	}
}

func TestRecursiveTypeBound(t *testing.T) {
	expectNoErrors(t, "class Node<T extends Comparable<T>> { T value; }")
}

func TestAnnotatedTypeParameters(t *testing.T) {
	sf := expectNoErrors(t, "class C<@Deprecated T, @Deprecated U extends Number> {}")
	if sf.Statements.Nodes[0].AsClassDeclaration().TypeParameters.Len() != 2 {
		t.Error("should have 2 type parameters")
	}
}

func TestRawAndParameterizedType(t *testing.T) {
	expectNoErrors(t, "class C { java.util.List raw; java.util.List<String> typed; }")
}

func TestWildcardArray(t *testing.T) {
	expectNoErrors(t, "class C { java.util.List<?>[] a; java.util.Map<String, ?>[] b; }")
}

func TestDiamondInObjectCreation(t *testing.T) {
	if parseExpr(t, "new java.util.ArrayList<>()").Kind != ObjectCreationExpression {
		t.Error("diamond object creation")
	}
}

func TestExplicitGenericInvocationTypeArgs(t *testing.T) {
	call := parseExpr(t, "this.<String>doIt()")
	if call.Kind != CallExpression || call.AsCallExpression().TypeArguments.Len() != 1 {
		t.Error("explicit generic invocation should capture 1 type argument")
	}
}

func TestExplicitMultiTypeArgCall(t *testing.T) {
	if parseExpr(t, "Helper.<String, Integer>make()").AsCallExpression().TypeArguments.Len() != 2 {
		t.Error("should capture 2 type arguments")
	}
}

func TestGenericVarargsParameter(t *testing.T) {
	members := classMembers(t, "class C { void m(java.util.List<String>... xs) {} }")
	if !members[0].AsMethodDeclaration().Parameters.Nodes[0].AsParameter().IsVarArgs {
		t.Error("generic varargs parameter")
	}
}

func TestCastParameterizedAndIntersection(t *testing.T) {
	if parseExpr(t, "(java.util.Map<String, Integer>) o").Kind != CastExpression {
		t.Error("cast to a parameterized type")
	}
	if parseExpr(t, "(Runnable & java.io.Serializable) o").AsCastExpression().Bounds.Len() != 1 {
		t.Error("intersection cast bounds")
	}
}

func TestGenericFieldDiamond(t *testing.T) {
	expectNoErrors(t, "class C { java.util.Map<String, java.util.List<Integer>> m = new java.util.HashMap<>(); }")
}

func TestCompoundShiftNotGenericClose(t *testing.T) {
	e := parseExpr(t, "a >>= b")
	if e.AsAssignmentExpression().OperatorToken != GreaterThanGreaterThanEqualsToken {
		t.Error("'>>=' should stay a compound shift assignment")
	}
}

func TestObjectCreationMultipleTypeArgs(t *testing.T) {
	e := parseExpr(t, "new java.util.HashMap<String, Integer>()")
	if e.Kind != ObjectCreationExpression {
		t.Fatalf("kind = %v", e.Kind)
	}
	if e.AsObjectCreationExpression().Type.AsTypeReference().TypeArguments.Len() != 2 {
		t.Error("creation type should have 2 type arguments")
	}
}

// --- expression / statement edge cases ---------------------------------------

func TestNestedTernaryAssociativity(t *testing.T) {
	e := parseExpr(t, "a ? b : c ? d : e")
	if e.AsConditionalExpression().WhenFalse.Kind != ConditionalExpression {
		t.Error("nested ternary should be right-associative")
	}
}

func TestLambdasInBothBranches(t *testing.T) {
	expectNoErrors(t, "class C { Runnable pick(boolean b) { return b ? () -> {} : () -> {}; } }")
}

func TestLabeledNestedLoops(t *testing.T) {
	expectNoErrors(t, "class C { void m() { outer: for (;;) { inner: for (;;) { if (true) break outer; else continue inner; } } } }")
}

func TestMultidimArrayCreation(t *testing.T) {
	if parseExpr(t, "new int[2][3]").Kind != ArrayCreationExpression {
		t.Error("new int[2][3]")
	}
	if parseExpr(t, "new int[][]{ {1, 2}, {3} }").Kind != ArrayCreationExpression {
		t.Error("new int[][]{...}")
	}
}

func TestAnonymousClassWithMembers(t *testing.T) {
	if parseExpr(t, "new Runnable() { public void run() { int x = 1; } }").AsObjectCreationExpression().ClassBody == nil {
		t.Error("anonymous class with members should have a class body")
	}
}

func TestChainedCallsIndexFieldAccess(t *testing.T) {
	if parseExpr(t, "a.b().c[0].d().e").Kind != PropertyAccessExpression {
		t.Error("chained calls/indexing/field access should end in a PropertyAccessExpression")
	}
}

func TestSwitchNullDefaultGuardedRecord(t *testing.T) {
	expectNoErrors(t, "class C { int m(Object o) { return switch (o) {"+
		" case null -> 0;"+
		" case Integer i when i > 0 -> 1;"+
		" case Point(int x, int y) -> x + y;"+
		" default -> -1; }; } }")
}

func TestEmptyTypeDeclarationsEveryKind(t *testing.T) {
	expectNoErrors(t, "class A {} interface B {} enum C {} @interface D {} record E() {}")
}

// --- pinned parser-diagnostic-code tests --------------------------------------

func TestErrorCode1001(t *testing.T) {
	if !hasCode(codes("class C {"), 1001) {
		t.Error("missing close brace should report 1001")
	}
}

func TestErrorCode1002(t *testing.T) {
	if !hasCode(codes("class { }"), 1002) {
		t.Error("missing type name should report 1002")
	}
}

func TestErrorCode1003(t *testing.T) {
	if !hasCode(codes("int 123;"), 1003) {
		t.Error("stray top-level tokens should report 1003")
	}
}

func TestErrorCode1005(t *testing.T) {
	if !hasCode(codes("class C { void m() { int x = ; } }"), 1005) {
		t.Error("assignment with no RHS should report 1005")
	}
}

func TestErrorCode1007(t *testing.T) {
	if !hasCode(codes("class C { 123 }"), 1007) {
		t.Error("bare literal class member should report 1007")
	}
}

func TestErrorCode1020(t *testing.T) {
	if !hasCode(codes("public 123"), 1020) {
		t.Error("modifiers not followed by a declaration should report 1020")
	}
}

func TestErrorCode1021(t *testing.T) {
	if !hasCode(codes("class C { void m( { } }"), 1021) {
		t.Error("malformed parameter list should report 1021")
	}
}

func TestArrayConstructorReferences(t *testing.T) {
	sf := parse("class T {\n" +
		"  Object[] m(java.util.stream.Stream<Object> s) {\n" +
		"    return s.toArray(Object[]::new);\n" +
		"  }\n" +
		"  Class<?> c() { return String[].class; }\n" +
		"  java.util.function.IntFunction<String[][]> g() { return String[][]::new; }\n" +
		"}").AsSourceFile()
	if len(sf.ParseDiagnostics) != 0 {
		t.Errorf("unexpected diagnostics: %v", sf.ParseDiagnostics)
	}
}

func TestLambdaIntersectionCastQualifiedNew(t *testing.T) {
	sf := parse("class T {\n" +
		"  class Inner {}\n" +
		"  Object a(java.util.function.Function<String, String> f) {\n" +
		"    return (java.util.function.Function<String, String> & java.io.Serializable) p -> null;\n" +
		"  }\n" +
		"  Object b(T t) { return t.new Inner(); }\n" +
		"  Object c(T t) { return t.new Inner() { }; }\n" +
		"  int d(Object x, int b) { return (int) -b; }\n" +
		"}").AsSourceFile()
	if len(sf.ParseDiagnostics) != 0 {
		t.Errorf("unexpected diagnostics: %v", sf.ParseDiagnostics)
	}
}
