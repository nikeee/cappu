package compiler

import (
	"strings"
	"testing"
)

// Port of src/compiler/checker.edge.test.ts.

func edgeFieldType(t *testing.T, text, name string) string {
	t.Helper()
	ctx := genericsSetup(text)
	return typeToString(ctx.checker.GetTypeOfSymbol(ctx.sym(name, 1)))
}

func edgeInitType(t *testing.T, text, expr string) string {
	t.Helper()
	ctx := genericsSetup(strings.Replace(text, "$EXPR", expr, 1))
	declarator := ctx.sym("zz", 1).ValueDeclaration
	return typeToString(ctx.checker.GetTypeOfExpression(declarator.AsVariableDeclarator().Initializer))
}

func assignableFields(t *testing.T, text, a, b string) bool {
	t.Helper()
	ctx := genericsSetup(text)
	return ctx.checker.IsAssignableTo(
		ctx.checker.GetTypeOfSymbol(ctx.sym(a, 1)),
		ctx.checker.GetTypeOfSymbol(ctx.sym(b, 1)))
}

func edgeMethod(body string) string {
	return "class C { C self; String[] names; void m(boolean flag, Object obj) { " + body + " } }"
}

func TestBoxingVariants(t *testing.T) {
	code := "class C { long aa; Long bb; double cc; Double dd; boolean ee; Boolean ff; char gg; Character hh; }"
	checks := []struct{ a, b string }{{"aa", "bb"}, {"dd", "cc"}, {"ee", "ff"}, {"hh", "gg"}}
	for _, ch := range checks {
		if !assignableFields(t, code, ch.a, ch.b) {
			t.Errorf("%s -> %s should be assignable (boxing)", ch.a, ch.b)
		}
	}
}

func TestWideningChains(t *testing.T) {
	code := "class C { byte vb; short vs; int vi; long vl; float vf; double vd; }"
	if !assignableFields(t, code, "vb", "vd") || !assignableFields(t, code, "vs", "vl") || !assignableFields(t, code, "vi", "vf") {
		t.Error("widening should be allowed")
	}
	if assignableFields(t, code, "vd", "vi") || assignableFields(t, code, "vl", "vb") {
		t.Error("narrowing should not be allowed")
	}
}

func TestArrayCovariance(t *testing.T) {
	code := "class C { String[] arrS; Object[] arrO; int[] arrI; long[] arrL; }"
	if !assignableFields(t, code, "arrS", "arrO") {
		t.Error("String[] -> Object[] should be allowed")
	}
	if assignableFields(t, code, "arrO", "arrS") || assignableFields(t, code, "arrI", "arrL") {
		t.Error("invalid array conversions should be rejected")
	}
}

func TestInvariantGenerics(t *testing.T) {
	code := "class C { java.util.List<String> listS; java.util.List<Object> listO; java.util.List<? extends Object> listW; }"
	if assignableFields(t, code, "listS", "listO") {
		t.Error("List<String> -> List<Object> should be rejected (invariant)")
	}
	if !assignableFields(t, code, "listS", "listW") {
		t.Error("List<String> -> List<? extends Object> should be allowed")
	}
}

func TestSubtypeAcrossGenericInterfaces(t *testing.T) {
	code := "class C { java.util.ArrayList<String> aList; java.util.Collection<String> coll; }"
	if !assignableFields(t, code, "aList", "coll") {
		t.Error("ArrayList<String> -> Collection<String> should be allowed")
	}
}

func TestGenericFieldRenders(t *testing.T) {
	if got := edgeFieldType(t, "class C { java.util.Map<String, java.util.List<Integer>> mapField; }", "mapField"); got != "Map<String, List<Integer>>" {
		t.Errorf("got %q, want Map<String, List<Integer>>", got)
	}
}

func TestInheritedGenericMemberCall(t *testing.T) {
	got := edgeInitType(t, edgeMethod("var zz = new java.util.ArrayList<String>().iterator();"), "")
	if !strings.HasPrefix(got, "Iterator") {
		t.Errorf("got %q, want Iterator...", got)
	}
}

func TestGenericMethodReturnInferred(t *testing.T) {
	if got := edgeInitType(t, "class C { <T> T id(T arg) { return arg; } void m() { var zz = id(1); } }", ""); got != "Integer" {
		t.Errorf("got %q, want Integer", got)
	}
}

func TestMemberAccessChain(t *testing.T) {
	if got := edgeInitType(t, edgeMethod("var zz = self.self.self;"), ""); got != "C" {
		t.Errorf("got %q, want C", got)
	}
}

func TestElementAccessType(t *testing.T) {
	if got := edgeInitType(t, edgeMethod("var zz = names[0];"), ""); got != "String" {
		t.Errorf("got %q, want String", got)
	}
}

func TestThisAndConcat(t *testing.T) {
	if got := edgeInitType(t, edgeMethod("var zz = this;"), ""); got != "C" {
		t.Errorf("this -> %q, want C", got)
	}
	if got := edgeInitType(t, edgeMethod("var zz = 1 + \"x\" + 2;"), ""); got != "String" {
		t.Errorf("concat -> %q, want String", got)
	}
}

func TestConditionalAndInstanceof(t *testing.T) {
	if got := edgeInitType(t, edgeMethod("var zz = flag ? \"a\" : \"b\";"), ""); got != "String" {
		t.Errorf("conditional -> %q, want String", got)
	}
	if got := edgeInitType(t, edgeMethod("var zz = obj instanceof String;"), ""); got != "boolean" {
		t.Errorf("instanceof -> %q, want boolean", got)
	}
}

func TestEnumConstantType(t *testing.T) {
	if got := edgeFieldType(t, "enum Color { RED, GREEN }", "RED"); got != "Color" {
		t.Errorf("got %q, want Color", got)
	}
}

func TestUnresolvedMemberStaysError(t *testing.T) {
	if got := edgeInitType(t, edgeMethod("var zz = obj.whatever;"), ""); got != "<error>" {
		t.Errorf("got %q, want <error>", got)
	}
}

func TestCastTakesCastType(t *testing.T) {
	if got := edgeInitType(t, edgeMethod("var zz = (String) obj;"), ""); got != "String" {
		t.Errorf("got %q, want String", got)
	}
}
