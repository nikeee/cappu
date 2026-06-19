package services

// Hover / quick-info rendering for a resolved symbol. Shared by the LSP server
// and the fourslash hover baselines so both render identically.
// Port of src/services/hover.ts.

import (
	"strings"

	"github.com/nikeee/cappu/internal/compiler"
)

// SymbolKindWord returns the human-readable kind word for a symbol's flags.
func SymbolKindWord(flags compiler.SymbolFlags) string {
	switch {
	case flags&compiler.SymbolFlagsPackage != 0:
		return "package"
	case flags&compiler.SymbolFlagsClass != 0:
		return "class"
	case flags&compiler.SymbolFlagsInterface != 0:
		return "interface"
	case flags&compiler.SymbolFlagsEnum != 0:
		return "enum"
	case flags&compiler.SymbolFlagsRecord != 0:
		return "record"
	case flags&compiler.SymbolFlagsAnnotation != 0:
		return "@interface"
	case flags&compiler.SymbolFlagsConstructor != 0:
		return "constructor"
	case flags&compiler.SymbolFlagsMethod != 0:
		return "method"
	case flags&compiler.SymbolFlagsField != 0:
		return "field"
	case flags&compiler.SymbolFlagsEnumConstant != 0:
		return "enum constant"
	case flags&compiler.SymbolFlagsParameter != 0:
		return "parameter"
	case flags&compiler.SymbolFlagsTypeParameter != 0:
		return "type parameter"
	case flags&compiler.SymbolFlagsLocalVariable != 0:
		return "local variable"
	default:
		return "symbol"
	}
}

const typeFlags = compiler.SymbolFlagsClass | compiler.SymbolFlagsInterface |
	compiler.SymbolFlagsEnum | compiler.SymbolFlagsRecord | compiler.SymbolFlagsAnnotation

// EnclosingCall returns the call whose callee is this identifier (directly or
// via recv.name), or nil.
func EnclosingCall(identifier *compiler.Node) *compiler.Node {
	parent := identifier.Parent
	if parent.Kind == compiler.CallExpression && parent.AsCallExpression().Expression == identifier {
		return parent
	}
	if parent.Kind == compiler.PropertyAccessExpression && parent.AsPropertyAccessExpression().Name == identifier &&
		parent.Parent.Kind == compiler.CallExpression && parent.Parent.AsCallExpression().Expression == parent {
		return parent.Parent
	}
	return nil
}

func typeParameterBounds(symbol *compiler.Symbol) string {
	declaration := symbolDeclaration(symbol)
	if declaration == nil || declaration.Kind != compiler.TypeParameter {
		return ""
	}
	bounds := declaration.AsTypeParameter().Constraint
	if bounds == nil || bounds.Len() == 0 {
		return ""
	}
	text := compiler.GetSourceFileOfNode(declaration).AsSourceFile().Text
	var parts []string
	for _, b := range bounds.Nodes {
		parts = append(parts, text[compiler.SkipTrivia(text, b.Pos):b.End])
	}
	return strings.Join(parts, " & ")
}

// GetHoverText renders a one-line hover label for a resolved symbol. atNode is
// the referencing identifier when hovering a use rather than the declaration
// (pass nil for the declaration).
func GetHoverText(checker *compiler.Checker, symbol *compiler.Symbol, atNode *compiler.Node) string {
	if symbol.Flags&(compiler.SymbolFlagsMethod|compiler.SymbolFlagsConstructor) != 0 {
		var call *compiler.Node
		if atNode != nil && atNode.Kind == compiler.Identifier {
			call = EnclosingCall(atNode)
		}
		if call != nil {
			if instantiated, ok := checker.InstantiatedSignatureOfCall(call); ok {
				return instantiated
			}
		}
		if signature, ok := checker.SignatureOfSymbol(symbol); ok {
			return signature
		}
	}
	word := SymbolKindWord(symbol.Flags)
	if symbol.Flags&(typeFlags|compiler.SymbolFlagsPackage) != 0 {
		return word + " " + symbol.EscapedName
	}
	if symbol.Flags&compiler.SymbolFlagsTypeParameter != 0 {
		bounds := typeParameterBounds(symbol)
		if bounds != "" {
			return "(" + word + ") " + symbol.EscapedName + " extends " + bounds
		}
		return "(" + word + ") " + symbol.EscapedName
	}
	typ := useSiteTypeString(checker, atNode)
	if typ == "" {
		typ = checker.TypeStringOfSymbol(symbol)
	}
	if typ == "<error>" {
		return "(" + word + ") " + symbol.EscapedName
	}
	return "(" + word + ") " + typ + " " + symbol.EscapedName
}

func useSiteTypeString(checker *compiler.Checker, atNode *compiler.Node) string {
	if atNode == nil || atNode.Kind != compiler.Identifier ||
		atNode.Parent == nil || atNode.Parent.Kind != compiler.PropertyAccessExpression ||
		atNode.Parent.AsPropertyAccessExpression().Name != atNode {
		return ""
	}
	t := checker.GetTypeOfExpression(atNode.Parent)
	if t.Kind == compiler.TypeKindError {
		return ""
	}
	return compiler.TypeToString(t)
}
