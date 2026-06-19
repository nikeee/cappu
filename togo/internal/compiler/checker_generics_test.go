package compiler

import (
	"strings"
	"testing"
)

// Port of src/compiler/checker.generics.test.ts.

func genericsSetup(text string) *checkerCtx {
	program := NewProgram()
	LoadJdkStub(program)
	program.SetOpenDocument("file:///T.java", text, 1)
	return &checkerCtx{program: program, checker: NewChecker(program), uri: "file:///T.java"}
}

func (ctx *checkerCtx) sym(needle string, occ int) *Symbol {
	sf := ctx.program.GetSourceFile(ctx.uri)
	text := sf.AsSourceFile().Text
	offset := -1
	for i := 0; i < occ; i++ {
		offset = strings.Index(text[offset+1:], needle) + offset + 1
	}
	return ctx.checker.ResolveName(GetIdentifierAtPosition(sf, offset))
}

func zzType(t *testing.T, body string) string {
	t.Helper()
	ctx := genericsSetup("import java.util.*;\nclass C<E> { E val; " + body + " }")
	declarator := ctx.sym("zz", 1).ValueDeclaration
	return typeToString(ctx.checker.GetTypeOfExpression(declarator.AsVariableDeclarator().Initializer))
}

func zzVarType(t *testing.T, body string) string {
	t.Helper()
	ctx := genericsSetup("import java.util.*;\nclass C { " + body + " }")
	return typeToString(ctx.checker.GetTypeOfSymbol(ctx.sym("zz", 1)))
}

func TestMemberAccessSubstitution(t *testing.T) {
	cases := []struct{ body, want string }{
		{"void m(List<String> xs) { var zz = xs.get(0); }", "String"},
		{"void m(Map<String, Integer> mp) { var zz = mp.get(null); }", "Integer"},
		{"void m(ArrayList<String> xs) { var zz = xs.get(0); }", "String"},
		{"void m(ArrayList<String> xs) { var zz = xs.iterator(); }", "Iterator<String>"},
		{"void m(List<String> xs) { var zz = xs.iterator(); }", "Iterator<String>"},
		{"void m(List<String> xs) { var zz = xs.iterator().next(); }", "String"},
		{"void m(C<String> c) { var zz = c.val; }", "String"},
		{"void m(List<List<String>> xs) { var zz = xs.get(0); }", "List<String>"},
	}
	for _, tc := range cases {
		if got := zzType(t, tc.body); got != tc.want {
			t.Errorf("%q -> %q, want %q", tc.body, got, tc.want)
		}
	}
}

func TestVarInference(t *testing.T) {
	cases := []struct{ body, want string }{
		{"void m() { var zz = \"hi\"; }", "String"},
		{"void m() { var zz = 1 + 2; }", "int"},
		{"void m(java.util.List<String> xs) { var zz = xs.get(0); }", "String"},
	}
	for _, tc := range cases {
		if got := zzVarType(t, tc.body); got != tc.want {
			t.Errorf("%q -> %q, want %q", tc.body, got, tc.want)
		}
	}
}

func TestEnhancedForVarInference(t *testing.T) {
	ctx := genericsSetup("import java.util.*;\nclass C { void m(List<String> xs) { for (var item : xs) { use(item); } } }")
	if got := typeToString(ctx.checker.GetTypeOfSymbol(ctx.sym("item", 1))); got != "String" {
		t.Errorf("List element var = %q, want String", got)
	}
	ctx2 := genericsSetup("class C { void m(String[] arr) { for (var item : arr) { use(item); } } }")
	if got := typeToString(ctx2.checker.GetTypeOfSymbol(ctx2.sym("item", 1))); got != "String" {
		t.Errorf("array element var = %q, want String", got)
	}
}

func TestGenericMethodInference(t *testing.T) {
	cases := []struct{ body, want string }{
		{"<T> T id(T arg) {} void m() { var zz = id(\"s\"); }", "String"},
		{"<T> T pick(T a, T b) {} void m() { var zz = pick(\"a\", \"b\"); }", "String"},
		{"<T> T first(java.util.List<T> xs) {} void m(java.util.List<String> ss) { var zz = first(ss); }", "String"},
		{"<T> T make() {} void m() { var zz = make(); }", "T"},
		{"<T> int len(T arg) {} void m() { var zz = len(\"s\"); }", "int"},
	}
	for _, tc := range cases {
		if got := zzVarType(t, tc.body); got != tc.want {
			t.Errorf("%q -> %q, want %q", tc.body, got, tc.want)
		}
	}
}
