package lspserver

// The LSP request handlers, wiring the language-services layer to the wire
// protocol. Port of the connection.on* handlers in src/services/server.ts.

import (
	"encoding/json"
	"strings"

	"github.com/nikeee/cappu/internal/compiler"
	"github.com/nikeee/cappu/internal/lsp"
	"github.com/nikeee/cappu/internal/services"
)

func (s *Server) onDocumentSymbol(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode[lsp.DocumentSymbolParams](params)
	sourceFile := s.program.GetSourceFile(compiler.URI(p.TextDocument.URI))
	if sourceFile == nil {
		return []lsp.DocumentSymbol{}, nil
	}
	return services.GetDocumentSymbols(sourceFile, sourceFile.AsSourceFile().LineStarts()), nil
}

func (s *Server) onReferences(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode[lsp.ReferenceParams](params)
	identifier := s.identifierAt(compiler.URI(p.TextDocument.URI), p.Position)
	if identifier == nil {
		return nil, nil
	}
	symbol := s.checker.ResolveName(identifier)
	if symbol == nil {
		return nil, nil
	}
	var locs []lsp.Location
	for _, node := range compiler.FindReferences(symbol, s.program, s.checker.ResolveName) {
		locs = append(locs, s.locationOf(node))
	}
	return locs, nil
}

func (s *Server) onPrepareRename(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode[lsp.TextDocumentPositionParams](params)
	identifier := s.identifierAt(compiler.URI(p.TextDocument.URI), p.Position)
	if identifier == nil {
		return nil, nil
	}
	symbol := s.checker.ResolveName(identifier)
	if symbol == nil || compiler.IsStubSymbol(symbol) {
		return nil, nil
	}
	return s.rangeOf(identifier), nil
}

func (s *Server) onRename(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode[lsp.RenameParams](params)
	identifier := s.identifierAt(compiler.URI(p.TextDocument.URI), p.Position)
	if identifier == nil {
		return nil, nil
	}
	symbol := s.checker.ResolveName(identifier)
	if symbol == nil {
		return nil, nil
	}
	if compiler.IsStubSymbol(symbol) {
		return nil, &lsp.ResponseError{Code: lsp.ErrInvalidRequest, Message: "Cannot rename a symbol defined by the JDK."}
	}
	if !compiler.IsValidIdentifier(p.NewName) {
		return nil, &lsp.ResponseError{Code: lsp.ErrInvalidParams, Message: "'" + p.NewName + "' is not a valid Java identifier."}
	}
	changes := map[string][]lsp.TextEdit{}
	for _, node := range compiler.FindReferences(symbol, s.program, s.checker.ResolveName) {
		loc := s.locationOf(node)
		changes[loc.URI] = append(changes[loc.URI], lsp.TextEdit{Range: loc.Range, NewText: p.NewName})
	}
	if len(changes) == 0 {
		return nil, nil
	}
	return lsp.WorkspaceEdit{Changes: changes}, nil
}

func (s *Server) onDefinition(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode[lsp.TextDocumentPositionParams](params)
	sourceFile, offset, ok := s.sourceAndOffset(compiler.URI(p.TextDocument.URI), p.Position)
	if !ok {
		return nil, nil
	}
	if identifier := compiler.GetIdentifierAtPosition(sourceFile, offset); identifier != nil {
		symbol := s.checker.ResolveName(identifier)
		if symbol == nil {
			return nil, nil
		}
		if nameNode := compiler.GetDeclarationNameNode(symbol); nameNode != nil {
			return s.locationOf(nameNode), nil
		}
		return nil, nil
	}
	node := compiler.GetNodeAtPosition(sourceFile, offset)
	if node.Kind == compiler.VarType {
		if nameID := varInferredFrom(node); nameID != nil {
			return s.typeDefinitionOf(nameID), nil
		}
	}
	return nil, nil
}

func (s *Server) onTypeDefinition(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode[lsp.TextDocumentPositionParams](params)
	identifier := s.identifierAt(compiler.URI(p.TextDocument.URI), p.Position)
	if identifier == nil {
		return nil, nil
	}
	return s.typeDefinitionOf(identifier), nil
}

