package services

import (
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/compiler"
)

// Port of src/services/hover.test.ts.

type hoverCtx struct {
	program *compiler.Program
	checker *compiler.Checker
	text    string
}

func hoverSetup(text string) *hoverCtx {
	program := compiler.NewProgram()
	compiler.LoadJdkStub(program)
	program.SetOpenDocument("file:///T.java", text, 1)
	return &hoverCtx{program: program, checker: compiler.NewChecker(program), text: text}
}

func (ctx *hoverCtx) symbolAt(needle string, occ int) *compiler.Symbol {
	offset := -1
	for i := 0; i < occ; i++ {
		offset = strings.Index(ctx.text[offset+1:], needle) + offset + 1
	}
	sf := ctx.program.GetSourceFile("file:///T.java")
	return ctx.checker.ResolveName(compiler.GetIdentifierAtPosition(sf, offset))
}

func (ctx *hoverCtx) hoverAt(needle string, occ int) string {
	return GetHoverText(ctx.checker, ctx.symbolAt(needle, occ), nil)
}

func TestMethodHoverSignature(t *testing.T) {
	ctx := hoverSetup("class C { int add(int a, int b) { return a + b; } }")
	if got := ctx.hoverAt("add", 1); got != "int add(int a, int b)" {
		t.Errorf("got %q", got)
	}
}

func TestGenericMethodSignature(t *testing.T) {
	ctx := hoverSetup("class C { <T> T pick(T x, T y) throws Exception { return x; } }")
	if got := ctx.hoverAt("pick", 1); got != "<T> T pick(T x, T y) throws Exception" {
		t.Errorf("got %q", got)
	}
}

func TestConstructorSignatureNoReturn(t *testing.T) {
	ctx := hoverSetup("class C { C(int a) {} }")
	if got := ctx.hoverAt("C", 2); got != "C(int a)" {
		t.Errorf("got %q", got)
	}
}

func TestRecordPatternBindingHover(t *testing.T) {
	ctx := hoverSetup("record Circle(double radius) {}\nclass C { double m(Object s) { return switch (s) { case Circle(double zz) -> zz; default -> 0.0; }; } }")
	if got := ctx.hoverAt("zz", 2); got != "(local variable) double zz" {
		t.Errorf("got %q", got)
	}
}

func TestConciseLambdaParamHover(t *testing.T) {
	ctx := hoverSetup("class C { java.util.function.Function<Integer, Integer> twice = x -> x * 2; }")
	if got := ctx.hoverAt("x", 2); got != "(parameter) Integer x" {
		t.Errorf("got %q", got)
	}
}

func TestMultiParamLambdaHover(t *testing.T) {
	ctx := hoverSetup("class C { java.util.function.BiFunction<String, Integer, Integer> f = (key, num) -> num; }")
	if got := ctx.hoverAt("key", 1); got != "(parameter) String key" {
		t.Errorf("key -> %q", got)
	}
	if got := ctx.hoverAt("num", 1); got != "(parameter) Integer num" {
		t.Errorf("num -> %q", got)
	}
}

func TestLambdaParamUnknownTarget(t *testing.T) {
	ctx := hoverSetup("class C { void m() { var f = x -> x; } }")
	if got := ctx.hoverAt("x", 2); got != "(parameter) x" {
		t.Errorf("got %q", got)
	}
}

func TestArrayLengthAndObjectMethods(t *testing.T) {
	ctx := hoverSetup("class C { void m(String[] arr) { int n = arr.length; var s = arr.hashCode(); } }")
	if got := ctx.hoverAt("length", 1); got != "(field) int length" {
		t.Errorf("length -> %q", got)
	}
	if got := ctx.hoverAt("hashCode", 1); got != "int hashCode()" {
		t.Errorf("hashCode -> %q", got)
	}
}

func TestPackageQualifiersHover(t *testing.T) {
	ctx := hoverSetup("class C { java.util.List<String> xs; }")
	if got := ctx.hoverAt("java", 1); got != "package java" {
		t.Errorf("java -> %q", got)
	}
	if got := ctx.hoverAt("util", 1); got != "package java.util" {
		t.Errorf("util -> %q", got)
	}
	if got := ctx.hoverAt("List", 1); got != "interface List" {
		t.Errorf("List -> %q", got)
	}
}

func TestGetDocumentationMethod(t *testing.T) {
	ctx := hoverSetup(strings.Join([]string{
		"class C {",
		"  /**",
		"   * Adds two numbers.",
		"   * @param a first",
		"   */",
		"  int add(int a, int b) { return a + b; }",
		"}",
	}, "\n"))
	doc, ok := ctx.checker.GetDocumentation(ctx.symbolAt("add", 1))
	if !ok || doc != "Adds two numbers.\n@param a first" {
		t.Errorf("got %q (ok=%v)", doc, ok)
	}
}

func TestGetDocumentationNone(t *testing.T) {
	ctx := hoverSetup("class C {\n  // not javadoc\n  int add(int a) { return a; } }")
	if _, ok := ctx.checker.GetDocumentation(ctx.symbolAt("add", 1)); ok {
		t.Error("expected no documentation")
	}
}

func TestGetDocumentationClass(t *testing.T) {
	ctx := hoverSetup("/** A widget. */\nclass Widget {}")
	doc, ok := ctx.checker.GetDocumentation(ctx.symbolAt("Widget", 1))
	if !ok || doc != "A widget." {
		t.Errorf("got %q (ok=%v)", doc, ok)
	}
}

func (ctx *hoverCtx) callSignatureAt(needle string, occ int) string {
	offset := -1
	for i := 0; i < occ; i++ {
		offset = strings.Index(ctx.text[offset+1:], needle) + offset + 1
	}
	sf := ctx.program.GetSourceFile("file:///T.java")
	id := compiler.GetIdentifierAtPosition(sf, offset)
	call := id.Parent
	decl := ctx.checker.ResolveCall(call)
	sig, _ := ctx.checker.SignatureOfDeclaration(decl)
	return sig
}

func TestOverloadHoverStringArg(t *testing.T) {
	ctx := hoverSetup("class C { int f(int a){return 0;} String f(String s){return \"\";} void m(){ f(\"x\"); } }")
	if got := ctx.callSignatureAt("f(", 3); got != "String f(String s)" {
		t.Errorf("got %q", got)
	}
}

func TestOverloadHoverIntArg(t *testing.T) {
	ctx := hoverSetup("class C { int f(int a){return 0;} String f(String s){return \"\";} void m(){ f(1); } }")
	if got := ctx.callSignatureAt("f(", 3); got != "int f(int a)" {
		t.Errorf("got %q", got)
	}
}
