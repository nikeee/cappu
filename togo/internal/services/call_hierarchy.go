package services

// Call hierarchy (incoming / outgoing calls) as pure functions over the program,
// kept separate from the lspserver layer. Port of src/services/callHierarchy.ts.
// Incoming/outgoing re-resolve the method from the item's SelectionRange position
// the client hands back.

import (
	"github.com/nikeee/cappu/internal/compiler"
	"github.com/nikeee/cappu/internal/lsp"
)

const callableFlags = compiler.SymbolFlagsMethod | compiler.SymbolFlagsConstructor

// enclosingCallable returns the method/constructor declaration enclosing a node,
// or nil (a reference in a field initializer / static block has none).
func enclosingCallable(node *compiler.Node) *compiler.Node {
	for n := node.Parent; n != nil; n = n.Parent {
		if n.Kind == compiler.MethodDeclaration || n.Kind == compiler.ConstructorDeclaration {
			return n
		}
	}
	return nil
}

// callHierarchyItemOf builds an item for a method/constructor declaration.
func callHierarchyItemOf(decl *compiler.Node) lsp.CallHierarchyItem {
	file := compiler.GetSourceFileOfNode(decl)
	text := file.AsSourceFile().Text
	lineStarts := compiler.ComputeLineStarts(text)
	name := "<anonymous>"
	kind := lsp.SymbolKindMethod
	if decl.Kind == compiler.ConstructorDeclaration {
		name = "<init>"
		kind = lsp.SymbolKindConstructor
	} else if nm := nodeNameOf(decl); nm != nil && nm.Kind == compiler.Identifier {
		name = nm.AsIdentifier().Text
	}
	selection := decl
	if nm := nodeNameOf(decl); nm != nil {
		selection = nm
	}
	return lsp.CallHierarchyItem{
		Name:           name,
		Kind:           kind,
		URI:            file.AsSourceFile().FileName,
		Range:          dsRange(text, lineStarts, compiler.SkipTrivia(text, decl.Pos), decl.End),
		SelectionRange: dsRange(text, lineStarts, compiler.SkipTrivia(text, selection.Pos), selection.End),
	}
}

func callableSymbolOfItem(program *compiler.Program, checker *compiler.Checker, item lsp.CallHierarchyItem) *compiler.Symbol {
	sourceFile := program.GetSourceFile(compiler.URI(item.URI))
	if sourceFile == nil {
		return nil
	}
	text := sourceFile.AsSourceFile().Text
	lineStarts := compiler.ComputeLineStarts(text)
	offset := compiler.GetPositionOfLineAndCharacter(text, lineStarts, item.SelectionRange.Start.Line, item.SelectionRange.Start.Character)
	id := compiler.GetIdentifierAtPosition(sourceFile, offset)
	if id == nil {
		return nil
	}
	symbol := checker.ResolveName(id)
	if symbol == nil || symbol.Flags&callableFlags == 0 {
		return nil
	}
	return symbol
}

func callableDeclarations(symbol *compiler.Symbol) []*compiler.Node {
	var out []*compiler.Node
	for _, d := range symbol.Declarations {
		if d.Kind == compiler.MethodDeclaration || d.Kind == compiler.ConstructorDeclaration {
			out = append(out, d)
		}
	}
	return out
}

// PrepareCallHierarchy returns the method/constructor at a position.
func PrepareCallHierarchy(checker *compiler.Checker, sourceFile *compiler.Node, offset int) []lsp.CallHierarchyItem {
	id := compiler.GetIdentifierAtPosition(sourceFile, offset)
	if id == nil {
		return nil
	}
	symbol := checker.ResolveName(id)
	if symbol == nil || symbol.Flags&callableFlags == 0 {
		return nil
	}
	var out []lsp.CallHierarchyItem
	for _, d := range callableDeclarations(symbol) {
		out = append(out, callHierarchyItemOf(d))
	}
	return out
}