func (s *Server) onHover(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode[lsp.TextDocumentPositionParams](params)
	identifier := s.identifierAt(compiler.URI(p.TextDocument.URI), p.Position)
	if identifier == nil {
		return nil, nil
	}
	symbol := s.checker.ResolveName(identifier)
	if symbol == nil {
		return nil, nil
	}
	text := services.GetHoverText(s.checker, symbol, identifier)
	var doc string
	if call := services.EnclosingCall(identifier); call != nil {
		if overload := s.checker.ResolveCall(call); overload != nil {
			doc, _ = s.checker.GetDocumentationOfNode(overload)
		}
	} else {
		doc, _ = s.checker.GetDocumentation(symbol)
	}
	value := "```java\n" + text + "\n```"
	if dep, ok := compiler.SymbolDeprecation(symbol); ok {
		note := "**Deprecated.**"
		if dep.ForRemoval {
			note = "**Deprecated** (for removal)."
		}
		if dep.HasSince {
			note += " Since " + dep.Since + "."
		}
		value += "\n\n" + note
	}
	if doc != "" {
		value += "\n\n" + doc
	}
	return lsp.Hover{Contents: lsp.MarkupContent{Kind: lsp.MarkupKindMarkdown, Value: value}}, nil
}

func (s *Server) onCompletion(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode[lsp.TextDocumentPositionParams](params)
	sourceFile, offset, ok := s.sourceAndOffset(compiler.URI(p.TextDocument.URI), p.Position)
	if !ok {
		return []lsp.CompletionItem{}, nil
	}
	var out []lsp.CompletionItem
	for _, it := range services.GetCompletions(s.program, s.checker, sourceFile, offset, s.config) {
		item := lsp.CompletionItem{Label: it.Label, Kind: int(it.Kind)}
		if it.Deprecated {
			item.Tags = []int{lsp.CompletionItemTagDeprecated}
		}
		out = append(out, item)
	}
	if out == nil {
		out = []lsp.CompletionItem{}
	}
	return out, nil
}

func (s *Server) onCodeAction(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode[lsp.CodeActionParams](params)
	uri := compiler.URI(p.TextDocument.URI)
	sourceFile := s.program.GetSourceFile(uri)
	if sourceFile == nil {
		return []lsp.CodeAction{}, nil
	}
	text := sourceFile.AsSourceFile().Text
	lineStarts := sourceFile.AsSourceFile().LineStarts()
	start := compiler.GetPositionOfLineAndCharacter(text, lineStarts, p.Range.Start.Line, p.Range.Start.Character)
	end := compiler.GetPositionOfLineAndCharacter(text, lineStarts, p.Range.End.Line, p.Range.End.Character)
	var out []lsp.CodeAction
	for _, action := range services.GetCodeActions(s.program, s.checker, sourceFile, start, end) {
		var edits []lsp.TextEdit
		for _, c := range action.Changes {
			edits = append(edits, lsp.TextEdit{Range: lspRange(text, lineStarts, c.Start, c.End), NewText: c.NewText})
		}
		changes := map[string][]lsp.TextEdit{p.TextDocument.URI: edits}
		for uri, cs := range action.AdditionalEdits {
			other := s.program.GetSourceFile(compiler.URI(uri))
			if other == nil {
				continue
			}
			otherText := other.AsSourceFile().Text
			otherLineStarts := other.AsSourceFile().LineStarts()
			var otherEdits []lsp.TextEdit
			for _, c := range cs {
				otherEdits = append(otherEdits, lsp.TextEdit{Range: lspRange(otherText, otherLineStarts, c.Start, c.End), NewText: c.NewText})
			}
			changes[uri] = otherEdits
		}
		out = append(out, lsp.CodeAction{
			Title: action.Title,
			Kind:  action.Kind,
			Edit:  lsp.WorkspaceEdit{Changes: changes},
		})
	}
	if out == nil {
		out = []lsp.CodeAction{}
	}
	return out, nil
}

func (s *Server) callAt(uri compiler.URI, pos lsp.Position) (*compiler.Node, int) {
	sourceFile, offset, ok := s.sourceAndOffset(uri, pos)
	if !ok {
		return nil, 0
	}
	for _, at := range []int{offset, offset - 1} {
		node := compiler.GetNodeAtPosition(sourceFile, at)
		for ; node != nil; node = node.Parent {
			if node.Kind != compiler.CallExpression {
				continue
			}
			if offset > node.AsCallExpression().Expression.End && offset <= node.End {
				return node, offset
			}
		}
	}
	return nil, offset
}

