package compiler

import (
	"strings"
	"testing"
)

// Port of src/compiler/resolver.test.ts.

// resolveAt resolves the name at the nth occurrence of needle in text.
func resolveAt(t *testing.T, text, needle string, occurrence int) *Symbol {
	t.Helper()
	program := NewProgram()
	program.SetOpenDocument("file:///T.java", text, 1)
	sf := program.GetSourceFile("file:///T.java")
	offset := -1
	for i := 0; i < occurrence; i++ {
		offset = strings.Index(text[offset+1:], needle) + offset + 1
	}
	id := GetIdentifierAtPosition(sf, offset)
	if id == nil {
		return nil
	}
	return ResolveIdentifier(id, program)
}

func TestLocalVariableUseResolves(t *testing.T) {
	sym := resolveAt(t, "class C { void m() { int x = 1; return x; } }", "x", 2)
	if sym == nil || sym.Flags != SymbolFlagsLocalVariable || sym.EscapedName != "x" {
		t.Errorf("got %+v, want local 'x'", sym)
	}
}

func TestParameterUseResolves(t *testing.T) {
	sym := resolveAt(t, "class C { int m(int a) { return a; } }", "a", 2)
	if sym == nil || sym.Flags != SymbolFlagsParameter {
		t.Errorf("got %+v, want parameter", sym)
	}
}

func TestFieldUseResolves(t *testing.T) {
	sym := resolveAt(t, "class C { int f; void m() { f = 1; } }", "f", 2)
	if sym == nil || sym.Flags != SymbolFlagsField {
		t.Errorf("got %+v, want field", sym)
	}
}

func TestLocalShadowsField(t *testing.T) {
	sym := resolveAt(t, "class C { int x; void m() { int x = 1; return x; } }", "x", 3)
	if sym == nil || sym.Flags != SymbolFlagsLocalVariable {
		t.Errorf("got %+v, want local (shadowing field)", sym)
	}
}

func TestTypeRefResolvesFileLocalLater(t *testing.T) {
	sym := resolveAt(t, "class C extends Base {}\nclass Base {}", "Base", 1)
	if sym == nil || sym.Flags != SymbolFlagsClass || sym.EscapedName != "Base" {
		t.Errorf("got %+v, want class Base", sym)
	}
}

func TestTypeParameterUseResolves(t *testing.T) {
	sym := resolveAt(t, "class C<T> { T get() { return null; } }", "T", 2)
	if sym == nil || sym.Flags != SymbolFlagsTypeParameter {
		t.Errorf("got %+v, want type parameter", sym)
	}
}

func TestMethodCallNameResolves(t *testing.T) {
	sym := resolveAt(t, "class C { void m() { helper(); } int helper() { return 0; } }", "helper", 1)
	if sym == nil || sym.Flags != SymbolFlagsMethod {
		t.Errorf("got %+v, want method", sym)
	}
}

func TestDeclarationNameResolvesToItself(t *testing.T) {
	sym := resolveAt(t, "class C { int field; }", "field", 1)
	if sym == nil || sym.Flags != SymbolFlagsField || sym.EscapedName != "field" {
		t.Errorf("got %+v, want field 'field'", sym)
	}
}

func TestUnresolvedNameReturnsNil(t *testing.T) {
	if sym := resolveAt(t, "class C { void m() { unknownThing(); } }", "unknownThing", 1); sym != nil {
		t.Errorf("got %+v, want nil", sym)
	}
}

// --- cross-file resolution, inheritance, find-references ---------------------

func programOf(files map[URI]string) *Program {
	program := NewProgram()
	for uri, text := range files {
		program.SetOpenDocument(uri, text, 1)
	}
	return program
}

func resolveInFile(t *testing.T, program *Program, uri URI, needle string, occurrence int) *Symbol {
	t.Helper()
	sf := program.GetSourceFile(uri)
	text := sf.AsSourceFile().Text
	offset := -1
	for i := 0; i < occurrence; i++ {
		offset = strings.Index(text[offset+1:], needle) + offset + 1
	}
	id := GetIdentifierAtPosition(sf, offset)
	if id == nil {
		return nil
	}
	return ResolveIdentifier(id, program)
}

