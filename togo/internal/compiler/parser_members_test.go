package compiler

import "testing"

// --- harness (mirrors the Node build's methodBody / firstStatement / expr) ---

func classMembers(t *testing.T, text string) []*Node {
	t.Helper()
	return expectNoErrors(t, text).Statements.Nodes[0].AsClassDeclaration().Members.Nodes
}

func methodBody(t *testing.T, stmts string) []*Node {
	t.Helper()
	d := expectNoErrors(t, "class C { void m() { "+stmts+" } }")
	method := d.Statements.Nodes[0].AsClassDeclaration().Members.Nodes[0]
	return method.AsMethodDeclaration().Body.AsBlock().Statements.Nodes
}

func firstStatement(t *testing.T, stmts string) *Node {
	t.Helper()
	return methodBody(t, stmts)[0]
}

func exprStmt(t *testing.T, text string) *Node {
	t.Helper()
	return firstStatement(t, text+";").AsExpressionStatement().Expression
}

// --- members -----------------------------------------------------------------

func TestFieldDeclarations(t *testing.T) {
	members := classMembers(t, "class C { private int x; int a, b = 1, c[]; }")
	if len(members) != 2 {
		t.Fatalf("members = %d, want 2", len(members))
	}
	if members[0].Kind != FieldDeclaration || members[0].AsFieldDeclaration().Declarators.Len() != 1 {
		t.Errorf("field 0 wrong")
	}
	f1 := members[1].AsFieldDeclaration()
	if f1.Declarators.Len() != 3 {
		t.Fatalf("field 1 declarators = %d, want 3", f1.Declarators.Len())
	}
	if f1.Declarators.Nodes[2].AsVariableDeclarator().ArrayRankAfterName != 1 {
		t.Error("c[] should have array rank 1")
	}
}

func TestMethodWithParametersAndThrows(t *testing.T) {
	m := classMembers(t, "class C { public void m(int a, String b) throws java.io.IOException {} }")[0]
	if m.Kind != MethodDeclaration {
		t.Fatalf("kind = %v", m.Kind)
	}
	md := m.AsMethodDeclaration()
	if md.Parameters.Len() != 2 || md.Throws.Len() != 1 || md.Body == nil {
		t.Errorf("params=%d throws=%d body=%v", md.Parameters.Len(), md.Throws.Len(), md.Body)
	}
}

func TestGenericMethod(t *testing.T) {
	md := classMembers(t, "class C { <T> T id(T x) { return x; } }")[0].AsMethodDeclaration()
	if md.TypeParameters.Len() != 1 {
		t.Errorf("type params = %d, want 1", md.TypeParameters.Len())
	}
	if md.ReturnType.AsTypeReference().TypeName.Kind != Identifier {
		t.Error("return type name should be an identifier")
	}
}

func TestVarargsParameter(t *testing.T) {
	md := classMembers(t, "class C { void f(int... xs) {} }")[0].AsMethodDeclaration()
	if !md.Parameters.Nodes[0].AsParameter().IsVarArgs {
		t.Error("parameter should be varargs")
	}
}

func TestAbstractMethodNoBody(t *testing.T) {
	d := expectNoErrors(t, "interface I { int compute(int x); }")
	m := d.Statements.Nodes[0].AsInterfaceDeclaration().Members.Nodes[0].AsMethodDeclaration()
	if m.Body != nil {
		t.Error("interface method should have no body")
	}
}

func TestConstructors(t *testing.T) {
	if classMembers(t, "class C { C(int x) {} }")[0].Kind != ConstructorDeclaration {
		t.Error("expected a constructor")
	}
	ctor := classMembers(t, "class C { <T> C(T x) {} }")[0]
	if ctor.Kind != ConstructorDeclaration || ctor.AsConstructorDeclaration().TypeParameters.Len() != 1 {
		t.Error("expected a generic constructor with 1 type parameter")
	}
}

func TestInitializerBlocks(t *testing.T) {
	members := classMembers(t, "class C { static {} {} }")
	if !members[0].AsInitializerBlock().IsStatic {
		t.Error("first initializer should be static")
	}
	if members[1].AsInitializerBlock().IsStatic {
		t.Error("second initializer should be instance")
	}
}

func TestNestedTypeMembers(t *testing.T) {
	members := classMembers(t, "class C { class Inner {} static interface N {} }")
	if members[0].Kind != ClassDeclaration || members[1].Kind != InterfaceDeclaration {
		t.Errorf("nested kinds = %v, %v", members[0].Kind, members[1].Kind)
	}
}

func TestEnumWithBody(t *testing.T) {
	e := expectNoErrors(t, "enum E { A, B, C; int code; void m() {} }").Statements.Nodes[0].AsEnumDeclaration()
	if e.EnumConstants.Len() != 3 || e.Members.Len() != 2 {
		t.Errorf("constants=%d members=%d", e.EnumConstants.Len(), e.Members.Len())
	}
}

func TestEnumConstantWithArgsAndBody(t *testing.T) {
	e := expectNoErrors(t, "enum E { A(1) { void m() {} }, B(2); E(int x) {} }").Statements.Nodes[0].AsEnumDeclaration()
	if e.EnumConstants.Len() != 2 {
		t.Fatalf("constants = %d, want 2", e.EnumConstants.Len())
	}
	if e.EnumConstants.Nodes[0].AsEnumConstantDeclaration().ClassBody == nil {
		t.Error("constant A should have a class body")
	}
	if e.Members.Len() != 1 {
		t.Errorf("members = %d, want 1 (the constructor)", e.Members.Len())
	}
}

func TestAnnotationElementDefault(t *testing.T) {
	expectNoErrors(t, "@interface Config { int timeout() default 30; String name(); }")
}