func (s *Server) onSignatureHelp(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode[lsp.TextDocumentPositionParams](params)
	call, offset := s.callAt(compiler.URI(p.TextDocument.URI), p.Position)
	if call == nil {
		return nil, nil
	}
	candidates := s.checker.ResolveCallCandidates(call)
	if len(candidates) == 0 {
		return nil, nil
	}
	var signatures []lsp.SignatureInformation
	for _, decl := range candidates {
		label, ok := s.checker.SignatureOfDeclaration(decl)
		if !ok {
			continue
		}
		var paramInfos []lsp.ParameterInformation
		for _, p := range s.checker.ParameterLabelsOf(decl) {
			paramInfos = append(paramInfos, lsp.ParameterInformation{Label: p})
		}
		si := lsp.SignatureInformation{Label: label, Parameters: paramInfos}
		if doc, ok := s.checker.GetDocumentationOfNode(decl); ok {
			si.Documentation = doc
		}
		signatures = append(signatures, si)
	}
	if len(signatures) == 0 {
		return nil, nil
	}
	resolved := s.checker.ResolveCall(call)
	activeSignature := 0
	for i, d := range candidates {
		if d == resolved {
			activeSignature = i
			break
		}
	}
	activeParameter := 0
	if args := call.AsCallExpression().Arguments; args != nil {
		for _, a := range args.Nodes {
			if a.End < offset {
				activeParameter++
			}
		}
	}
	return lsp.SignatureHelp{Signatures: signatures, ActiveSignature: activeSignature, ActiveParameter: activeParameter}, nil
}

func (s *Server) onWorkspaceSymbol(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode[lsp.WorkspaceSymbolParams](params)
	query := strings.ToLower(p.Query)
	if query == "" {
		return []lsp.SymbolInformation{}, nil
	}
	var results []lsp.SymbolInformation
	var flatten func(uri string, symbols []lsp.DocumentSymbol, container string)
	flatten = func(uri string, symbols []lsp.DocumentSymbol, container string) {
		for _, sym := range symbols {
			if strings.Contains(strings.ToLower(sym.Name), query) {
				info := lsp.SymbolInformation{Name: sym.Name, Kind: int(sym.Kind), Location: lsp.Location{URI: uri, Range: sym.Range}, ContainerName: container, Tags: sym.Tags}
				results = append(results, info)
			}
			if len(sym.Children) > 0 {
				flatten(uri, sym.Children, sym.Name)
			}
			if len(results) >= 256 {
				return
			}
		}
	}
	for _, uri := range s.program.GetAllUris() {
		if compiler.IsSyntheticURI(uri) {
			continue
		}
		sourceFile := s.program.GetSourceFile(uri)
		if sourceFile == nil {
			continue
		}
		lineStarts := sourceFile.AsSourceFile().LineStarts()
		flatten(string(uri), services.GetDocumentSymbols(sourceFile, lineStarts), "")
		if len(results) >= 256 {
			break
		}
	}
	if results == nil {
		results = []lsp.SymbolInformation{}
	}
	return results, nil
}

func (s *Server) onDocumentHighlight(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode[lsp.TextDocumentPositionParams](params)
	identifier := s.identifierAt(compiler.URI(p.TextDocument.URI), p.Position)
	if identifier == nil {
		return nil, nil
	}
	symbol := s.checker.ResolveName(identifier)
	if symbol == nil {
		return nil, nil
	}
	isWrite := func(node *compiler.Node) bool {
		parent := node.Parent
		if parent == nil {
			return false
		}
		if parent.Kind == compiler.AssignmentExpression && parent.AsAssignmentExpression().Left == node {
			return true
		}
		if parent.Kind == compiler.PostfixUnaryExpression {
			return true
		}
		if parent.Kind == compiler.PrefixUnaryExpression {
			op := parent.AsPrefixUnaryExpression().Operator
			return op == compiler.PlusPlusToken || op == compiler.MinusMinusToken
		}
		return false
	}
	var out []lsp.DocumentHighlight
	for _, node := range compiler.FindReferences(symbol, s.program, s.checker.ResolveName) {
		if compiler.GetSourceFileOfNode(node).AsSourceFile().FileName != p.TextDocument.URI {
			continue
		}
		kind := lsp.DocumentHighlightRead
		if isWrite(node) {
			kind = lsp.DocumentHighlightWrite
		}
		out = append(out, lsp.DocumentHighlight{Range: s.rangeOf(node), Kind: kind})
	}
	return out, nil
}

var foldableKinds = map[compiler.SyntaxKind]bool{
	compiler.ClassDeclaration: true, compiler.InterfaceDeclaration: true, compiler.EnumDeclaration: true,
	compiler.RecordDeclaration: true, compiler.AnnotationTypeDeclaration: true, compiler.MethodDeclaration: true,
	compiler.ConstructorDeclaration: true, compiler.CompactConstructorDeclaration: true, compiler.InitializerBlock: true,
}