// CallHierarchyIncoming returns who calls the item's method, grouped by the
// enclosing method/constructor of each call site.
func CallHierarchyIncoming(program *compiler.Program, checker *compiler.Checker, item lsp.CallHierarchyItem) []lsp.CallHierarchyIncomingCall {
	symbol := callableSymbolOfItem(program, checker, item)
	if symbol == nil {
		return nil
	}
	// Preserve first-seen caller order while grouping ranges.
	var order []*compiler.Node
	calls := map[*compiler.Node]*lsp.CallHierarchyIncomingCall{}
	for _, ref := range compiler.FindReferences(symbol, program, checker.ResolveName) {
		if EnclosingCall(ref) == nil {
			continue // only call sites
		}
		caller := enclosingCallable(ref)
		if caller == nil {
			continue
		}
		file := compiler.GetSourceFileOfNode(ref)
		text := file.AsSourceFile().Text
		lineStarts := compiler.ComputeLineStarts(text)
		r := dsRange(text, lineStarts, compiler.SkipTrivia(text, ref.Pos), ref.End)
		if entry, ok := calls[caller]; ok {
			entry.FromRanges = append(entry.FromRanges, r)
		} else {
			calls[caller] = &lsp.CallHierarchyIncomingCall{From: callHierarchyItemOf(caller), FromRanges: []lsp.Range{r}}
			order = append(order, caller)
		}
	}
	var out []lsp.CallHierarchyIncomingCall
	for _, caller := range order {
		out = append(out, *calls[caller])
	}
	return out
}

// CallHierarchyOutgoing returns the callees of the item's method, grouped by
// callee, with the call-site ranges in the method body.
func CallHierarchyOutgoing(program *compiler.Program, checker *compiler.Checker, item lsp.CallHierarchyItem) []lsp.CallHierarchyOutgoingCall {
	symbol := callableSymbolOfItem(program, checker, item)
	if symbol == nil {
		return nil
	}
	var order []*compiler.Node
	calls := map[*compiler.Node]*lsp.CallHierarchyOutgoingCall{}
	for _, decl := range callableDeclarations(symbol) {
		body := declBody(decl)
		if body == nil {
			continue
		}
		file := compiler.GetSourceFileOfNode(decl)
		text := file.AsSourceFile().Text
		lineStarts := compiler.ComputeLineStarts(text)
		var visit func(node *compiler.Node)
		visit = func(node *compiler.Node) {
			if node.Kind == compiler.CallExpression {
				if target := checker.ResolveCall(node); target != nil {
					if nameNode := calleeName(node); nameNode != nil {
						r := dsRange(text, lineStarts, compiler.SkipTrivia(text, nameNode.Pos), nameNode.End)
						if entry, ok := calls[target]; ok {
							entry.FromRanges = append(entry.FromRanges, r)
						} else {
							calls[target] = &lsp.CallHierarchyOutgoingCall{To: callHierarchyItemOf(target), FromRanges: []lsp.Range{r}}
							order = append(order, target)
						}
					}
				}
			}
			node.ForEachChild(func(c *compiler.Node) bool {
				visit(c)
				return false
			})
		}
		visit(body)
	}
	var out []lsp.CallHierarchyOutgoingCall
	for _, callee := range order {
		out = append(out, *calls[callee])
	}
	return out
}

// declBody returns a method/constructor declaration's body, or nil.
func declBody(decl *compiler.Node) *compiler.Node {
	switch decl.Kind {
	case compiler.MethodDeclaration:
		return decl.AsMethodDeclaration().Body
	case compiler.ConstructorDeclaration:
		return decl.AsConstructorDeclaration().Body
	default:
		return nil
	}
}

// calleeName returns the callee name identifier of a call expression, or nil.
func calleeName(call *compiler.Node) *compiler.Node {
	callee := call.AsCallExpression().Expression
	switch callee.Kind {
	case compiler.PropertyAccessExpression:
		return callee.AsPropertyAccessExpression().Name
	case compiler.Identifier:
		return callee
	default:
		return nil
	}
}
