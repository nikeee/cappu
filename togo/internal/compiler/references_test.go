package compiler

import (
	"strings"
	"testing"
)

// Port of src/services/references.test.ts (exercises FindReferences with the
// checker's member-aware ResolveName).

func refSetup(files map[URI]string) (*Program, *Checker) {
	program := NewProgram()
	for uri, text := range files {
		program.SetOpenDocument(uri, text, 1)
	}
	return program, NewChecker(program)
}

func refSymbolAt(program *Program, checker *Checker, uri URI, needle string, occ int) *Symbol {
	sf := program.GetSourceFile(uri)
	text := sf.AsSourceFile().Text
	offset := -1
	for i := 0; i < occ; i++ {
		offset = strings.Index(text[offset+1:], needle) + offset + 1
	}
	return checker.ResolveName(GetIdentifierAtPosition(sf, offset))
}

var twoFields = map[URI]string{
	"file:///A.java": "class A { int value; }",
	"file:///B.java": "class B { int value; void m(A a) { int x = a.value; int y = value; } }",
}

func TestFindReferencesWithCheckerMatchesMemberAccess(t *testing.T) {
	program, checker := refSetup(twoFields)
	aValue := refSymbolAt(program, checker, "file:///A.java", "value", 1)
	refs := FindReferences(aValue, program, checker.ResolveName)
	if len(refs) != 2 {
		t.Errorf("references = %d, want 2 (declaration + a.value)", len(refs))
	}
}

func TestDefaultLexicalResolverMissesMemberAccess(t *testing.T) {
	program, checker := refSetup(twoFields)
	aValue := refSymbolAt(program, checker, "file:///A.java", "value", 1)
	if n := len(FindReferences(aValue, program, nil)); n != 1 {
		t.Errorf("references = %d, want 1 (declaration only)", n)
	}
}

func TestRenameBFieldNotMemberAccess(t *testing.T) {
	program, checker := refSetup(twoFields)
	bValue := refSymbolAt(program, checker, "file:///B.java", "value", 1)
	if n := len(FindReferences(bValue, program, checker.ResolveName)); n != 2 {
		t.Errorf("references = %d, want 2 (declaration + bare use)", n)
	}
}

func TestRenameLocalEveryUse(t *testing.T) {
	program, checker := refSetup(map[URI]string{
		"file:///C.java": "class C { void m() { int count = 1; count = count + 1; use(count); } }",
	})
	local := refSymbolAt(program, checker, "file:///C.java", "count", 1)
	if n := len(FindReferences(local, program, checker.ResolveName)); n != 4 {
		t.Errorf("references = %d, want 4", n)
	}
}

func TestRenameParameterWithinMethod(t *testing.T) {
	program, checker := refSetup(map[URI]string{
		"file:///P.java": "class P { int f(int amount) { return amount + amount; } int g(int amount) { return amount; } }",
	})
	param := refSymbolAt(program, checker, "file:///P.java", "amount", 1)
	if n := len(FindReferences(param, program, checker.ResolveName)); n != 3 {
		t.Errorf("references = %d, want 3", n)
	}
}

func TestRenameMethodQualifiedCall(t *testing.T) {
	program, checker := refSetup(map[URI]string{
		"file:///D.java": "class D { void run() {} void m(D d) { d.run(); } }",
	})
	run := refSymbolAt(program, checker, "file:///D.java", "run", 1)
	if n := len(FindReferences(run, program, checker.ResolveName)); n != 2 {
		t.Errorf("references = %d, want 2", n)
	}
}

func renameEdits(program *Program, checker *Checker, uri URI, needle string, occ int) map[string]int {
	symbol := refSymbolAt(program, checker, uri, needle, occ)
	perFile := map[string]int{}
	for _, node := range FindReferences(symbol, program, checker.ResolveName) {
		file := GetSourceFileOfNode(node).AsSourceFile().FileName
		perFile[file]++
	}
	return perFile
}

func TestRenameClassCrossFile(t *testing.T) {
	program, checker := refSetup(map[URI]string{
		"file:///P.java":    "class P { }",
		"file:///UseA.java": "class UseA { P p = new P(); P make() { return new P(); } }",
		"file:///UseB.java": "class UseB extends P { }",
	})
	edits := renameEdits(program, checker, "file:///P.java", "P", 1)
	if edits["file:///P.java"] != 1 || edits["file:///UseA.java"] != 4 || edits["file:///UseB.java"] != 1 {
		t.Errorf("edits = %v, want P=1 UseA=4 UseB=1", edits)
	}
}

func TestRenameMethodSpansFiles(t *testing.T) {
	program, checker := refSetup(map[URI]string{
		"file:///S.java": "class S { void go() { } }",
		"file:///T.java": "class T { void go() { } void m(S s) { s.go(); go(); } }",
	})
	edits := renameEdits(program, checker, "file:///S.java", "go", 1)
	if edits["file:///S.java"] != 1 || edits["file:///T.java"] != 1 {
		t.Errorf("edits = %v, want S=1 T=1", edits)
	}
}

func TestRenameFieldThroughThisAndReceiver(t *testing.T) {
	program, checker := refSetup(map[URI]string{
		"file:///H.java": "class H { int count; int bump() { return this.count + count; } }",
		"file:///K.java": "class K { int read(H h) { return h.count; } }",
	})
	edits := renameEdits(program, checker, "file:///H.java", "count", 1)
	if edits["file:///H.java"] != 3 || edits["file:///K.java"] != 1 {
		t.Errorf("edits = %v, want H=3 K=1", edits)
	}
}