func (s *Server) onFoldingRange(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode[lsp.DocumentSymbolParams](params)
	sourceFile := s.program.GetSourceFile(compiler.URI(p.TextDocument.URI))
	if sourceFile == nil {
		return nil, nil
	}
	text := sourceFile.AsSourceFile().Text
	lineStarts := sourceFile.AsSourceFile().LineStarts()
	lineAt := func(offset int) int { return compiler.GetLineAndCharacterOfPosition(text, lineStarts, offset).Line }
	var ranges []lsp.FoldingRange
	var visit compiler.Visitor
	visit = func(node *compiler.Node) bool {
		if foldableKinds[node.Kind] {
			startLine := lineAt(compiler.SkipTrivia(text, node.Pos))
			endLine := lineAt(node.End) - 1
			if endLine > startLine {
				ranges = append(ranges, lsp.FoldingRange{StartLine: startLine, EndLine: endLine})
			}
		}
		node.ForEachChild(visit)
		return false
	}
	visit(sourceFile)
	imports := sourceFile.AsSourceFile().Imports
	if imports != nil && imports.Len() > 1 {
		first := imports.Nodes[0]
		last := imports.Nodes[imports.Len()-1]
		startLine := lineAt(compiler.SkipTrivia(text, first.Pos))
		endLine := lineAt(last.End)
		if endLine > startLine {
			ranges = append(ranges, lsp.FoldingRange{StartLine: startLine, EndLine: endLine, Kind: "imports"})
		}
	}
	return ranges, nil
}

func (s *Server) onImplementation(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode[lsp.TextDocumentPositionParams](params)
	identifier := s.identifierAt(compiler.URI(p.TextDocument.URI), p.Position)
	if identifier == nil {
		return nil, nil
	}
	symbol := s.checker.ResolveName(identifier)
	if symbol == nil {
		return nil, nil
	}
	if symbol.Flags&(compiler.SymbolFlagsClass|compiler.SymbolFlagsInterface) != 0 {
		var locs []lsp.Location
		for _, sub := range services.GetSubtypeIndex(s.program).AllSubtypesOf(symbol) {
			if n := services.DeclarationName(sub); n != nil {
				locs = append(locs, s.locationOf(n))
			}
		}
		if len(locs) == 0 {
			return nil, nil
		}
		return locs, nil
	}
	if symbol.Flags&compiler.SymbolFlagsMethod != 0 {
		var locs []lsp.Location
		for _, d := range symbol.Declarations {
			if d.Kind != compiler.MethodDeclaration {
				continue
			}
			for _, m := range services.FindMethodImplementations(d, s.program) {
				locs = append(locs, s.locationOf(m.AsMethodDeclaration().Name))
			}
		}
		if len(locs) == 0 {
			return nil, nil
		}
		return locs, nil
	}
	return nil, nil
}

// typeHierarchyItemParams is the {item} payload of supertypes/subtypes requests.
type typeHierarchyItemParams struct {
	Item lsp.TypeHierarchyItem `json:"item"`
}

func (s *Server) onPrepareTypeHierarchy(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode[lsp.TextDocumentPositionParams](params)
	sourceFile, offset, ok := s.sourceAndOffset(compiler.URI(p.TextDocument.URI), p.Position)
	if !ok {
		return nil, nil
	}
	items := services.PrepareTypeHierarchy(s.checker, sourceFile, offset)
	if len(items) == 0 {
		return nil, nil
	}
	return items, nil
}

func (s *Server) onTypeHierarchySupertypes(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode[typeHierarchyItemParams](params)
	items := services.TypeHierarchySupertypes(s.program, s.checker, p.Item)
	if len(items) == 0 {
		return nil, nil
	}
	return items, nil
}

func (s *Server) onTypeHierarchySubtypes(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode[typeHierarchyItemParams](params)
	items := services.TypeHierarchySubtypes(s.program, s.checker, p.Item)
	if len(items) == 0 {
		return nil, nil
	}
	return items, nil
}

// callHierarchyItemParams is the {item} payload of incoming/outgoing requests.
type callHierarchyItemParams struct {
	Item lsp.CallHierarchyItem `json:"item"`
}

