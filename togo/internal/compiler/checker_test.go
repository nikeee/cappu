package compiler

import (
	"strings"
	"testing"
)

// Port of src/compiler/checker.test.ts.

type checkerCtx struct {
	program *Program
	checker *Checker
	uri     URI
}

func checkerSetup(text string) *checkerCtx {
	program := NewProgram()
	LoadJdkStub(program)
	uri := URI("file:///T.java")
	program.SetOpenDocument(uri, text, 1)
	return &checkerCtx{program: program, checker: NewChecker(program), uri: uri}
}

func (ctx *checkerCtx) identifierAt(needle string, occurrence int) *Node {
	sf := ctx.program.GetSourceFile(ctx.uri)
	text := sf.AsSourceFile().Text
	offset := -1
	for i := 0; i < occurrence; i++ {
		offset = strings.Index(text[offset+1:], needle) + offset + 1
	}
	return GetIdentifierAtPosition(sf, offset)
}

func TestLambdaReturnTypedAgainstSAM(t *testing.T) {
	ctx := checkerSetup("class C { interface Sup<T> { T get(); } interface Run { void go(); }" +
		" void m() { Sup<Run> s = () -> { return () -> {}; }; } }")
	sf := ctx.program.GetSourceFile(ctx.uri)
	text := sf.AsSourceFile().Text
	node := GetNodeAtPosition(sf, strings.Index(text, "() -> {}"))
	for node != nil && node.Kind != LambdaExpression {
		node = node.Parent
	}
	info := ctx.checker.GetLambdaInfo(node)
	if info == nil || typeToString(info.InterfaceType) != "Run" {
		t.Errorf("inner lambda interface = %v, want Run", info)
	}
}

func TestDeclaredTypesOfFieldsAndMethods(t *testing.T) {
	ctx := checkerSetup("class C { String name; int count() { return 0; } }")
	nameSym := ctx.checker.ResolveName(ctx.identifierAt("name", 1))
	if got := typeToString(ctx.checker.GetTypeOfSymbol(nameSym)); got != "String" {
		t.Errorf("name type = %q, want String", got)
	}
	countSym := ctx.checker.ResolveName(ctx.identifierAt("count", 1))
	if got := typeToString(ctx.checker.GetTypeOfSymbol(countSym)); got != "int" {
		t.Errorf("count type = %q, want int", got)
	}
}

func TestMemberAccessThroughStub(t *testing.T) {
	ctx := checkerSetup("class C { String s; void m() { s.length(); } }")
	lengthSym := ctx.checker.ResolveName(ctx.identifierAt("length", 1))
	if lengthSym == nil || lengthSym.Flags != SymbolFlagsMethod {
		t.Fatalf("length symbol = %v, want Method", lengthSym)
	}
	sf := ctx.program.GetSourceFile(ctx.uri)
	text := sf.AsSourceFile().Text
	node := GetNodeAtPosition(sf, strings.Index(text, "length"))
	for node.Kind != CallExpression {
		node = node.Parent
	}
	if got := typeToString(ctx.checker.GetTypeOfExpression(node)); got != "int" {
		t.Errorf("s.length() type = %q, want int", got)
	}
}

func (ctx *checkerCtx) initType(t *testing.T, varName string) string {
	sym := ctx.checker.ResolveName(ctx.identifierAt(varName, 1))
	declarator := sym.ValueDeclaration
	return typeToString(ctx.checker.GetTypeOfExpression(declarator.AsVariableDeclarator().Initializer))
}

func initializerType(t *testing.T, text, varName string) string {
	t.Helper()
	return checkerSetup(text).initType(t, varName)
}

