package services

import (
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/compiler"
	"github.com/nikeee/cappu/internal/lsp"
)

// Port of src/services/typeHierarchy.test.ts.

const thSrc = "interface Shape {}\n" +
	"class Base implements Shape {}\n" +
	"class Mid extends Base {}\n" +
	"class Leaf extends Mid {}"

func thSetup(text string) (*compiler.Program, *compiler.Checker, *compiler.Node) {
	program := compiler.NewProgram()
	compiler.LoadJdkStub(program)
	program.SetOpenDocument("file:///S.java", text, 1)
	return program, compiler.NewChecker(program), program.GetSourceFile("file:///S.java")
}

func itemNames(items []lsp.TypeHierarchyItem) []string {
	out := []string{}
	for _, i := range items {
		out = append(out, i.Name)
	}
	return out
}

func TestPrepareTypeHierarchy(t *testing.T) {
	_, checker, sf := thSetup(thSrc)
	items := PrepareTypeHierarchy(checker, sf, strings.Index(thSrc, "Mid extends"))
	if got := itemNames(items); len(got) != 1 || got[0] != "Mid" {
		t.Fatalf("prepare = %v, want [Mid]", got)
	}
}

func TestTypeHierarchyNeighbours(t *testing.T) {
	program, checker, sf := thSetup(thSrc)
	mid := PrepareTypeHierarchy(checker, sf, strings.Index(thSrc, "Mid extends"))[0]
	if got := itemNames(TypeHierarchySupertypes(program, checker, mid)); len(got) != 1 || got[0] != "Base" {
		t.Errorf("supertypes = %v, want [Base]", got)
	}
	if got := itemNames(TypeHierarchySubtypes(program, checker, mid)); len(got) != 1 || got[0] != "Leaf" {
		t.Errorf("subtypes = %v, want [Leaf]", got)
	}
}

func TestTypeHierarchySupertypesIncludesInterface(t *testing.T) {
	program, checker, sf := thSetup(thSrc)
	base := PrepareTypeHierarchy(checker, sf, strings.Index(thSrc, "Base implements"))[0]
	found := false
	for _, n := range itemNames(TypeHierarchySupertypes(program, checker, base)) {
		if n == "Shape" {
			found = true
		}
	}
	if !found {
		t.Error("Base's supertypes should include Shape")
	}
}

func TestPrepareTypeHierarchyNotAType(t *testing.T) {
	_, checker, sf := thSetup(thSrc)
	if items := PrepareTypeHierarchy(checker, sf, 0); len(items) != 0 {
		t.Errorf("prepare at non-type = %v, want empty", itemNames(items))
	}
}
