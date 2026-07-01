package services

// Builds an LSP DocumentSymbol tree (outline) from a parsed SourceFile. Kept
// separate from the server transport so it is unit-testable.
// Port of src/services/documentSymbols.ts.

import (
	"github.com/nikeee/cappu/internal/compiler"
	"github.com/nikeee/cappu/internal/lsp"
)

func dsRange(text string, lineStarts []int, pos, end int) lsp.Range {
	s := compiler.GetLineAndCharacterOfPosition(text, lineStarts, pos)
	e := compiler.GetLineAndCharacterOfPosition(text, lineStarts, end)
	return lsp.Range{
		Start: lsp.Position{Line: s.Line, Character: s.Character},
		End:   lsp.Position{Line: e.Line, Character: e.Character},
	}
}

func dsSymbol(name string, kind lsp.SymbolKind, node, selection *compiler.Node, text string, lineStarts []int, children []lsp.DocumentSymbol) lsp.DocumentSymbol {
	if name == "" {
		name = "<anonymous>"
	}
	// `node` is the declaration for every caller except fields (where it is the
	// VariableDeclarator and @Deprecated sits on the enclosing FieldDeclaration -
	// that branch tags the symbol itself, since parent pointers need binding).
	var tags []int
	if _, ok := compiler.ReadDeprecation(node); ok {
		tags = []int{lsp.SymbolTagDeprecated}
	}
	return lsp.DocumentSymbol{
		Name:           name,
		Kind:           kind,
		Range:          dsRange(text, lineStarts, node.Pos, node.End),
		SelectionRange: dsRange(text, lineStarts, selection.Pos, selection.End),
		Children:       children,
		Tags:           tags,
	}
}

func membersOf(members []*compiler.Node, text string, lineStarts []int) []lsp.DocumentSymbol {
	var result []lsp.DocumentSymbol
	for _, member := range members {
		result = append(result, memberSymbols(member, text, lineStarts)...)
	}
	return result
}

func memberSymbols(node *compiler.Node, text string, lineStarts []int) []lsp.DocumentSymbol {
	switch node.Kind {
	case compiler.ClassDeclaration, compiler.InterfaceDeclaration, compiler.EnumDeclaration,
		compiler.AnnotationTypeDeclaration, compiler.RecordDeclaration:
		return []lsp.DocumentSymbol{typeSymbol(node, text, lineStarts)}
	case compiler.MethodDeclaration:
		name := node.AsMethodDeclaration().Name
		return []lsp.DocumentSymbol{dsSymbol(name.AsIdentifier().Text, lsp.SymbolKindMethod, node, name, text, lineStarts, nil)}
	case compiler.ConstructorDeclaration:
		name := node.AsConstructorDeclaration().Name
		return []lsp.DocumentSymbol{dsSymbol(name.AsIdentifier().Text, lsp.SymbolKindConstructor, node, name, text, lineStarts, nil)}
	case compiler.CompactConstructorDeclaration:
		name := node.AsCompactConstructorDeclaration().Name
		return []lsp.DocumentSymbol{dsSymbol(name.AsIdentifier().Text, lsp.SymbolKindConstructor, node, name, text, lineStarts, nil)}
	case compiler.FieldDeclaration:
		// @Deprecated sits on the FieldDeclaration, not the per-name declarators.
		_, deprecated := compiler.ReadDeprecation(node)
		var out []lsp.DocumentSymbol
		for _, d := range node.AsFieldDeclaration().Declarators.Nodes {
			name := d.AsVariableDeclarator().Name
			s := dsSymbol(name.AsIdentifier().Text, lsp.SymbolKindField, d, name, text, lineStarts, nil)
			if deprecated {
				s.Tags = []int{lsp.SymbolTagDeprecated}
			}
			out = append(out, s)
		}
		return out
	case compiler.EnumConstantDeclaration:
		name := node.AsEnumConstantDeclaration().Name
		return []lsp.DocumentSymbol{dsSymbol(name.AsIdentifier().Text, lsp.SymbolKindEnumMember, node, name, text, lineStarts, nil)}
	case compiler.RecordComponent:
		name := node.AsRecordComponent().Name
		return []lsp.DocumentSymbol{dsSymbol(name.AsIdentifier().Text, lsp.SymbolKindField, node, name, text, lineStarts, nil)}
	default:
		return nil
	}
}

func typeSymbol(node *compiler.Node, text string, lineStarts []int) lsp.DocumentSymbol {
	var children []lsp.DocumentSymbol
	kind := lsp.SymbolKindClass
	var name *compiler.Node

	switch node.Kind {
	case compiler.InterfaceDeclaration:
		kind = lsp.SymbolKindInterface
		d := node.AsInterfaceDeclaration()
		name = d.Name
		children = append(children, membersOf(nodesOf(d.Members), text, lineStarts)...)
	case compiler.AnnotationTypeDeclaration:
		kind = lsp.SymbolKindInterface
		d := node.AsAnnotationTypeDeclaration()
		name = d.Name
		children = append(children, membersOf(nodesOf(d.Members), text, lineStarts)...)
	case compiler.EnumDeclaration:
		kind = lsp.SymbolKindEnum
		d := node.AsEnumDeclaration()
		name = d.Name
		children = append(children, membersOf(nodesOf(d.EnumConstants), text, lineStarts)...)
		children = append(children, membersOf(nodesOf(d.Members), text, lineStarts)...)
	case compiler.RecordDeclaration:
		d := node.AsRecordDeclaration()
		name = d.Name
		children = append(children, membersOf(nodesOf(d.RecordComponents), text, lineStarts)...)
		children = append(children, membersOf(nodesOf(d.Members), text, lineStarts)...)
	default:
		d := node.AsClassDeclaration()
		name = d.Name
		children = append(children, membersOf(nodesOf(d.Members), text, lineStarts)...)
	}

	return dsSymbol(name.AsIdentifier().Text, kind, node, name, text, lineStarts, children)
}

// GetDocumentSymbols builds the top-level outline for a source file.
func GetDocumentSymbols(sourceFile *compiler.Node, lineStarts []int) []lsp.DocumentSymbol {
	text := sourceFile.AsSourceFile().Text
	var result []lsp.DocumentSymbol
	for _, statement := range sourceFile.AsSourceFile().Statements.Nodes {
		switch statement.Kind {
		case compiler.ClassDeclaration, compiler.InterfaceDeclaration, compiler.EnumDeclaration,
			compiler.AnnotationTypeDeclaration, compiler.RecordDeclaration:
			result = append(result, typeSymbol(statement, text, lineStarts))
		}
	}
	return result
}
