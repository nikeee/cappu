package compiler

// Symbols (binder, M9). Port of the Symbol types in src/compiler/types.ts.

import (
	"cmp"
	"slices"
)

type SymbolFlags int

const (
	SymbolFlagsNone          SymbolFlags = 0
	SymbolFlagsPackage       SymbolFlags = 1 << 0
	SymbolFlagsClass         SymbolFlags = 1 << 1
	SymbolFlagsInterface     SymbolFlags = 1 << 2
	SymbolFlagsEnum          SymbolFlags = 1 << 3
	SymbolFlagsAnnotation    SymbolFlags = 1 << 4
	SymbolFlagsRecord        SymbolFlags = 1 << 5
	SymbolFlagsMethod        SymbolFlags = 1 << 6
	SymbolFlagsConstructor   SymbolFlags = 1 << 7
	SymbolFlagsField         SymbolFlags = 1 << 8
	SymbolFlagsEnumConstant  SymbolFlags = 1 << 9
	SymbolFlagsParameter     SymbolFlags = 1 << 10
	SymbolFlagsTypeParameter SymbolFlags = 1 << 11
	SymbolFlagsLocalVariable SymbolFlags = 1 << 12
	SymbolFlagsModule        SymbolFlags = 1 << 13

	SymbolFlagsType = SymbolFlagsClass | SymbolFlagsInterface | SymbolFlagsEnum |
		SymbolFlagsAnnotation | SymbolFlagsRecord | SymbolFlagsTypeParameter
)

// SymbolTable maps a declared name to its symbol.
type SymbolTable map[string]*Symbol

// Symbol is a named declaration (or set of merged declarations).
type Symbol struct {
	Flags        SymbolFlags
	EscapedName  string
	Declarations []*Node
	Members      SymbolTable
	// Parent is the enclosing symbol: member -> type -> package.
	Parent *Symbol
	// ValueDeclaration is the first declaration (for hover/goto).
	ValueDeclaration *Node
}

// symbolDeclPos is a symbol's first declaration offset, used to recover source
// declaration order. Symbols with no declaration sort last (a large sentinel).
func symbolDeclPos(s *Symbol) int {
	if s.ValueDeclaration != nil {
		return s.ValueDeclaration.Pos
	}
	if len(s.Declarations) > 0 {
		return s.Declarations[0].Pos
	}
	return 1 << 62
}

// OrderedKeys returns the table's keys in a deterministic order: by each
// symbol's first declaration position, then name. The TS build stores a
// SymbolTable as an insertion-ordered Map, so iterating it yields declaration
// order; a Go map randomizes iteration, so any order-sensitive consumer (e.g.
// completion lists) must go through this to match the TS output and stay stable
// run-to-run.
func (t SymbolTable) OrderedKeys() []string {
	keys := make([]string, 0, len(t))
	for k := range t {
		keys = append(keys, k)
	}
	slices.SortStableFunc(keys, func(a, b string) int {
		if c := cmp.Compare(symbolDeclPos(t[a]), symbolDeclPos(t[b])); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	return keys
}
