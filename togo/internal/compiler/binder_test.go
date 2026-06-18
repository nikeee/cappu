package compiler

import "testing"

// Port of src/compiler/binder.test.ts.

func bindJava(text string) *Node {
	sf := ParseSourceFile("Test.java", text)
	BindSourceFile(sf)
	return sf
}

func TestTopLevelTypesInFileScope(t *testing.T) {
	sf := bindJava("class A {} interface B {}")
	if sf.Locals["A"].Flags != SymbolFlagsClass {
		t.Errorf("A flags = %d, want Class", sf.Locals["A"].Flags)
	}
	if sf.Locals["B"].Flags != SymbolFlagsInterface {
		t.Errorf("B flags = %d, want Interface", sf.Locals["B"].Flags)
	}
}

func TestClassMembersSymbolTable(t *testing.T) {
	sf := bindJava("class C { int x; void m() {} }")
	c := sf.Locals["C"]
	if c.Members["x"].Flags != SymbolFlagsField {
		t.Errorf("x flags = %d, want Field", c.Members["x"].Flags)
	}
	if c.Members["m"].Flags != SymbolFlagsMethod {
		t.Errorf("m flags = %d, want Method", c.Members["m"].Flags)
	}
}

func TestDuplicateFieldReported(t *testing.T) {
	sf := bindJava("class C { int x; int x; }")
	bd := sf.AsSourceFile().BindDiagnostics
	if len(bd) != 1 {
		t.Fatalf("bind diagnostics = %d, want 1", len(bd))
	}
	if !containsSubstr(bd[0].MessageText, "x") {
		t.Errorf("message %q should mention 'x'", bd[0].MessageText)
	}
}

func TestMethodOverloadsNoCollide(t *testing.T) {
	sf := bindJava("class C { void m() {} void m(int a) {} }")
	if len(sf.AsSourceFile().BindDiagnostics) != 0 {
		t.Errorf("unexpected diagnostics: %v", sf.AsSourceFile().BindDiagnostics)
	}
	m := sf.Locals["C"].Members["m"]
	if len(m.Declarations) != 2 {
		t.Errorf("m declarations = %d, want 2", len(m.Declarations))
	}
}

func TestParametersAndLocalsScoped(t *testing.T) {
	sf := bindJava("class C { void m(int a) { int b; } }")
	method := sf.Locals["C"].Members["m"].Declarations[0]
	if method.Locals["a"].Flags != SymbolFlagsParameter {
		t.Errorf("a flags = %d, want Parameter", method.Locals["a"].Flags)
	}
	body := method.AsMethodDeclaration().Body
	if body.Locals["b"].Flags != SymbolFlagsLocalVariable {
		t.Errorf("b flags = %d, want LocalVariable", body.Locals["b"].Flags)
	}
}

func TestTypeParametersBound(t *testing.T) {
	sf := bindJava("class C<T, U> {}")
	c := sf.Locals["C"]
	if c.Members["T"].Flags != SymbolFlagsTypeParameter || c.Members["U"].Flags != SymbolFlagsTypeParameter {
		t.Error("T and U should be TypeParameter symbols")
	}
}

func TestEnumConstantsBound(t *testing.T) {
	sf := bindJava("enum E { A, B, C }")
	e := sf.Locals["E"]
	if e.Members["A"].Flags != SymbolFlagsEnumConstant || e.Members["C"].Flags != SymbolFlagsEnumConstant {
		t.Error("A and C should be EnumConstant symbols")
	}
}

func TestNestedClassesAreMembers(t *testing.T) {
	sf := bindJava("class Outer { class Inner {} }")
	if sf.Locals["Outer"].Members["Inner"].Flags != SymbolFlagsClass {
		t.Error("Inner should be a Class member of Outer")
	}
}

func TestParentPointersSet(t *testing.T) {
	sf := bindJava("class C { void m() { int x = 1; } }")
	c := sf.AsSourceFile().Statements.Nodes[0]
	if c.Parent != sf {
		t.Error("class parent should be the source file")
	}
	m := c.AsClassDeclaration().Members.Nodes[0]
	if m.Parent != c {
		t.Error("method parent should be the class")
	}
	if m.AsMethodDeclaration().Name.Parent != m {
		t.Error("method name parent should be the method")
	}
	if m.AsMethodDeclaration().Body.Parent != m {
		t.Error("method body parent should be the method")
	}
}

func TestMultipleDeclaratorsEachSymbol(t *testing.T) {
	sf := bindJava("class C { int a, b, c; }")
	c := sf.Locals["C"]
	for _, name := range []string{"a", "b", "c"} {
		if c.Members[name] == nil {
			t.Errorf("member %q should be declared", name)
		}
	}
}

func TestNodeSymbolAttached(t *testing.T) {
	sf := bindJava("class C {}")
	c := sf.AsSourceFile().Statements.Nodes[0]
	if c.Symbol != sf.Locals["C"] {
		t.Error("class node symbol should match the file-scope symbol")
	}
}

func TestLocalClassScopedToBlock(t *testing.T) {
	sf := bindJava("class C { void m() { class Local {} } }")
	method := sf.Locals["C"].Members["m"].Declarations[0]
	if method.AsMethodDeclaration().Body.Locals["Local"].Flags != SymbolFlagsClass {
		t.Error("Local should be a Class scoped to the method body")
	}
}

func TestUnnamedVariablesNotDeclared(t *testing.T) {
	sf := bindJava("class C { void m() { var _ = a(); var _ = b(); } }")
	if len(sf.AsSourceFile().BindDiagnostics) != 0 {
		t.Errorf("unnamed variables should not collide: %v", sf.AsSourceFile().BindDiagnostics)
	}
}

func TestTypedLambdaParamsScoped(t *testing.T) {
	sf := bindJava("class C { Runnable r = (int a) -> { int b = a; }; }")
	if len(sf.AsSourceFile().BindDiagnostics) != 0 {
		t.Errorf("unexpected diagnostics: %v", sf.AsSourceFile().BindDiagnostics)
	}
	var lambda *Node
	var visit Visitor
	visit = func(n *Node) bool {
		if n.Kind == LambdaExpression {
			lambda = n
		}
		n.ForEachChild(visit)
		return false
	}
	sf.ForEachChild(visit)
	if lambda == nil || lambda.Locals["a"].Flags != SymbolFlagsParameter {
		t.Error("typed lambda parameter 'a' should be scoped to the lambda")
	}
}

func TestSymbolParentAndValueDeclaration(t *testing.T) {
	sf := bindJava("class C { int x; void m() {} }")
	c := sf.Locals["C"]
	x := c.Members["x"]
	if x.Parent != c {
		t.Error("member symbol parent should be the enclosing type")
	}
	if x.ValueDeclaration != x.Declarations[0] {
		t.Error("x valueDeclaration should be its first declaration")
	}
	if c.ValueDeclaration != c.Declarations[0] {
		t.Error("c valueDeclaration should be its first declaration")
	}
}

func containsSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
