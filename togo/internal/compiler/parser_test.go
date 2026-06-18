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
