package mcp

import (
	"testing"

	"github.com/nikeee/cappu/internal/compiler"
)

// Port of src/services/mcpResolve.test.ts.

func indexFor(files map[string]string) *compiler.GlobalIndex {
	program := compiler.NewProgram()
	for uri, text := range files {
		program.AddProjectFile(compiler.URI(uri), text)
	}
	return program.GetGlobalIndex()
}

func TestResolveFullyQualifiedType(t *testing.T) {
	index := indexFor(map[string]string{"file:///Foo.java": "package a; class Foo {}"})
	syms := ResolveSymbolRef("a.Foo", index)
	if len(syms) != 1 || syms[0].EscapedName != "Foo" || syms[0].Flags&compiler.SymbolFlagsClass == 0 {
		t.Errorf("got %+v", syms)
	}
}

func TestResolveBareSimpleType(t *testing.T) {
	index := indexFor(map[string]string{"file:///Foo.java": "package a; class Foo {}"})
	syms := ResolveSymbolRef("Foo", index)
	if len(syms) != 1 || syms[0].EscapedName != "Foo" {
		t.Errorf("got %+v", syms)
	}
}

func TestResolveAmbiguousSimpleName(t *testing.T) {
	index := indexFor(map[string]string{
		"file:///a/Foo.java": "package a; class Foo {}",
		"file:///b/Foo.java": "package b; class Foo {}",
	})
	if syms := ResolveSymbolRef("Foo", index); len(syms) != 2 {
		t.Errorf("got %d candidates, want 2", len(syms))
	}
}

func TestResolveMember(t *testing.T) {
	index := indexFor(map[string]string{"file:///Foo.java": "package a; class Foo { int bar() { return 0; } }"})
	syms := ResolveSymbolRef("a.Foo#bar", index)
	if len(syms) != 1 || syms[0].EscapedName != "bar" || syms[0].Flags&compiler.SymbolFlagsMethod == 0 {
		t.Errorf("got %+v", syms)
	}
}

func TestResolveUnknownRef(t *testing.T) {
	index := indexFor(map[string]string{"file:///Foo.java": "package a; class Foo {}"})
	if got := ResolveSymbolRef("a.Nope", index); len(got) != 0 {
		t.Errorf("a.Nope -> %+v, want empty", got)
	}
	if got := ResolveSymbolRef("a.Foo#nope", index); len(got) != 0 {
		t.Errorf("a.Foo#nope -> %+v, want empty", got)
	}
}
