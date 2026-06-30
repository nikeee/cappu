package services

// Semantic tokens: classify every resolved identifier so editors color fields,
// locals, parameters, type parameters etc. accurately. Offset-based and
// position-free; the LSP server encodes the entries into the wire format.
// Port of src/services/semanticTokens.ts.

import (
	"cmp"
	"slices"

	"github.com/nikeee/cappu/internal/compiler"
)

// TokenTypes is the semantic-token legend the server advertises.
var TokenTypes = []string{
	"namespace", "class", "interface", "enum", "enumMember", "typeParameter",
	"type", "method", "property", "parameter", "variable",
}

// TokenModifiers is the semantic-token modifier legend.
var TokenModifiers = []string{"declaration", "static", "readonly", "defaultLibrary"}

func typeIndex(name string) int {
	for i, t := range TokenTypes {
		if t == name {
			return i
		}
	}
	return -1
}

const (
	modDeclaration    = 1 << 0
	modStatic         = 1 << 1
	modReadonly       = 1 << 2
	modDefaultLibrary = 1 << 3
)

// SemanticTokenEntry is one classified identifier span.
type SemanticTokenEntry struct {
	Offset         int
	Length         int
	TokenType      int // index into TokenTypes
	TokenModifiers int // bit set over TokenModifiers
}

func tokenTypeOf(flags compiler.SymbolFlags) int {
	switch {
	case flags&compiler.SymbolFlagsPackage != 0:
		return typeIndex("namespace")
	case flags&compiler.SymbolFlagsClass != 0:
		return typeIndex("class")
	case flags&compiler.SymbolFlagsInterface != 0:
		return typeIndex("interface")
	case flags&compiler.SymbolFlagsEnum != 0:
		return typeIndex("enum")
	case flags&compiler.SymbolFlagsRecord != 0:
		return typeIndex("class")
	case flags&compiler.SymbolFlagsAnnotation != 0:
		return typeIndex("type")
	case flags&compiler.SymbolFlagsTypeParameter != 0:
		return typeIndex("typeParameter")
	case flags&compiler.SymbolFlagsEnumConstant != 0:
		return typeIndex("enumMember")
	case flags&(compiler.SymbolFlagsMethod|compiler.SymbolFlagsConstructor) != 0:
		return typeIndex("method")
	case flags&compiler.SymbolFlagsField != 0:
		return typeIndex("property")
	case flags&compiler.SymbolFlagsParameter != 0:
		return typeIndex("parameter")
	case flags&compiler.SymbolFlagsLocalVariable != 0:
		return typeIndex("variable")
	default:
		return -1
	}
}

// modifierCarrier returns the node whose modifiers govern this symbol.
func modifierCarrier(symbol *compiler.Symbol) *compiler.Node {
	declaration := symbolDeclaration(symbol)
	if declaration == nil {
		return nil
	}
	if declaration.Kind == compiler.VariableDeclarator {
		return declaration.Parent
	}
	return declaration
}

func modifiersOf(symbol *compiler.Symbol, isDeclarationName bool) int {
	bits := 0
	if isDeclarationName {
		bits = modDeclaration
	}
	if symbol.Flags&compiler.SymbolFlagsEnumConstant != 0 {
		bits |= modStatic | modReadonly
	} else if carrier := modifierCarrier(symbol); carrier != nil {
		if mods := declModifiers(carrier); mods != nil {
			for _, m := range mods.Nodes {
				if m.Kind == compiler.StaticKeyword {
					bits |= modStatic
				}
				if m.Kind == compiler.FinalKeyword {
					bits |= modReadonly
				}
			}
		}
	}
	if declaration := symbolDeclaration(symbol); declaration != nil {
		if compiler.IsSyntheticURI(compiler.URI(compiler.GetSourceFileOfNode(declaration).AsSourceFile().FileName)) {
			bits |= modDefaultLibrary
		}
	}
	return bits
}

// GetSemanticTokens classifies every resolved identifier in a source file.
func GetSemanticTokens(checker *compiler.Checker, sourceFile *compiler.Node) []SemanticTokenEntry {
	text := sourceFile.AsSourceFile().Text
	var entries []SemanticTokenEntry
	var visit compiler.Visitor
	visit = func(node *compiler.Node) bool {
		if node.Kind == compiler.Identifier {
			symbol := checker.ResolveName(node)
			tokenType := -1
			if symbol != nil {
				tokenType = tokenTypeOf(symbol.Flags)
			}
			if symbol != nil && tokenType != -1 {
				start := compiler.SkipTrivia(text, node.Pos)
				length := node.End - start
				if length > 0 {
					isDeclarationName := node.Parent != nil && node.Parent.Symbol == symbol &&
						nodeNameOf(node.Parent) == node
					entries = append(entries, SemanticTokenEntry{
						Offset:         start,
						Length:         length,
						TokenType:      tokenType,
						TokenModifiers: modifiersOf(symbol, isDeclarationName),
					})
				}
			}
		}
		node.ForEachChild(visit)
		return false
	}
	visit(sourceFile)
	slices.SortStableFunc(entries, func(a, b SemanticTokenEntry) int { return cmp.Compare(a.Offset, b.Offset) })
	return entries
}

// nodeNameOf returns the name Identifier of a node (for declaration-name checks).
func nodeNameOf(node *compiler.Node) *compiler.Node {
	return declName(node)
}
