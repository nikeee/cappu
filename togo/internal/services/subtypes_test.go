package services

import (
	"sort"
	"testing"

	"github.com/nikeee/cappu/internal/compiler"
)

// Port of src/services/subtypes.test.ts.

func names(syms []*compiler.Symbol) []string {
	out := []string{}
	for _, s := range syms {
		out = append(out, s.EscapedName)
	}
	return out
}

func TestSubtypeIndexAcrossFiles(t *testing.T) {
	program := compiler.NewProgram()
	compiler.LoadJdkStub(program)
	program.AddProjectFile("file:///I.java", "interface I {}")
	program.AddProjectFile("file:///A.java", "class A implements I {}")
	program.AddProjectFile("file:///B.java", "class B extends A {}")

	i := program.GetGlobalIndex().GetType("I")
	a := program.GetGlobalIndex().GetType("A")
	index := GetSubtypeIndex(program)

	if got := names(index.DirectSubtypesOf(i)); len(got) != 1 || got[0] != "A" {
		t.Errorf("direct subtypes of I = %v, want [A]", got)
	}
	all := names(index.AllSubtypesOf(i))
	sort.Strings(all)
	if len(all) != 2 || all[0] != "A" || all[1] != "B" {
		t.Errorf("all subtypes of I = %v, want [A B]", all)
	}
	if got := names(index.DirectSubtypesOf(a)); len(got) != 1 || got[0] != "B" {
		t.Errorf("direct subtypes of A = %v, want [B]", got)
	}

	program.AddProjectFile("file:///C.java", "class C implements I {}")
	index = GetSubtypeIndex(program)
	if got := index.AllSubtypesOf(program.GetGlobalIndex().GetType("I")); len(got) != 3 {
		t.Errorf("all subtypes of I after change = %d, want 3", len(got))
	}
}
