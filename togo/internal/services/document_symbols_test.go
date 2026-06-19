package services

import (
	"testing"

	"github.com/nikeee/cappu/internal/compiler"
	"github.com/nikeee/cappu/internal/lsp"
)

// Port of src/services/documentSymbols.test.ts.

func outline(text string) []lsp.DocumentSymbol {
	sf := compiler.ParseSourceFile("Test.java", text)
	return GetDocumentSymbols(sf, compiler.ComputeLineStarts(text))
}

func childNames(children []lsp.DocumentSymbol) []string {
	out := []string{}
	for _, c := range children {
		out = append(out, c.Name)
	}
	return out
}

func TestClassOutline(t *testing.T) {
	symbols := outline("class C {\n  int x;\n  C() {}\n  void m(int a) {}\n}")
	if len(symbols) != 1 {
		t.Fatalf("symbols = %d, want 1", len(symbols))
	}
	cls := symbols[0]
	if cls.Name != "C" || cls.Kind != lsp.SymbolKindClass {
		t.Errorf("class = %q kind %d", cls.Name, cls.Kind)
	}
	want := []struct {
		name string
		kind lsp.SymbolKind
	}{{"x", lsp.SymbolKindField}, {"C", lsp.SymbolKindConstructor}, {"m", lsp.SymbolKindMethod}}
	if len(cls.Children) != 3 {
		t.Fatalf("children = %d, want 3", len(cls.Children))
	}
	for i, w := range want {
		if cls.Children[i].Name != w.name || cls.Children[i].Kind != w.kind {
			t.Errorf("child %d = %q/%d, want %q/%d", i, cls.Children[i].Name, cls.Children[i].Kind, w.name, w.kind)
		}
	}
}

func TestMultipleDeclaratorsOutline(t *testing.T) {
	cls := outline("class C { int a, b, c; }")[0]
	got := childNames(cls.Children)
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("children = %v, want [a b c]", got)
	}
}

func TestEnumAndRecordOutline(t *testing.T) {
	e := outline("enum E { A, B; int code; }")[0]
	if e.Kind != lsp.SymbolKindEnum {
		t.Errorf("enum kind = %d", e.Kind)
	}
	want := []struct {
		name string
		kind lsp.SymbolKind
	}{{"A", lsp.SymbolKindEnumMember}, {"B", lsp.SymbolKindEnumMember}, {"code", lsp.SymbolKindField}}
	for i, w := range want {
		if e.Children[i].Name != w.name || e.Children[i].Kind != w.kind {
			t.Errorf("enum child %d = %q/%d, want %q/%d", i, e.Children[i].Name, e.Children[i].Kind, w.name, w.kind)
		}
	}
	r := outline("record Point(int x, int y) {}")[0]
	if got := childNames(r.Children); len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Errorf("record children = %v, want [x y]", got)
	}
}

func TestNestedTypesOutline(t *testing.T) {
	outer := outline("class Outer { interface Inner { void f(); } }")[0]
	inner := outer.Children[0]
	if inner.Name != "Inner" || inner.Kind != lsp.SymbolKindInterface {
		t.Errorf("inner = %q/%d", inner.Name, inner.Kind)
	}
	if got := childNames(inner.Children); len(got) != 1 || got[0] != "f" {
		t.Errorf("inner children = %v, want [f]", got)
	}
}

func TestSelectionRangeContained(t *testing.T) {
	cls := outline("class C {\n  void method() {}\n}")[0]
	m := cls.Children[0]
	before := func(a, b lsp.Position) bool {
		return a.Line < b.Line || (a.Line == b.Line && a.Character <= b.Character)
	}
	if !before(m.Range.Start, m.SelectionRange.Start) || !before(m.SelectionRange.End, m.Range.End) {
		t.Error("selectionRange should be contained in range")
	}
}
