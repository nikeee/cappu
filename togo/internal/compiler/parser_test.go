package compiler

import "testing"

func expectNoErrors(t *testing.T, text string) *SourceFileData {
	t.Helper()
	sf := parse(text)
	d := sf.AsSourceFile()
	if len(d.ParseDiagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", d.ParseDiagnostics)
	}
	return d
}

// Port of the parser-core cases in src/compiler/parser.test.ts (the
// compilation-unit skeleton: empty statements, recovery, positions,
// forEachChild). Grammar-level cases are added as the grammar is ported.

func parse(text string) *Node { return ParseSourceFile("Test.java", text) }

func TestEmptySourceFile(t *testing.T) {
	sf := parse("")
	if sf.Kind != SourceFile {
		t.Fatalf("kind = %v", sf.Kind)
	}
	d := sf.AsSourceFile()
	if d.Statements.Len() != 0 {
		t.Errorf("statements = %d, want 0", d.Statements.Len())
	}
	if len(d.ParseDiagnostics) != 0 {
		t.Errorf("diagnostics = %v", d.ParseDiagnostics)
	}
	if d.EndOfFileToken.Kind != EndOfFileToken {
		t.Errorf("eof kind = %v", d.EndOfFileToken.Kind)
	}
}

func TestWhitespaceAndCommentsOnly(t *testing.T) {
	d := parse("  // hello\n  /* block */\n").AsSourceFile()
	if d.Statements.Len() != 0 || len(d.ParseDiagnostics) != 0 {
		t.Errorf("statements=%d diagnostics=%d", d.Statements.Len(), len(d.ParseDiagnostics))
	}
}

func TestEmptyStatementsParsed(t *testing.T) {
	d := parse(";;;").AsSourceFile()
	if d.Statements.Len() != 3 {
		t.Fatalf("statements = %d, want 3", d.Statements.Len())
	}
	for _, s := range d.Statements.Nodes {
		if s.Kind != EmptyStatement {
			t.Errorf("statement kind = %v", s.Kind)
		}
	}
	if len(d.ParseDiagnostics) != 0 {
		t.Errorf("diagnostics = %v", d.ParseDiagnostics)
	}
}

func TestGarbageRecovered(t *testing.T) {
	d := parse("foo").AsSourceFile()
	if d.Statements.Len() != 0 {
		t.Errorf("statements = %d, want 0", d.Statements.Len())
	}
	if len(d.ParseDiagnostics) < 1 {
		t.Error("expected at least one diagnostic")
	}
	// The error happened right before the EOF token finished, so it carries the
	// ThisNodeHasError flag (exercises finishNode's error stamping).
	if d.EndOfFileToken.Flags&NodeFlagThisNodeHasError == 0 {
		t.Error("EOF token should carry ThisNodeHasError")
	}
}

func TestGarbageInterleavedRecovers(t *testing.T) {
	d := parse("; bar ;").AsSourceFile()
	if d.Statements.Len() != 2 {
		t.Fatalf("statements = %d, want 2", d.Statements.Len())
	}
	for _, s := range d.Statements.Nodes {
		if s.Kind != EmptyStatement {
			t.Errorf("statement kind = %v", s.Kind)
		}
	}
	if len(d.ParseDiagnostics) < 1 {
		t.Error("expected at least one diagnostic")
	}
}

func TestLongGarbageTerminates(t *testing.T) {
	src := ""
	for i := 0; i < 20; i++ {
		src += "@ @ @ # # # < > < > & & |"
	}
	d := parse(src).AsSourceFile()
	if d.EndOfFileToken.Kind != EndOfFileToken {
		t.Error("expected to terminate at EOF")
	}
	if len(d.ParseDiagnostics) < 1 {
		t.Error("expected at least one diagnostic")
	}
}

