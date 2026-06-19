package compiler

import "testing"

// Port of src/compiler/checker.advanced.test.ts.

func diagnose(text string) []int {
	program := NewProgram()
	LoadJdkStub(program)
	program.SetOpenDocument("file:///T.java", text, 1)
	checker := NewChecker(program)
	var out []int
	for _, d := range checker.GetSemanticDiagnostics(program.GetSourceFile("file:///T.java")) {
		out = append(out, d.Code)
	}
	return out
}

func containsCode(codes []int, code int) bool {
	for _, c := range codes {
		if c == code {
			return true
		}
	}
	return false
}

const (
	codeOverride      = 1301
	codeNotExhaustive = 1302
	codeNoMember      = 1303
)

func TestOverrideAccepted(t *testing.T) {
	cases := []string{
		"class Base { void run() {} }\nclass Sub extends Base { @Override void run() {} }",
		"interface I { int d(); }\nclass C implements I { @Override public int d() { return 1; } }",
		"class C { @Override public String toString() { return \"\"; } }",
		"class A { void f() {} }\nclass B extends A {}\nclass C extends B { @Override void f() {} }",
		"class Base {}\nclass Sub extends Base { void nope() {} }",
		"class Sub extends Unknown { @Override void whatever() {} }",
	}
	for _, code := range cases {
		if containsCode(diagnose(code), codeOverride) {
			t.Errorf("%q should NOT flag @Override", code)
		}
	}
}

func TestOverrideFlagged(t *testing.T) {
	code := "class Base {}\nclass Sub extends Base { @Override void nope() {} }"
	if !containsCode(diagnose(code), codeOverride) {
		t.Errorf("%q should flag @Override", code)
	}
}

func TestExhaustivenessFlagged(t *testing.T) {
	code := "enum E { A, B, C }\nclass C0 { int m(E e) { return switch (e) { case A -> 1; case B -> 2; }; } }"
	if !containsCode(diagnose(code), codeNotExhaustive) {
		t.Error("non-exhaustive enum switch should be flagged")
	}
}

func TestExhaustivenessAccepted(t *testing.T) {
	cases := []string{
		"enum E { A, B }\nclass C0 { int m(E e) { return switch (e) { case A -> 1; case B -> 2; }; } }",
		"enum E { A, B, C }\nclass C0 { int m(E e) { return switch (e) { case A -> 1; default -> 0; }; } }",
		"enum E { A, B, C }\nclass C0 { int m(E e) { return switch (e) { case A, B, C -> 1; }; } }",
		"class C0 { int m(int n) { return switch (n) { case 1 -> 1; }; } }",
		"enum E { A, B }\nclass C0 { void m(E e) { switch (e) { case A -> use(1); } } }",
	}
	for _, code := range cases {
		if containsCode(diagnose(code), codeNotExhaustive) {
			t.Errorf("%q should NOT flag exhaustiveness", code)
		}
	}
}

func TestNoMemberFlagged(t *testing.T) {
	cases := []string{
		"class A { int f; }\nclass B { void m(A a) { int x = a.nope; } }",
		"class A { int f; }\nclass B { void m(A a) { a.nope(); } }",
		"class C { void m() { System.ouu.println(\"x\"); } }",
		"class C { void m(String s) { String t = s.trimm(); } }",
	}
	for _, code := range cases {
		if !containsCode(diagnose(code), codeNoMember) {
			t.Errorf("%q should flag a missing member", code)
		}
	}
}

func TestNoMemberAccepted(t *testing.T) {
	cases := []string{
		"class A { int f; }\nclass B { void m(A a) { int x = a.f; } }",
		"class A {}\nclass B { void m(A a) { String s = a.toString(); } }",
		"class C { void m() { System.out.println(\"x\"); } }",
		"class C { void m(String s) { String t = s.trim(); } }",
		"class C { void m(Unknown u) { Object x = u.whatever; } }",
		"class C extends Frame { void m() { Object x = this.anything; } }",
		"enum E { A }\nclass C { void m() { E.values(); } }",
		"class Base { int shared; }\nclass Mid extends Base {}\nclass C { void m(Mid x) { int v = x.shared; } }",
	}
	for _, code := range cases {
		if containsCode(diagnose(code), codeNoMember) {
			t.Errorf("%q should NOT flag a member", code)
		}
	}
}