func TestTrailingCommaEnumConstants(t *testing.T) {
	e := expectNoErrors(t, "enum E { A, B, }").Statements.Nodes[0].AsEnumDeclaration()
	if e.EnumConstants.Len() != 2 {
		t.Errorf("constants = %d, want 2", e.EnumConstants.Len())
	}
}

func TestForEachChildMethod(t *testing.T) {
	m := classMembers(t, "class C { int add(int a, int b) { return a; } }")[0]
	var kinds []SyntaxKind
	m.ForEachChild(func(n *Node) bool {
		kinds = append(kinds, n.Kind)
		return false
	})
	for _, want := range []SyntaxKind{Parameter, Block} {
		if !contains(kinds, want) {
			t.Errorf("method children %v missing %v", kinds, want)
		}
	}
}

// --- statements --------------------------------------------------------------

func TestLocalVariableDeclarations(t *testing.T) {
	stmts := methodBody(t, `int x = 1, y; final String s = "a";`)
	if len(stmts) != 2 || stmts[0].Kind != LocalVariableDeclarationStatement {
		t.Errorf("stmts = %d, kind0 = %v", len(stmts), stmts[0].Kind)
	}
}

func TestControlFlowStatements(t *testing.T) {
	cases := map[string]SyntaxKind{
		"if (a) b(); else c();":            IfStatement,
		"while (a) b();":                   WhileStatement,
		"do b(); while (a);":               DoStatement,
		"for (int i = 0; i < n; i++) b();": ForStatement,
		"for (String s : list) b();":       ForEachStatement,
		"return x;":                        ReturnStatement,
		"throw e;":                         ThrowStatement,
		"synchronized (lock) {}":           SynchronizedStatement,
		`assert x > 0 : "bad";`:            AssertStatement,
	}
	for src, want := range cases {
		if got := firstStatement(t, src).Kind; got != want {
			t.Errorf("%q = %v, want %v", src, got, want)
		}
	}
}

func TestLabeledBreak(t *testing.T) {
	labeled := firstStatement(t, "outer: for (;;) break outer;")
	if labeled.Kind != LabeledStatement || labeled.AsLabeledStatement().Label.AsIdentifier().Text != "outer" {
		t.Errorf("labeled = %v", labeled.Kind)
	}
}

func TestTryStatement(t *testing.T) {
	tn := firstStatement(t, "try (Reader r = open(); Reader q = open()) { use(); } catch (IOException | RuntimeException e) { log(e); } finally { close(); }")
	if tn.Kind != TryStatement {
		t.Fatalf("kind = %v", tn.Kind)
	}
	tr := tn.AsTryStatement()
	if tr.Resources.Len() != 2 || tr.CatchClauses.Len() != 1 || tr.FinallyBlock == nil {
		t.Errorf("resources=%d catches=%d finally=%v", tr.Resources.Len(), tr.CatchClauses.Len(), tr.FinallyBlock)
	}
	if tr.CatchClauses.Nodes[0].AsCatchClause().CatchTypes.Len() != 2 {
		t.Error("multi-catch should have 2 types")
	}
}

func TestSwitchStatement(t *testing.T) {
	sw := firstStatement(t, "switch (x) { case 1: a(); break; case 2: b(); default: c(); }")
	if sw.Kind != SwitchStatement {
		t.Fatalf("kind = %v", sw.Kind)
	}
	clauses := sw.AsSwitchStatement().Clauses
	if clauses.Len() != 3 || !clauses.Nodes[2].AsSwitchClause().IsDefault {
		t.Errorf("clauses = %d, last default = %v", clauses.Len(), clauses.Nodes[2].AsSwitchClause().IsDefault)
	}
}

func TestStringSwitch(t *testing.T) {
	expectNoErrors(t, `class C { void m(String s) { switch (s) { case "a": break; default: } } }`)
}

func TestLocalClassDeclaration(t *testing.T) {
	if firstStatement(t, "class Local {} ").Kind != ClassDeclaration {
		t.Error("expected a local class declaration")
	}
}

func TestNestedBlocks(t *testing.T) {
	if firstStatement(t, "{ int x = 1; { int y = 2; } }").Kind != Block {
		t.Error("expected a block")
	}
}

func TestFieldInitializersAreExpressions(t *testing.T) {
	d := expectNoErrors(t, "class C { int x = 1 + 2; int[] a = {1, 2, 3}; }")
	field := d.Statements.Nodes[0].AsClassDeclaration().Members.Nodes[0].AsFieldDeclaration()
	if field.Declarators.Nodes[0].AsVariableDeclarator().Initializer.Kind != BinaryExpression {
		t.Error("x initializer should be a binary expression")
	}
}

func TestVarLocalVariable(t *testing.T) {
	stmt := firstStatement(t, "var x = 10;")
	if stmt.Kind != LocalVariableDeclarationStatement || stmt.AsLocalVariableDeclarationStatement().Type.Kind != VarType {
		t.Errorf("var local = %v", stmt.Kind)
	}
}

func TestVarInForEach(t *testing.T) {
	fe := firstStatement(t, "for (var item : items) use(item);")
	if fe.Kind != ForEachStatement || fe.AsForEachStatement().Parameter.AsParameter().Type.Kind != VarType {
		t.Error("for-each var parameter type should be VarType")
	}
}

func TestVarInTryWithResources(t *testing.T) {
	expectNoErrors(t, "class C { void m() { try (var r = open()) {} } }")
}

func TestVarAsIdentifier(t *testing.T) {
	if exprStmt(t, "var.length").Kind != PropertyAccessExpression {
		t.Error("var.length should be a property access (var as identifier)")
	}
}