func TestNodePositionsOrdered(t *testing.T) {
	sf := parse("  ;  ;")
	if sf.Pos != 0 || sf.End != 6 {
		t.Errorf("source file range = [%d,%d], want [0,6]", sf.Pos, sf.End)
	}
	stmts := sf.AsSourceFile().Statements.Nodes
	if stmts[0].End > stmts[1].Pos {
		t.Errorf("statement order: a.End=%d > b.Pos=%d", stmts[0].End, stmts[1].Pos)
	}
}

func TestForEachChildVisitsStatementsThenEOF(t *testing.T) {
	sf := parse(";;")
	var visited []SyntaxKind
	sf.ForEachChild(func(n *Node) bool {
		visited = append(visited, n.Kind)
		return false
	})
	want := []SyntaxKind{EmptyStatement, EmptyStatement, EndOfFileToken}
	if len(visited) != len(want) {
		t.Fatalf("visited = %v, want %v", visited, want)
	}
	for i := range want {
		if visited[i] != want[i] {
			t.Errorf("visited = %v, want %v", visited, want)
		}
	}
}

func TestPackageDeclarationQualifiedName(t *testing.T) {
	d := expectNoErrors(t, "package com.example.app;")
	if d.PackageDeclaration == nil || d.PackageDeclaration.Kind != PackageDeclaration {
		t.Fatalf("package declaration = %v", d.PackageDeclaration)
	}
	name := d.PackageDeclaration.AsPackageDeclaration().Name
	if name.Kind != QualifiedName {
		t.Fatalf("name kind = %v", name.Kind)
	}
	if got := name.AsQualifiedName().Right.AsIdentifier().Text; got != "app" {
		t.Errorf("rightmost name = %q, want app", got)
	}
}

func TestImportDeclarations(t *testing.T) {
	d := expectNoErrors(t, "import java.util.List;\nimport static org.Assert.assertTrue;\nimport java.util.*;")
	if d.Imports.Len() != 3 {
		t.Fatalf("imports = %d, want 3", d.Imports.Len())
	}
	i0 := d.Imports.Nodes[0].AsImportDeclaration()
	i1 := d.Imports.Nodes[1].AsImportDeclaration()
	i2 := d.Imports.Nodes[2].AsImportDeclaration()
	if i0.IsStatic || i0.IsOnDemand {
		t.Errorf("import 0 = %+v, want plain", i0)
	}
	if !i1.IsStatic {
		t.Error("import 1 should be static")
	}
	if !i2.IsOnDemand {
		t.Error("import 2 should be on-demand")
	}
}

// extendsType parses `class C extends <typeText> {}` and returns the extends
// type plus the diagnostic count (mirrors the TS helper).
func extendsType(text string) (*Node, int) {
	sf := parse("class C extends " + text + " {}")
	cls := sf.AsSourceFile().Statements.Nodes[0]
	return cls.AsClassDeclaration().ExtendsType, len(sf.AsSourceFile().ParseDiagnostics)
}

func contains(ks []SyntaxKind, want SyntaxKind) bool {
	for _, k := range ks {
		if k == want {
			return true
		}
	}
	return false
}

func TestClassHeader(t *testing.T) {
	d := expectNoErrors(t, "public final class Foo<T extends Number, U> extends Bar implements A, B {}")
	cls := d.Statements.Nodes[0]
	if cls.Kind != ClassDeclaration {
		t.Fatalf("kind = %v", cls.Kind)
	}
	c := cls.AsClassDeclaration()
	if c.Name.AsIdentifier().Text != "Foo" {
		t.Errorf("name = %q", c.Name.AsIdentifier().Text)
	}
	if c.Modifiers.Len() != 2 {
		t.Errorf("modifiers = %d, want 2", c.Modifiers.Len())
	}
	if c.TypeParameters.Len() != 2 {
		t.Errorf("typeParameters = %d, want 2", c.TypeParameters.Len())
	}
	if c.ExtendsType == nil || c.ExtendsType.Kind != TypeReference {
		t.Errorf("extendsType = %v", c.ExtendsType)
	}
	if c.ImplementsTypes.Len() != 2 {
		t.Errorf("implementsTypes = %d, want 2", c.ImplementsTypes.Len())
	}
}

