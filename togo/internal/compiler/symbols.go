package compiler

// Symbols (binder, M9). Port of the Symbol types in src/compiler/types.ts.

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
