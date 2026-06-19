package services

// Inlay hints: parameter names at call sites and inferred types for `var`.
// Offset-based so the unit tests stay position-free; the LSP server converts
// offsets to positions and applies the user's configuration.
// Port of src/services/inlayHints.ts.

import (
	"sort"

	"github.com/nikeee/cappu/internal/compiler"
)

// InlayHintsSettings toggles each hint family.
type InlayHintsSettings struct {
	ParameterNames bool // hints like `count:` before call arguments
	VarTypes       bool // hints like `: String` after a `var` declaration's name
}

// DefaultInlayHints enables both families.
var DefaultInlayHints = InlayHintsSettings{ParameterNames: true, VarTypes: true}

// InlayHintEntry is one hint at an offset.
type InlayHintEntry struct {
	Offset int
	Label  string
	Kind   string // "parameter" | "type"
}

func isSelfExplanatoryArgument(arg *compiler.Node) bool {
	switch arg.Kind {
	case compiler.Identifier, compiler.ThisExpression, compiler.PropertyAccessExpression:
		return true
	default:
		return false
	}
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

// GetInlayHints collects parameter-name and var-type hints in [startOffset, endOffset].
func GetInlayHints(checker *compiler.Checker, sourceFile *compiler.Node, startOffset, endOffset int, settings InlayHintsSettings) []InlayHintEntry {
	text := sourceFile.AsSourceFile().Text
	var hints []InlayHintEntry

	skipToStart := func(node *compiler.Node) int {
		pos := node.Pos
		for pos < node.End && pos < len(text) && isSpace(text[pos]) {
			pos++
		}
		return pos
	}

	varTypeHint := func(declarator *compiler.Node) {
		name := declName(declarator)
		symbol := declarator.Symbol
		if name == nil || symbol == nil {
			return
		}
		if name.End < startOffset || name.End > endOffset {
			return
		}
		typ := checker.TypeStringOfSymbol(symbol)
		if typ == "<error>" || typ == "var" || typ == "" {
			return
		}
		hints = append(hints, InlayHintEntry{Offset: name.End, Label: ": " + typ, Kind: "type"})
	}

	collectCallHints := func(call *compiler.Node) {
		args := call.AsCallExpression().Arguments
		if args == nil || args.Len() == 0 {
			return
		}
		decl := checker.ResolveCall(call)
		if decl == nil {
			return
		}
		params := decl.AsMethodDeclaration().Parameters
		for i, arg := range args.Nodes {
			if arg.End < startOffset || arg.Pos > endOffset {
				continue
			}
			if isSelfExplanatoryArgument(arg) {
				continue
			}
			if i >= params.Len() {
				continue
			}
			param := params.Nodes[i].AsParameter()
			if param.Name == nil {
				continue
			}
			name := param.Name.AsIdentifier().Text
			if param.IsVarArgs {
				if i != params.Len()-1 {
					continue
				}
				hints = append(hints, InlayHintEntry{Offset: skipToStart(arg), Label: "..." + name + ":", Kind: "parameter"})
				continue
			}
			hints = append(hints, InlayHintEntry{Offset: skipToStart(arg), Label: name + ":", Kind: "parameter"})
		}
	}

	var visit compiler.Visitor
	visit = func(node *compiler.Node) bool {
		if node.End < startOffset || node.Pos > endOffset {
			return false
		}
		if settings.ParameterNames && node.Kind == compiler.CallExpression {
			collectCallHints(node)
		}
		if settings.VarTypes && node.Kind == compiler.LocalVariableDeclarationStatement {
			s := node.AsLocalVariableDeclarationStatement()
			if s.Type.Kind == compiler.VarType {
				for _, d := range s.Declarators.Nodes {
					varTypeHint(d)
				}
			}
		}
		if settings.VarTypes && node.Kind == compiler.ForEachStatement {
			parameter := node.AsForEachStatement().Parameter
			if parameter.AsParameter().Type.Kind == compiler.VarType {
				varTypeHint(parameter)
			}
		}
		node.ForEachChild(visit)
		return false
	}
	visit(sourceFile)
	sort.SliceStable(hints, func(i, j int) bool { return hints[i].Offset < hints[j].Offset })
	return hints
}