func TestInterfaceExtendsList(t *testing.T) {
	d := expectNoErrors(t, "interface I extends A, B, C {}")
	iface := d.Statements.Nodes[0]
	if iface.Kind != InterfaceDeclaration {
		t.Fatalf("kind = %v", iface.Kind)
	}
	if iface.AsInterfaceDeclaration().ExtendsTypes.Len() != 3 {
		t.Errorf("extendsTypes = %d, want 3", iface.AsInterfaceDeclaration().ExtendsTypes.Len())
	}
}

func TestEnumAndAnnotationType(t *testing.T) {
	if k := expectNoErrors(t, "enum Color implements Paintable {}").Statements.Nodes[0].Kind; k != EnumDeclaration {
		t.Errorf("enum kind = %v", k)
	}
	if k := expectNoErrors(t, "public @interface Marker {}").Statements.Nodes[0].Kind; k != AnnotationTypeDeclaration {
		t.Errorf("@interface kind = %v", k)
	}
}

func TestAnnotationAsModifier(t *testing.T) {
	d := expectNoErrors(t, "@Deprecated @SuppressWarnings public class C {}")
	mods := d.Statements.Nodes[0].AsClassDeclaration().Modifiers
	if mods.Len() != 3 {
		t.Fatalf("modifiers = %d, want 3", mods.Len())
	}
	if mods.Nodes[0].Kind != Annotation {
		t.Errorf("modifier 0 kind = %v, want Annotation", mods.Nodes[0].Kind)
	}
	if mods.Nodes[2].Kind != PublicKeyword {
		t.Errorf("modifier 2 kind = %v, want PublicKeyword", mods.Nodes[2].Kind)
	}
}

func TestNestedGenericsHeritage(t *testing.T) {
	expectNoErrors(t, "class C extends java.util.HashMap<String, java.util.List<Integer>> {}")
}

func TestMultipleTopLevelDeclarations(t *testing.T) {
	d := expectNoErrors(t, "class A {} interface B {} enum C {}")
	got := []SyntaxKind{d.Statements.Nodes[0].Kind, d.Statements.Nodes[1].Kind, d.Statements.Nodes[2].Kind}
	want := []SyntaxKind{ClassDeclaration, InterfaceDeclaration, EnumDeclaration}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("kinds = %v, want %v", got, want)
		}
	}
}

func TestPackageImportsClass(t *testing.T) {
	d := expectNoErrors(t, "package p;\nimport java.util.List;\npublic class Main {}")
	if d.PackageDeclaration == nil || d.Imports.Len() != 1 || d.Statements.Len() != 1 {
		t.Errorf("pkg=%v imports=%d statements=%d", d.PackageDeclaration, d.Imports.Len(), d.Statements.Len())
	}
}

func TestForEachChildClassHeader(t *testing.T) {
	cls := parse("class Foo<T> extends Bar {}").AsSourceFile().Statements.Nodes[0]
	var kinds []SyntaxKind
	cls.ForEachChild(func(n *Node) bool {
		kinds = append(kinds, n.Kind)
		return false
	})
	for _, want := range []SyntaxKind{Identifier, TypeParameter, TypeReference} {
		if !contains(kinds, want) {
			t.Errorf("class header children %v missing %v", kinds, want)
		}
	}
}

func TestSimpleAndQualifiedTypeRefs(t *testing.T) {
	simple, errs := extendsType("Foo")
	if errs != 0 || simple.Kind != TypeReference {
		t.Fatalf("simple = %v, errs %d", simple, errs)
	}
	if simple.AsTypeReference().TypeName.AsIdentifier().Text != "Foo" {
		t.Error("simple type name != Foo")
	}
	qualified, _ := extendsType("java.util.List")
	if qualified.AsTypeReference().TypeName.Kind != QualifiedName {
		t.Error("qualified type name should be a QualifiedName")
	}
}