func (s *Server) onPrepareCallHierarchy(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode[lsp.TextDocumentPositionParams](params)
	sourceFile, offset, ok := s.sourceAndOffset(compiler.URI(p.TextDocument.URI), p.Position)
	if !ok {
		return nil, nil
	}
	items := services.PrepareCallHierarchy(s.checker, sourceFile, offset)
	if len(items) == 0 {
		return nil, nil
	}
	return items, nil
}

func (s *Server) onCallHierarchyIncoming(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode[callHierarchyItemParams](params)
	calls := services.CallHierarchyIncoming(s.program, s.checker, p.Item)
	if len(calls) == 0 {
		return nil, nil
	}
	return calls, nil
}

func (s *Server) onCallHierarchyOutgoing(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode[callHierarchyItemParams](params)
	calls := services.CallHierarchyOutgoing(s.program, s.checker, p.Item)
	if len(calls) == 0 {
		return nil, nil
	}
	return calls, nil
}

func (s *Server) onInlayHint(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode[lsp.InlayHintParams](params)
	sourceFile := s.program.GetSourceFile(compiler.URI(p.TextDocument.URI))
	if sourceFile == nil {
		return []lsp.InlayHint{}, nil
	}
	text := sourceFile.AsSourceFile().Text
	lineStarts := sourceFile.AsSourceFile().LineStarts()
	start := compiler.GetPositionOfLineAndCharacter(text, lineStarts, p.Range.Start.Line, p.Range.Start.Character)
	end := compiler.GetPositionOfLineAndCharacter(text, lineStarts, p.Range.End.Line, p.Range.End.Character)
	var out []lsp.InlayHint
	for _, h := range services.GetInlayHints(s.checker, sourceFile, start, end, s.inlayHints) {
		hint := lsp.InlayHint{Position: lspPos(text, lineStarts, h.Offset), Label: h.Label}
		if h.Kind == "parameter" {
			hint.Kind = lsp.InlayHintKindParameter
			hint.PaddingRight = true
		} else {
			hint.Kind = lsp.InlayHintKindType
		}
		out = append(out, hint)
	}
	if out == nil {
		out = []lsp.InlayHint{}
	}
	return out, nil
}

func (s *Server) onSemanticTokens(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode[lsp.DocumentSymbolParams](params)
	sourceFile := s.program.GetSourceFile(compiler.URI(p.TextDocument.URI))
	if sourceFile == nil {
		return lsp.SemanticTokens{Data: []uint{}}, nil
	}
	text := sourceFile.AsSourceFile().Text
	lineStarts := sourceFile.AsSourceFile().LineStarts()
	var data []uint
	prevLine, prevChar := 0, 0
	for _, t := range services.GetSemanticTokens(s.checker, sourceFile) {
		lc := compiler.GetLineAndCharacterOfPosition(text, lineStarts, t.Offset)
		deltaLine := lc.Line - prevLine
		deltaChar := lc.Character
		if deltaLine == 0 {
			deltaChar = lc.Character - prevChar
		}
		data = append(data, uint(deltaLine), uint(deltaChar), uint(t.Length), uint(t.TokenType), uint(t.TokenModifiers))
		prevLine, prevChar = lc.Line, lc.Character
	}
	if data == nil {
		data = []uint{}
	}
	return lsp.SemanticTokens{Data: data}, nil
}

func (s *Server) onCodeLens(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode[lsp.DocumentSymbolParams](params)
	if strings.HasSuffix(p.TextDocument.URI, "/cappu.json") {
		return s.dependencyCodeLenses(compiler.URI(p.TextDocument.URI)), nil
	}
	sourceFile := s.program.GetSourceFile(compiler.URI(p.TextDocument.URI))
	if sourceFile == nil {
		return []lsp.CodeLens{}, nil
	}
	var out []lsp.CodeLens
	for _, entry := range services.GetCodeLenses(s.program, s.checker, sourceFile) {
		r := s.rangeOf(entry.Name)
		n := len(entry.Sites)
		noun := "reference"
		if entry.Kind != "references" {
			noun = "implementation"
		}
		plural := "s"
		if n == 1 {
			plural = ""
		}
		var sites []any
		for _, site := range entry.Sites {
			sites = append(sites, s.locationOf(site))
		}
		out = append(out, lsp.CodeLens{
			Range: r,
			Command: &lsp.Command{
				Title:     itoa(n) + " " + noun + plural,
				Command:   "cappu.showReferences",
				Arguments: []any{p.TextDocument.URI, r.Start, sites},
			},
		})
	}
	if out == nil {
		out = []lsp.CodeLens{}
	}
	return out, nil
}