func TestSamePackageTypeResolvesAcrossFiles(t *testing.T) {
	program := programOf(map[URI]string{
		"file:///A.java": "package p;\nclass A extends B {}",
		"file:///B.java": "package p;\nclass B {}",
	})
	sym := resolveInFile(t, program, "file:///A.java", "B", 1)
	if sym == nil || sym.Flags != SymbolFlagsClass {
		t.Fatalf("got %+v, want class B", sym)
	}
	if sym != program.GetGlobalIndex().GetType("p.B") {
		t.Error("should be the same symbol as the global index's p.B")
	}
}

func TestSingleTypeImportResolves(t *testing.T) {
	program := programOf(map[URI]string{
		"file:///A.java": "package p;\nimport q.B;\nclass A extends B {}",
		"file:///B.java": "package q;\npublic class B {}",
	})
	if resolveInFile(t, program, "file:///A.java", "B", 2) != program.GetGlobalIndex().GetType("q.B") {
		t.Error("single-type import should resolve q.B")
	}
}

func TestOnDemandImportResolves(t *testing.T) {
	program := programOf(map[URI]string{
		"file:///A.java": "package p;\nimport q.*;\nclass A extends B {}",
		"file:///B.java": "package q;\npublic class B {}",
	})
	if resolveInFile(t, program, "file:///A.java", "B", 1) != program.GetGlobalIndex().GetType("q.B") {
		t.Error("on-demand import should resolve q.B")
	}
}

func TestFullyQualifiedNameResolves(t *testing.T) {
	program := programOf(map[URI]string{
		"file:///A.java": "package p;\nclass A extends q.B {}",
		"file:///B.java": "package q;\npublic class B {}",
	})
	if resolveInFile(t, program, "file:///A.java", "B", 1) != program.GetGlobalIndex().GetType("q.B") {
		t.Error("fully-qualified name should resolve q.B")
	}
}

func TestInheritedFieldResolves(t *testing.T) {
	program := programOf(map[URI]string{
		"file:///Base.java": "package p;\nclass Base { int f; }",
		"file:///Sub.java":  "package p;\nclass Sub extends Base { void m() { f = 1; } }",
	})
	sym := resolveInFile(t, program, "file:///Sub.java", "f", 1)
	if sym == nil || sym.Flags != SymbolFlagsField {
		t.Fatalf("got %+v, want field", sym)
	}
	if sym != program.GetGlobalIndex().GetType("p.Base").Members["f"] {
		t.Error("inherited field should be Base's member f")
	}
}

func TestFindReferencesDeclarationAndUses(t *testing.T) {
	program := programOf(map[URI]string{
		"file:///C.java": "class C { int x; void m() { x = x + 1; } }",
	})
	sym := resolveInFile(t, program, "file:///C.java", "x", 2)
	refs := FindReferences(sym, program, nil)
	if len(refs) != 3 {
		t.Errorf("references = %d, want 3 (declaration + 2 uses)", len(refs))
	}
}

func TestFindReferencesLocalStaysInFile(t *testing.T) {
	program := programOf(map[URI]string{
		"file:///A.java": "package p;\nclass A { void m() { int local = 1; use(local); } }",
		"file:///B.java": "package p;\nclass B { void m() { int local = 2; use(local); } }",
	})
	sym := resolveInFile(t, program, "file:///A.java", "local", 1)
	if sym == nil || sym.Flags != SymbolFlagsLocalVariable {
		t.Fatalf("got %+v, want local", sym)
	}
	refs := FindReferences(sym, program, nil)
	if len(refs) != 2 {
		t.Errorf("references = %d, want 2 (only in A.java)", len(refs))
	}
}

func TestFindReferencesCrossFileType(t *testing.T) {
	program := programOf(map[URI]string{
		"file:///Base.java": "package p;\nclass Base {}",
		"file:///A.java":    "package p;\nclass A extends Base {}",
		"file:///B.java":    "package p;\nclass B extends Base {}",
	})
	sym := program.GetGlobalIndex().GetType("p.Base")
	refs := FindReferences(sym, program, nil)
	if len(refs) != 3 {
		t.Errorf("references = %d, want 3 (declaration + 2 extends)", len(refs))
	}
}