func TestExpressionTyping(t *testing.T) {
	cases := []struct{ text, varName, want string }{
		{"class C { void m() { var x = 1 + 2; } }", "x", "int"},
		{"class C { void m() { var x = 1 + 2.0; } }", "x", "double"},
		{"class C { void m() { var x = \"a\" + 1; } }", "x", "String"},
		{"class C { void m() { var x = 1 < 2; } }", "x", "boolean"},
		{"class C { void m() { var x = new C(); } }", "x", "C"},
	}
	for _, tc := range cases {
		if got := initializerType(t, tc.text, tc.varName); got != tc.want {
			t.Errorf("%q -> %q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestUnaryPromotion(t *testing.T) {
	cases := []struct{ text, want string }{
		{"class C { void m() { byte b = 1; var x = -b; } }", "int"},
		{"class C { void m() { char c = 'a'; var x = ~c; } }", "int"},
		{"class C { void m() { short s = 1; var x = +s; } }", "int"},
		{"class C { void m() { long l = 1L; var x = -l; } }", "long"},
		{"class C { void m() { byte b = 1; var x = ++b; } }", "byte"},
	}
	for _, tc := range cases {
		if got := initializerType(t, tc.text, "x"); got != tc.want {
			t.Errorf("%q -> %q, want %q", tc.text, got, tc.want)
		}
	}
}

func (ctx *checkerCtx) diagCount() int {
	return len(ctx.checker.GetSemanticDiagnostics(ctx.program.GetSourceFile(ctx.uri)))
}

func TestPrimitiveAssignments(t *testing.T) {
	zero := []string{
		"class C { void m() { long l = 1; double d = l; byte b = 1; } }",
		"class C { void m() { short s = 1 + 2; char c = 65; } }",
		"class C { void m(int p) { final int k = 1; byte b = k; } }",
	}
	for _, text := range zero {
		if n := checkerSetup(text).diagCount(); n != 0 {
			t.Errorf("%q -> %d diagnostics, want 0", text, n)
		}
	}
	one := []string{
		"class C { void m() { byte b = 128; } }",
		"class C { void m() { char c = -1; } }",
		"class C { void m(long p) { int i = p; } }",
		"class C { void m() { float f = 1.5; } }",
		"class C { void m() { int x = 1; boolean y = x; } }",
		"class C { void m() { boolean t = true; int z = t; } }",
	}
	for _, text := range one {
		if n := checkerSetup(text).diagCount(); n != 1 {
			t.Errorf("%q -> %d diagnostics, want 1", text, n)
		}
	}
}

func TestCStyleArrayRank(t *testing.T) {
	ctx := checkerSetup("class C { char buf[]; int grid[][]; void m(int xs[]) { buf = new char[1]; } }")
	if got := typeToString(ctx.checker.GetTypeOfSymbol(ctx.checker.ResolveName(ctx.identifierAt("buf", 1)))); got != "char[]" {
		t.Errorf("buf type = %q, want char[]", got)
	}
	if got := typeToString(ctx.checker.GetTypeOfSymbol(ctx.checker.ResolveName(ctx.identifierAt("grid", 1)))); got != "int[][]" {
		t.Errorf("grid type = %q, want int[][]", got)
	}
	if got := typeToString(ctx.checker.GetTypeOfSymbol(ctx.checker.ResolveName(ctx.identifierAt("xs", 1)))); got != "int[]" {
		t.Errorf("xs type = %q, want int[]", got)
	}
	if ctx.diagCount() != 0 {
		t.Error("C-style array decls should produce no diagnostics")
	}
}

func (ctx *checkerCtx) diagsWithCode(code int) []string {
	var out []string
	for _, d := range ctx.checker.GetSemanticDiagnostics(ctx.program.GetSourceFile(ctx.uri)) {
		if int(d.Code) == code {
			out = append(out, d.MessageText)
		}
	}
	return out
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestUnusedImports(t *testing.T) {
	cases := []struct {
		text string
		want []string
	}{
		{"import java.util.List;\nclass C {}", []string{"Unused import 'java.util.List'."}},
		{"import java.util.List;\nclass C { List<String> l; }", nil},
		{"import java.util.List;\nclass C { void m() { int List = 1; } }", nil},
		{"import static java.util.List.of;\nclass C {}", []string{"Unused import 'java.util.List.of'."}},
		{"import static java.util.List.of;\nclass C { Object m() { return of(1); } }", nil},
		{"import java.util.*;\nclass C {}", nil},
		{"import java.util.List;\nimport java.util.List;\nimport java.util.*;\nimport java.util.*;\nclass C { List<String> l; }",
			[]string{"Unused import 'java.util.List'.", "Unused import 'java.util'."}},
		{"import java.util.List;\nclass C { void m() { int = ; } }", nil},
	}
	for _, tc := range cases {
		got := checkerSetup(tc.text).diagsWithCode(1305)
		if !eqStrings(got, tc.want) {
			t.Errorf("%q -> %v, want %v", tc.text, got, tc.want)
		}
	}
}

func TestArgumentTypes1300(t *testing.T) {
	cases := []struct {
		text string
		want []string
	}{
		{"class C { void f(String s) {} void m() { f(1); } }",
			[]string{"Incompatible types: 'int' cannot be converted to 'String'."}},
		{"class C { void f(int a) {} void m() { f(\"x\"); } }",
			[]string{"Incompatible types: 'String' cannot be converted to 'int'."}},
		{"class C { void f(long a) {} void m() { f(1); } }", nil},
		{"class C { void f(int a) {} void m(long x) { f(x); } }",
			[]string{"Incompatible types: 'long' cannot be converted to 'int'."}},
		{"class C { void f(int a) {} void f(int a, int b) {} void m() { f(\"s\"); } }",
			[]string{"Incompatible types: 'String' cannot be converted to 'int'."}},
		{"class C { void f(int a) {} void f(String s) {} void m() { f(true); } }", nil},
		{"class C { void f(int a, String... s) {} void m() { f(\"x\", \"y\"); } }",
			[]string{"Incompatible types: 'String' cannot be converted to 'int'."}},
		{"class C { void f(int a, String... s) {} void m() { f(1, \"y\"); } }", nil},
		{"class P { P(int a) {} } class Main { void m() { P p = new P(\"s\"); } }",
			[]string{"Incompatible types: 'String' cannot be converted to 'int'."}},
		{"class C { void toString(boolean b, StringBuilder sb) {} void m() { String s = toString(); } }", nil},
	}
	for _, tc := range cases {
		got := checkerSetup(tc.text).diagsWithCode(1300)
		if !eqStrings(got, tc.want) {
			t.Errorf("%q -> %v, want %v", tc.text, got, tc.want)
		}
	}
}

func TestArity1304(t *testing.T) {
	cases := []struct {
		text string
		want []string
	}{
		{"class Main { static void lol(String[] a) {} static void m() { lol(); } }",
			[]string{"Invalid number of arguments: expected 1, got 0."}},
		{"class C { int f(int a, int b) { return 0; } void m(C c) { c.f(1); } }",
			[]string{"Invalid number of arguments: expected 2, got 1."}},
		{"class C { void f() {} void f(int a) {} void m() { f(1, 2, 3); } }",
			[]string{"Invalid number of arguments: expected 0 or 1, got 3."}},
		{"class C { void f(int a, String... s) {} void m() { f(1); f(1, \"x\", \"y\"); } }", nil},
		{"class C { void f(int a, String... s) {} void m() { f(); } }",
			[]string{"Invalid number of arguments: expected 1+, got 0."}},
		{"class A { A(int x) {} } class B { void m() { new A(); } }",
			[]string{"Invalid number of arguments: expected 1, got 0."}},
		{"class A { } class B { void m() { new A(1); } }",
			[]string{"Invalid number of arguments: expected 0, got 1."}},
		{"record R(int a, String b) {} class C { void m() { new R(1); } }",
			[]string{"Invalid number of arguments: expected 2, got 1."}},
		{"class A { A(int x) {} } record R(int a) {} class C { void f(int a) {} void m() { f(1); new A(2); new R(3); } }", nil},
	}
	for _, tc := range cases {
		got := checkerSetup(tc.text).diagsWithCode(1304)
		if !eqStrings(got, tc.want) {
			t.Errorf("%q -> %v, want %v", tc.text, got, tc.want)
		}
	}
}

func TestUnknownDegradesToError(t *testing.T) {
	if got := initializerType(t, "class C { void m() { var x = mystery(); } }", "x"); got != "<error>" {
		t.Errorf("got %q, want <error>", got)
	}
}

func TestAssignability(t *testing.T) {
	ctx := checkerSetup("class C { int iv; long lv; double dv; Integer bi; String sv; Object ov;" +
		" java.util.ArrayList<String> al; java.util.List<String> ls; int[] ia; String[] sa; }")
	typ := func(name string) *Type {
		return ctx.checker.GetTypeOfSymbol(ctx.checker.ResolveName(ctx.identifierAt(name, 1)))
	}
	a := func(x, y string) bool { return ctx.checker.IsAssignableTo(typ(x), typ(y)) }
	checks := []struct {
		x, y string
		want bool
	}{
		{"iv", "lv", true}, {"iv", "dv", true}, {"lv", "iv", false},
		{"iv", "bi", true}, {"bi", "iv", true},
		{"sv", "ov", true}, {"ov", "sv", false}, {"al", "ls", true},
		{"sa", "ov", true}, {"ia", "sa", false},
	}
	for _, ch := range checks {
		if got := a(ch.x, ch.y); got != ch.want {
			t.Errorf("isAssignable(%s,%s) = %v, want %v", ch.x, ch.y, got, ch.want)
		}
	}
	if !ctx.checker.IsAssignableTo(nullType, typ("sv")) {
		t.Error("null -> String should be assignable")
	}
	if ctx.checker.IsAssignableTo(nullType, typ("iv")) {
		t.Error("null -> int should not be assignable")
	}
}

func TestWildcardVariance(t *testing.T) {
	ctx := checkerSetup("class C { java.util.List<String> ls; java.util.List<? extends Object> wl; java.util.List<Object> lo; }")
	typ := func(name string) *Type {
		return ctx.checker.GetTypeOfSymbol(ctx.checker.ResolveName(ctx.identifierAt(name, 1)))
	}
	if !ctx.checker.IsAssignableTo(typ("ls"), typ("wl")) {
		t.Error("List<String> -> List<? extends Object> should be allowed (covariant)")
	}
	if ctx.checker.IsAssignableTo(typ("ls"), typ("lo")) {
		t.Error("List<String> -> List<Object> should NOT be allowed (invariant)")
	}
}

func TestOverloadResolution(t *testing.T) {
	if got := initializerType(t, "class C { String f(int x){return \"\";} int f(String s){return 0;} void m(){ var res = f(1); } }", "res"); got != "String" {
		t.Errorf("f(1) -> %q, want String", got)
	}
	if got := initializerType(t, "class C { String f(int x){return \"\";} int f(String s){return 0;} void m(){ var res = f(\"s\"); } }", "res"); got != "int" {
		t.Errorf("f(\"s\") -> %q, want int", got)
	}
}

func TestStrictBeatsBoxing(t *testing.T) {
	if got := initializerType(t, "class C { int f(int x){return 0;} String f(Integer i){return \"\";} void m(){ var res = f(1); } }", "res"); got != "int" {
		t.Errorf("got %q, want int", got)
	}
}

func TestVarargsOnlyWhenNeeded(t *testing.T) {
	if got := initializerType(t, "class C { int g(int... xs){return 0;} void m(){ var res = g(1, 2, 3); } }", "res"); got != "int" {
		t.Errorf("got %q, want int", got)
	}
	if got := initializerType(t, "class C { String g(int a){return \"\";} int g(int... xs){return 0;} void m(){ var res = g(7); } }", "res"); got != "String" {
		t.Errorf("got %q, want String", got)
	}
}

func TestMostSpecificOverload(t *testing.T) {
	if got := initializerType(t, "class C { int h(Object o){return 0;} String h(String s){return \"\";} void m(){ var res = h(\"s\"); } }", "res"); got != "String" {
		t.Errorf("got %q, want String", got)
	}
}

func semanticDiags(text string) []string {
	ctx := checkerSetup(text)
	var out []string
	for _, d := range ctx.checker.GetSemanticDiagnostics(ctx.program.GetSourceFile(ctx.uri)) {
		out = append(out, d.MessageText)
	}
	return out
}

func TestTypeMismatchReported(t *testing.T) {
	for _, text := range []string{
		"class C { int x = \"s\"; }",
		"class C { String s = 3; }",
		"class C { int m() { return \"s\"; } }",
		"class C { void m() { int v = 0; v = \"s\"; } }",
	} {
		if got := semanticDiags(text); len(got) != 1 {
			t.Errorf("%q -> %d diagnostics, want 1: %v", text, len(got), got)
		}
	}
}

func TestCompatibleNoDiagnostics(t *testing.T) {
	for _, text := range []string{
		"class C { int x = 1; long y = x; double d = y; }",
		"class C { String s = \"ok\"; Object o = s; Integer bi = 3; }",
		"class C { int m() { return 0; } }",
	} {
		if got := semanticDiags(text); len(got) != 0 {
			t.Errorf("%q -> %d diagnostics, want 0: %v", text, len(got), got)
		}
	}
}

func TestNoFalsePositives(t *testing.T) {
	for _, text := range []string{
		"class C { void m() { var x = mystery(); int y = x; } }",
		"class C<T> { T t; void m(T p) { t = p; } }",
		"class C { java.util.List<String> a; void m() { a = a; } }",
	} {
		if got := semanticDiags(text); len(got) != 0 {
			t.Errorf("%q -> %d diagnostics, want 0: %v", text, len(got), got)
		}
	}
}

// Fields that can be 'final' (nikeee/cappu#38, code 1317).
// Port of the "can be final" tests in src/compiler/checker.test.ts.

func TestFieldCanBeFinalWithInitializer(t *testing.T) {
	cases := []struct {
		text string
		want []string
	}{
		{"class C { private int x = 1; int use() { return x; } }", []string{"Field 'x' can be 'final'."}},
		{"class C { private static String S = \"s\"; }", []string{"Field 'S' can be 'final'."}},
		// multi-declarator: all declarators clean -> all reported
		{"class C { private int a = 1, b = 2; }", []string{"Field 'a' can be 'final'.", "Field 'b' can be 'final'."}},
	}
	for _, tc := range cases {
		got := checkerSetup(tc.text).diagsWithCode(1317)
		if !eqStrings(got, tc.want) {
			t.Errorf("%q -> %v, want %v", tc.text, got, tc.want)
		}
	}
}

func TestFieldCanBeFinalIsSuggestionOnName(t *testing.T) {
	ctx := checkerSetup("class C { private int count = 1; }")
	sf := ctx.program.GetSourceFile(ctx.uri)
	for _, d := range ctx.checker.GetSemanticDiagnostics(sf) {
		if d.Code != 1317 {
			continue
		}
		if d.Category != CategorySuggestion {
			t.Errorf("category = %v, want CategorySuggestion", d.Category)
		}
		if got := sf.AsSourceFile().Text[d.Pos:d.End]; got != "count" {
			t.Errorf("range spans %q, want \"count\"", got)
		}
		return
	}
	t.Fatal("no 1317 diagnostic reported")
}

func TestFieldCanBeFinalReassignedStaysSilent(t *testing.T) {
	cases := []string{
		"class C { private int x = 1; void m() { x = 2; } }",
		"class C { private int x = 1; void m() { x += 2; } }",
		"class C { private int x = 1; void m() { x++; } }",
		"class C { private int x = 1; void m() { --x; } }",
		"class C { private int x = 1; void m() { this.x = 2; } }",
		"class C { private int x = 1; void m(C o) { o.x = 2; } }",
		// writes from a nested class or lambda in the same file count too
		"class C { private int x = 1; class N { void m() { x = 2; } } }",
		"class C { private int x = 1; Runnable r = () -> { x = 2; }; }",
		// multi-declarator is all-or-nothing: one written declarator silences the field
		"class C { private int a = 1, b = 2; void m() { b = 3; } }",
	}
	for _, text := range cases {
		if got := checkerSetup(text).diagsWithCode(1317); got != nil {
			t.Errorf("%q -> %v, want none", text, got)
		}
	}
}

func TestFieldCanBeFinalIneligibleModifiers(t *testing.T) {
	cases := []string{
		"class C { private final int x = 1; }",
		"class C { private volatile int x = 1; }",
		"class C { int x = 1; }",
		"class C { public int x = 1; }",
	}
	for _, text := range cases {
		if got := checkerSetup(text).diagsWithCode(1317); got != nil {
			t.Errorf("%q -> %v, want none", text, got)
		}
	}
}

func TestFieldCanBeFinalCtorAssigned(t *testing.T) {
	want := []string{"Field 'x' can be 'final'."}
	cases := []string{
		"class C { private int x; C(int v) { this.x = v; } }",
		// bare-name assignment counts too
		"class C { private int x; C() { x = 1; } }",
		// every constructor assigns exactly once
		"class C { private int x; C() { x = 1; } C(int v) { x = v; } }",
		// a delegating constructor must not assign (the delegate already did)
		"class C { private int x; C() { this(1); } C(int v) { x = v; } }",
	}
	for _, text := range cases {
		if got := checkerSetup(text).diagsWithCode(1317); !eqStrings(got, want) {
			t.Errorf("%q -> %v, want %v", text, got, want)
		}
	}
}

func TestFieldCanBeFinalCtorUnprovableStaysSilent(t *testing.T) {
	cases := []string{
		// some constructor does not assign
		"class C { private int x; C() { x = 1; } C(int v) { } }",
		// no constructor at all
		"class C { private int x; }",
		// assigned twice in one constructor
		"class C { private int x; C() { x = 1; x = 2; } }",
		// initializer AND constructor write would double-assign a final field
		"class C { private int x = 1; C() { x = 2; } }",
		// assignment nested in a conditional/loop is not a top-level statement
		"class C { private int x; C(boolean b) { if (b) { x = 1; } } }",
		"class C { private int x; C() { for (int i = 0; i < 2; i++) { x = i; } } }",
		// an early return could skip the assignment
		"class C { private int x; C(boolean b) { if (b) return; x = 1; } }",
		// a delegating constructor that ALSO assigns would double-assign
		"class C { private int x; C() { this(1); x = 2; } C(int v) { x = v; } }",
		// assignment through another instance is illegal on a blank final
		"class C { private int x; C(C o) { o.x = 1; } }",
		// compound assignment is a read-modify-write, never valid on a blank final
		"class C { private int x; C() { x += 1; } }",
		// written in a method too
		"class C { private int x; C() { x = 1; } void m() { x = 2; } }",
		// initializer-block assignment is deferred (static and instance)
		"class C { private static int x; static { x = 1; } }",
		"class C { private int x; C() {} { x = 1; } }",
		// a recovered parse stays silent
		"class C { private int x = 1; void m() { int = ; } }",
	}
	for _, text := range cases {
		if got := checkerSetup(text).diagsWithCode(1317); got != nil {
			t.Errorf("%q -> %v, want none", text, got)
		}
	}
}
