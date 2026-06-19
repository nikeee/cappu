package compiler

import (
	"strings"
	"testing"
)

// Port of src/compiler/resolver.edge.test.ts. These cases load the JDK stub and
// use both the lexical resolver and the checker's member-aware resolveName.

func edgeCtx(text string) *checkerCtx {
	program := NewProgram()
	LoadJdkStub(program)
	program.SetOpenDocument("file:///T.java", text, 1)
	return &checkerCtx{program: program, checker: NewChecker(program), uri: "file:///T.java"}
}

func (ctx *checkerCtx) idAt(needle string, occ int) *Node {
	sf := ctx.program.GetSourceFile(ctx.uri)
	text := sf.AsSourceFile().Text
	offset := -1
	for i := 0; i < occ; i++ {
		offset = strings.Index(text[offset+1:], needle) + offset + 1
	}
	return GetIdentifierAtPosition(sf, offset)
}

// resolveAtEdge uses the plain lexical resolver (resolveIdentifier).
func resolveAtEdge(t *testing.T, text, needle string, occ int) *Symbol {
	t.Helper()
	ctx := edgeCtx(text)
	id := ctx.idAt(needle, occ)
	if id == nil {
		return nil
	}
	return ResolveIdentifier(id, ctx.program)
}

// resolveNameAtEdge uses the checker's member-aware resolveName.
func resolveNameAtEdge(t *testing.T, text, needle string, occ int) *Symbol {
	t.Helper()
	ctx := edgeCtx(text)
	id := ctx.idAt(needle, occ)
	if id == nil {
		return nil
	}
	return ctx.checker.ResolveName(id)
}

func TestParameterShadowsField(t *testing.T) {
	if sym := resolveAtEdge(t, "class C { int x; int m(int x) { return x; } }", "x", 3); sym == nil || sym.Flags != SymbolFlagsParameter {
		t.Errorf("got %v, want parameter", sym)
	}
}

func TestLocalShadowsFieldEdge(t *testing.T) {
	if sym := resolveAtEdge(t, "class C { int f; int m() { int f = 1; return f; } }", "f", 3); sym == nil || sym.Flags != SymbolFlagsLocalVariable {
		t.Errorf("got %v, want local", sym)
	}
}

func TestForwardReferenceField(t *testing.T) {
	if sym := resolveAtEdge(t, "class C { int m() { return later; } int later; }", "later", 1); sym == nil || sym.Flags != SymbolFlagsField {
		t.Errorf("got %v, want field", sym)
	}
}

func TestFieldInheritedTwoLevels(t *testing.T) {
	sym := resolveAtEdge(t, "class A extends B { int m() { return g; } }\nclass B extends Base {}\nclass Base { int g; }", "g", 1)
	if sym == nil || sym.Flags != SymbolFlagsField {
		t.Errorf("got %v, want field", sym)
	}
}

func TestDefaultInterfaceMethodInherited(t *testing.T) {
	sym := resolveNameAtEdge(t, "interface I { default int d() { return 1; } }\nclass C implements I { void m() { d(); } }", "d", 2)
	if sym == nil || sym.Flags != SymbolFlagsMethod {
		t.Errorf("got %v, want method", sym)
	}
}

func TestEnhancedForVariableResolves(t *testing.T) {
	sym := resolveAtEdge(t, "class C { void m(java.util.List<String> xs) { for (String item : xs) { use(item); } } }", "item", 2)
	if sym == nil || sym.Flags != SymbolFlagsParameter {
		t.Errorf("got %v, want parameter", sym)
	}
}

func TestCatchParameterResolves(t *testing.T) {
	sym := resolveAtEdge(t, "class C { void m() { try {} catch (Exception ex) { use(ex); } } }", "ex", 2)
	if sym == nil || sym.Flags != SymbolFlagsParameter {
		t.Errorf("got %v, want parameter", sym)
	}
}

func TestTypedLambdaParameterResolves(t *testing.T) {
	sym := resolveAtEdge(t, "class C { Runnable r = (int arg) -> { use(arg); }; }", "arg", 2)
	if sym == nil || sym.Flags != SymbolFlagsParameter {
		t.Errorf("got %v, want parameter", sym)
	}
}

func TestEnumConstantThroughMemberAccess(t *testing.T) {
	sym := resolveNameAtEdge(t, "enum E { A, B }\nclass C { E x = E.A; }", "A", 2)
	if sym == nil || sym.Flags != SymbolFlagsEnumConstant {
		t.Errorf("got %v, want enum constant", sym)
	}
}

func TestNestedTypeAsMemberType(t *testing.T) {
	sym := resolveAtEdge(t, "class O { Inner make() { return null; } class Inner {} }", "Inner", 1)
	if sym == nil || sym.Flags != SymbolFlagsClass {
		t.Errorf("got %v, want class", sym)
	}
}

func TestOverloadedMethodOneSymbol(t *testing.T) {
	sym := resolveAtEdge(t, "class C { void m() { helper(); } void helper(){} void helper(int a){} }", "helper", 1)
	if sym == nil || len(sym.Declarations) != 2 {
		t.Errorf("got %v declarations, want 2", sym)
	}
}

func TestMethodScopedTypeParameterResolves(t *testing.T) {
	sym := resolveAtEdge(t, "class C { <T> T id(T x) { T y = x; return y; } }", "T", 4)
	if sym == nil || sym.Flags != SymbolFlagsTypeParameter {
		t.Errorf("got %v, want type parameter", sym)
	}
}
