package compiler

import "testing"

// Port of the SE9 module-declaration cases in src/compiler/parser.test.ts.

func TestModuleAllDirectiveKinds(t *testing.T) {
	sf := expectNoErrors(t, "open module com.acme.app {\n"+
		"  requires java.base;\n"+
		"  requires transitive java.sql;\n"+
		"  requires static lombok;\n"+
		"  exports com.acme.api;\n"+
		"  exports com.acme.internal to com.acme.app, com.acme.test;\n"+
		"  opens com.acme.impl;\n"+
		"  uses com.acme.spi.Service;\n"+
		"  provides com.acme.spi.Service with com.acme.impl.ServiceImpl;\n"+
		"}")
	mod := sf.ModuleDeclaration
	if mod == nil || mod.Kind != ModuleDeclaration {
		t.Fatalf("module declaration missing or wrong kind: %v", mod)
	}
	md := mod.AsModuleDeclaration()
	if !md.IsOpen {
		t.Error("module should be open")
	}
	want := []SyntaxKind{
		RequiresDirective, RequiresDirective, RequiresDirective,
		ExportsDirective, ExportsDirective, OpensDirective,
		UsesDirective, ProvidesDirective,
	}
	if md.Directives.Len() != len(want) {
		t.Fatalf("directives = %d, want %d", md.Directives.Len(), len(want))
	}
	for i, k := range want {
		if got := md.Directives.Nodes[i].Kind; got != k {
			t.Errorf("directive %d = %v, want %v", i, got, k)
		}
	}
}

func TestPlainModuleAndRequiresModifiers(t *testing.T) {
	sf := expectNoErrors(t, "module m { requires transitive a.b; }")
	md := sf.ModuleDeclaration.AsModuleDeclaration()
	if md.IsOpen {
		t.Error("module should not be open")
	}
	if !md.Directives.Nodes[0].AsRequiresDirective().IsTransitive {
		t.Error("requires should be transitive")
	}
}

func TestModuleAsIdentifier(t *testing.T) {
	sf := expectNoErrors(t, "class C { int module; }")
	if sf.ModuleDeclaration != nil {
		t.Error("'module' as a field name should not be a module declaration")
	}
	if sf.Statements.Len() != 1 {
		t.Errorf("statements = %d, want 1", sf.Statements.Len())
	}
}
