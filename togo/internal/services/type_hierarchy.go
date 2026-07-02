package services

// Type hierarchy (supertypes / subtypes) as pure functions over the program -
// the transport-free half, kept separate from the lspserver layer for testing.
// Port of src/services/typeHierarchy.ts. Supertypes/subtypes re-resolve the type
// symbol from the item's SelectionRange position, so no opaque data survives the
// round-trip.

import (
	"github.com/nikeee/cappu/internal/compiler"
	"github.com/nikeee/cappu/internal/lsp"
)

func symbolKindOf(flags compiler.SymbolFlags) lsp.SymbolKind {
	switch {
	case flags&(compiler.SymbolFlagsInterface|compiler.SymbolFlagsAnnotation) != 0:
		return lsp.SymbolKindInterface
	case flags&compiler.SymbolFlagsEnum != 0:
		return lsp.SymbolKindEnum
	default:
		return lsp.SymbolKindClass // class and record
	}
}

// typeHierarchyItemOf builds an item for a type symbol; ok is false when it has
// no source declaration (e.g. a JDK-stub type).
func typeHierarchyItemOf(symbol *compiler.Symbol) (lsp.TypeHierarchyItem, bool) {
	nameNode := compiler.GetDeclarationNameNode(symbol)
	declaration := symbolDeclaration(symbol)
	if nameNode == nil || declaration == nil {
		return lsp.TypeHierarchyItem{}, false
	}
	file := compiler.GetSourceFileOfNode(declaration)
	text := file.AsSourceFile().Text
	lineStarts := file.AsSourceFile().LineStarts()
	name := "<anonymous>"
	if nameNode.Kind == compiler.Identifier {
		name = nameNode.AsIdentifier().Text
	}
	return lsp.TypeHierarchyItem{
		Name:           name,
		Kind:           symbolKindOf(symbol.Flags),
		URI:            file.AsSourceFile().FileName,
		Range:          dsRange(text, lineStarts, compiler.SkipTrivia(text, declaration.Pos), declaration.End),
		SelectionRange: dsRange(text, lineStarts, compiler.SkipTrivia(text, nameNode.Pos), nameNode.End),
	}, true
}

// typeSymbolOfItem re-resolves the type an item points at, via the identifier at
// its SelectionRange start.
func typeSymbolOfItem(program *compiler.Program, checker *compiler.Checker, item lsp.TypeHierarchyItem) *compiler.Symbol {
	sourceFile := program.GetSourceFile(compiler.URI(item.URI))
	if sourceFile == nil {
		return nil
	}
	text := sourceFile.AsSourceFile().Text
	lineStarts := sourceFile.AsSourceFile().LineStarts()
	offset := compiler.GetPositionOfLineAndCharacter(text, lineStarts, item.SelectionRange.Start.Line, item.SelectionRange.Start.Character)
	id := compiler.GetIdentifierAtPosition(sourceFile, offset)
	if id == nil {
		return nil
	}
	symbol := checker.ResolveName(id)
	if symbol == nil || symbol.Flags&typeFlags == 0 {
		return nil
	}
	return symbol
}

// PrepareTypeHierarchy returns the type at a position as a single-element list,
// or nil when the cursor is not on a type.
func PrepareTypeHierarchy(checker *compiler.Checker, sourceFile *compiler.Node, offset int) []lsp.TypeHierarchyItem {
	id := compiler.GetIdentifierAtPosition(sourceFile, offset)
	if id == nil {
		return nil
	}
	symbol := checker.ResolveName(id)
	if symbol == nil || symbol.Flags&typeFlags == 0 {
		return nil
	}
	if item, ok := typeHierarchyItemOf(symbol); ok {
		return []lsp.TypeHierarchyItem{item}
	}
	return nil
}

// TypeHierarchySupertypes returns the direct supertypes (extends + implements).
func TypeHierarchySupertypes(program *compiler.Program, checker *compiler.Checker, item lsp.TypeHierarchyItem) []lsp.TypeHierarchyItem {
	symbol := typeSymbolOfItem(program, checker, item)
	if symbol == nil {
		return nil
	}
	var out []lsp.TypeHierarchyItem
	for _, s := range compiler.GetDirectSuperTypeSymbols(symbol, program) {
		if it, ok := typeHierarchyItemOf(s); ok {
			out = append(out, it)
		}
	}
	return out
}

// TypeHierarchySubtypes returns the direct subtypes (declarations whose
// extends/implements names this type).
func TypeHierarchySubtypes(program *compiler.Program, checker *compiler.Checker, item lsp.TypeHierarchyItem) []lsp.TypeHierarchyItem {
	symbol := typeSymbolOfItem(program, checker, item)
	if symbol == nil {
		return nil
	}
	var out []lsp.TypeHierarchyItem
	for _, s := range GetSubtypeIndex(program).DirectSubtypesOf(symbol) {
		if it, ok := typeHierarchyItemOf(s); ok {
			out = append(out, it)
		}
	}
	return out
}