func TestTypeArguments(t *testing.T) {
	one, _ := extendsType("List<String>")
	if one.AsTypeReference().TypeArguments.Len() != 1 {
		t.Error("List<String> should have 1 type arg")
	}
	two, _ := extendsType("Map<K, V>")
	if two.AsTypeReference().TypeArguments.Len() != 2 {
		t.Error("Map<K, V> should have 2 type args")
	}
}

func TestDeepNestedGenerics(t *testing.T) {
	typ, errs := extendsType("A<B<C<D>>>")
	if errs != 0 {
		t.Fatalf("errors = %d", errs)
	}
	a := typ.AsTypeReference()
	b := a.TypeArguments.Nodes[0].AsTypeReference()
	c := b.TypeArguments.Nodes[0].AsTypeReference()
	dd := c.TypeArguments.Nodes[0].AsTypeReference()
	if dd.TypeName.AsIdentifier().Text != "D" {
		t.Errorf("innermost = %q, want D", dd.TypeName.AsIdentifier().Text)
	}
}

func TestWildcards(t *testing.T) {
	typ, errs := extendsType("Map<? extends Number, ? super Integer>")
	if errs != 0 {
		t.Fatalf("errors = %d", errs)
	}
	args := typ.AsTypeReference().TypeArguments
	if !args.Nodes[0].AsWildcardType().HasExtends {
		t.Error("arg 0 should have extends bound")
	}
	if !args.Nodes[1].AsWildcardType().HasSuper {
		t.Error("arg 1 should have super bound")
	}
	unbounded, _ := extendsType("List<?>")
	w := unbounded.AsTypeReference().TypeArguments.Nodes[0]
	if w.Kind != WildcardType || w.AsWildcardType().HasExtends || w.AsWildcardType().HasSuper {
		t.Errorf("unbounded wildcard = %+v", w.AsWildcardType())
	}
}

func TestDiamond(t *testing.T) {
	typ, errs := extendsType("List<>")
	if errs != 0 {
		t.Fatalf("errors = %d", errs)
	}
	if typ.AsTypeReference().TypeArguments.Len() != 0 {
		t.Error("diamond should yield an empty type-argument list")
	}
}

func TestArrayTypes(t *testing.T) {
	typ, errs := extendsType("int[][]")
	if errs != 0 || typ.Kind != ArrayType {
		t.Fatalf("type = %v errs %d", typ.Kind, errs)
	}
	inner := typ.AsArrayType().ElementType
	if inner.Kind != ArrayType {
		t.Fatalf("inner kind = %v", inner.Kind)
	}
	if inner.AsArrayType().ElementType.AsPrimitiveType().Keyword != IntKeyword {
		t.Error("innermost element should be int")
	}
}

func TestArrayInTypeArgument(t *testing.T) {
	typ, errs := extendsType("List<int[]>")
	if errs != 0 {
		t.Fatalf("errors = %d", errs)
	}
	if typ.AsTypeReference().TypeArguments.Nodes[0].Kind != ArrayType {
		t.Error("type argument should be an array type")
	}
}

func TestTypeParameterBounds(t *testing.T) {
	d := expectNoErrors(t, "class C<T extends A & B & java.io.Serializable> {}")
	tp := d.Statements.Nodes[0].AsClassDeclaration().TypeParameters.Nodes[0]
	if tp.AsTypeParameter().Constraint.Len() != 3 {
		t.Errorf("bounds = %d, want 3", tp.AsTypeParameter().Constraint.Len())
	}
}

func TestForEachChildShortCircuits(t *testing.T) {
	sf := parse(";;")
	var first *Node
	sf.ForEachChild(func(n *Node) bool {
		if n.Kind == EmptyStatement {
			first = n
			return true
		}
		return false
	})
	if first != sf.AsSourceFile().Statements.Nodes[0] {
		t.Error("short-circuit did not return the first empty statement")
	}
}
