package compiler

import (
	"sort"
	"testing"
)

// Port of src/compiler/program.test.ts.

func TestGetSourceFileParsesAndBinds(t *testing.T) {
	program := NewProgram()
	program.SetOpenDocument("file:///A.java", "class A {}", 1)
	sf := program.GetSourceFile("file:///A.java")
	if sf == nil {
		t.Fatal("source file should be defined")
	}
	if sf.Locals["A"].Flags != SymbolFlagsClass {
		t.Error("A should be a Class in the file scope")
	}
}

func TestResultCachedPerVersion(t *testing.T) {
	program := NewProgram()
	program.SetOpenDocument("file:///A.java", "class A {}", 1)
	first := program.GetSourceFile("file:///A.java")
	if program.GetSourceFile("file:///A.java") != first {
		t.Error("same version should return the cached source file")
	}
	program.SetOpenDocument("file:///A.java", "class B {}", 2)
	second := program.GetSourceFile("file:///A.java")
	if second == first {
		t.Error("a new version should rebuild")
	}
	if second.Locals["B"].Flags != SymbolFlagsClass {
		t.Error("B should be a Class after the change")
	}
}

func TestUnknownAndClosedDocuments(t *testing.T) {
	program := NewProgram()
	if program.GetSourceFile("file:///missing.java") != nil {
		t.Error("unknown document should be nil")
	}
	program.SetOpenDocument("file:///A.java", "class A {}", 1)
	if uris := program.GetOpenUris(); len(uris) != 1 || uris[0] != "file:///A.java" {
		t.Errorf("open uris = %v, want [file:///A.java]", uris)
	}
	program.CloseDocument("file:///A.java")
	if program.GetSourceFile("file:///A.java") != nil {
		t.Error("closed document should be nil")
	}
	if len(program.GetOpenUris()) != 0 {
		t.Error("open uris should be empty after close")
	}
}

func TestGlobalIndexResolvesAcrossFiles(t *testing.T) {
	program := NewProgram()
	program.SetOpenDocument("file:///A.java", "package com.app;\nclass A {}", 1)
	program.SetOpenDocument("file:///B.java", "package com.app;\ninterface B {}", 1)
	program.SetOpenDocument("file:///C.java", "class C {}", 1)
	index := program.GetGlobalIndex()

	if index.GetType("com.app.A").Flags != SymbolFlagsClass {
		t.Error("com.app.A should be a Class")
	}
	if index.GetType("com.app.B").Flags != SymbolFlagsInterface {
		t.Error("com.app.B should be an Interface")
	}
	if index.GetType("C").Flags != SymbolFlagsClass {
		t.Error("C should be a Class")
	}
	pkg := index.GetPackageTypes("com.app")
	keys := []string{}
	for k := range pkg {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) != 2 || keys[0] != "A" || keys[1] != "B" {
		t.Errorf("package types = %v, want [A B]", keys)
	}
	if index.GetPackageSymbol("com.app").Flags != SymbolFlagsPackage {
		t.Error("com.app should be a Package symbol")
	}
	if index.GetType("com.app.A").Parent != index.GetPackageSymbol("com.app") {
		t.Error("top-level type parent should be its package symbol")
	}
}

func TestIndexRebuildsOnChange(t *testing.T) {
	program := NewProgram()
	program.SetOpenDocument("file:///A.java", "package p;\nclass A {}", 1)
	if program.GetGlobalIndex().GetType("p.A") == nil {
		t.Error("p.A should be defined")
	}
	program.SetOpenDocument("file:///A.java", "package p;\nclass Renamed {}", 2)
	index := program.GetGlobalIndex()
	if index.GetType("p.A") != nil {
		t.Error("p.A should be gone after rename")
	}
	if index.GetType("p.Renamed") == nil {
		t.Error("p.Renamed should be defined")
	}
}

func TestChangingOneFileRebindsOnlyIt(t *testing.T) {
	program := NewProgram()
	program.SetOpenDocument("file:///A.java", "class A {}", 1)
	program.SetOpenDocument("file:///B.java", "class B {}", 1)
	program.GetGlobalIndex()
	aBefore := program.GetSourceFile("file:///A.java")

	program.SetOpenDocument("file:///B.java", "class B2 {}", 2)
	index := program.GetGlobalIndex()
	if index.GetType("B") != nil {
		t.Error("B should be gone")
	}
	if index.GetType("B2") == nil {
		t.Error("B2 should be defined")
	}
	if program.GetSourceFile("file:///A.java") != aBefore {
		t.Error("untouched file should keep its bound source file")
	}
	if index.GetType("A") == nil {
		t.Error("A should still be defined")
	}
}

func TestClosingRemovesOnlyItsTypes(t *testing.T) {
	program := NewProgram()
	program.AddProjectFile("file:///A.java", "package p;\nclass A {}")
	program.SetOpenDocument("file:///B.java", "package p;\nclass B {}", 1)
	if program.GetGlobalIndex().GetType("p.B") == nil {
		t.Error("p.B should be defined")
	}
	program.CloseDocument("file:///B.java")
	index := program.GetGlobalIndex()
	if index.GetType("p.B") != nil {
		t.Error("p.B should be gone after close")
	}
	if index.GetType("p.A") == nil {
		t.Error("p.A should survive")
	}
}

func TestRemovingProjectFileDropsTypes(t *testing.T) {
	program := NewProgram()
	program.AddProjectFile("file:///A.java", "package p;\nclass A {}")
	program.AddProjectFile("file:///B.java", "package p;\nclass B {}")
	if program.GetGlobalIndex().GetType("p.A") == nil {
		t.Error("p.A should be defined")
	}
	program.RemoveProjectFile("file:///A.java")
	index := program.GetGlobalIndex()
	if index.GetType("p.A") != nil {
		t.Error("p.A should be gone")
	}
	if index.GetType("p.B") == nil {
		t.Error("p.B should survive")
	}
	if program.GetSourceFile("file:///A.java") != nil {
		t.Error("removed project file should not resolve")
	}
	program.SetOpenDocument("file:///B.java", "package p;\nclass B { int x; }", 2)
	program.RemoveProjectFile("file:///B.java")
	if program.GetGlobalIndex().GetType("p.B") == nil {
		t.Error("an open document for a removed project file keeps resolving")
	}
}
