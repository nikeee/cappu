package services

// Refactor / quick-fix code actions. Each action is computed as plain text
// changes (offset ranges + replacement text) over a single file, so the logic is
// pure and testable; the server maps them to LSP WorkspaceEdits. Actions are
// offered for a [start, end) selection range in one source file.
// Port of src/services/codeActions.ts.

import (
	"sort"
	"strings"

	"github.com/nikeee/cappu/internal/compiler"
)

func forEachDescendant(node *compiler.Node, cb func(*compiler.Node)) {
	cb(node)
	node.ForEachChild(func(child *compiler.Node) bool {
		forEachDescendant(child, cb)
		return false
	})
}

// TextChange is an offset-range replacement.
type TextChange struct {
	Start   int
	End     int
	NewText string
}

// CodeActionResult is one offered action.
type CodeActionResult struct {
	Title   string
	Kind    string // LSP CodeActionKind, e.g. "quickfix" or "refactor.extract"
	Changes []TextChange
}

func packageOf(fqn string) string {
	dot := strings.LastIndex(fqn, ".")
	if dot < 0 {
		return ""
	}
	return fqn[:dot]
}

func filePackage(sourceFile *compiler.SourceFileData) string {
	if sourceFile.PackageDeclaration != nil {
		return compiler.EntityNameToString(sourceFile.PackageDeclaration.AsPackageDeclaration().Name)
	}
	return ""
}

func singleTypeImportFqns(sourceFile *compiler.SourceFileData) map[string]bool {
	out := map[string]bool{}
	for _, imp := range sourceFile.Imports.Nodes {
		d := imp.AsImportDeclaration()
		if !d.IsStatic && !d.IsOnDemand {
			out[compiler.EntityNameToString(d.Name)] = true
		}
	}
	return out
}

func importInsertion(sourceFile *compiler.SourceFileData, statement string) TextChange {
	if sourceFile.Imports.Len() > 0 {
		last := sourceFile.Imports.Nodes[sourceFile.Imports.Len()-1]
		return TextChange{Start: last.End, End: last.End, NewText: "\n" + statement}
	}
	if sourceFile.PackageDeclaration != nil {
		end := sourceFile.PackageDeclaration.End
		return TextChange{Start: end, End: end, NewText: "\n\n" + statement}
	}
	return TextChange{Start: 0, End: 0, NewText: statement + "\n\n"}
}

func addMissingImport(program *compiler.Program, checker *compiler.Checker, sf *compiler.Node, start int) []CodeActionResult {
	data := sf.AsSourceFile()
	identifier := compiler.GetIdentifierAtPosition(sf, start)
	if identifier == nil || checker.ResolveName(identifier) != nil {
		return nil
	}
	name := identifier.AsIdentifier().Text
	if name == "" {
		return nil
	}
	here := filePackage(data)
	alreadyImported := singleTypeImportFqns(data)
	var candidates []string
	for _, fqn := range program.GetGlobalIndex().FindFqnsBySimpleName(name) {
		pkg := packageOf(string(fqn))
		if pkg != "" && pkg != here && pkg != "java.lang" && !alreadyImported[string(fqn)] {
			candidates = append(candidates, string(fqn))
		}
	}
	sort.Strings(candidates)
	var out []CodeActionResult
	for _, fqn := range candidates {
		out = append(out, CodeActionResult{
			Title:   "Import '" + fqn + "'",
			Kind:    "quickfix",
			Changes: []TextChange{importInsertion(data, "import "+fqn+";")},
		})
	}
	return out
}

func importText(imp *compiler.Node) string {
	d := imp.AsImportDeclaration()
	star := ""
	if d.IsOnDemand {
		star = ".*"
	}
	static := ""
	if d.IsStatic {
		static = "static "
	}
	return "import " + static + compiler.EntityNameToString(d.Name) + star + ";"
}

func organizeImports(sf *compiler.Node) []CodeActionResult {
	data := sf.AsSourceFile()
	imports := data.Imports
	if imports.Len() == 0 {
		return nil
	}
	used := map[string]bool{}
	for _, statement := range data.Statements.Nodes {
		forEachDescendant(statement, func(n *compiler.Node) {
			if n.Kind == compiler.Identifier {
				used[n.AsIdentifier().Text] = true
			}
		})
	}
	var kept []*compiler.Node
	for _, imp := range imports.Nodes {
		d := imp.AsImportDeclaration()
		if d.IsStatic || d.IsOnDemand {
			kept = append(kept, imp)
			continue
		}
		fqn := compiler.EntityNameToString(d.Name)
		if used[fqn[strings.LastIndex(fqn, ".")+1:]] {
			kept = append(kept, imp)
		}
	}
	sorted := append([]*compiler.Node{}, kept...)
	sort.SliceStable(sorted, func(i, j int) bool {
		di, dj := sorted[i].AsImportDeclaration(), sorted[j].AsImportDeclaration()
		if di.IsStatic != dj.IsStatic {
			return !di.IsStatic
		}
		return importText(sorted[i]) < importText(sorted[j])
	})
	start := compiler.SkipTrivia(data.Text, imports.Nodes[0].Pos)
	end := imports.Nodes[imports.Len()-1].End
	var parts []string
	for _, imp := range sorted {
		parts = append(parts, importText(imp))
	}
	newText := strings.Join(parts, "\n")
	if newText == data.Text[start:end] {
		return nil
	}
	return []CodeActionResult{{
		Title:   "Organize imports",
		Kind:    "source.organizeImports",
		Changes: []TextChange{{Start: start, End: end, NewText: newText}},
	}}
}

func isExtractExpressionKind(kind compiler.SyntaxKind) bool {
	switch kind {
	case compiler.BinaryExpression, compiler.CallExpression, compiler.PropertyAccessExpression,
		compiler.ElementAccessExpression, compiler.ParenthesizedExpression, compiler.ConditionalExpression,
		compiler.CastExpression, compiler.ObjectCreationExpression, compiler.ArrayCreationExpression,
		compiler.PrefixUnaryExpression, compiler.PostfixUnaryExpression, compiler.InstanceofExpression,
		compiler.SwitchExpression, compiler.MethodReferenceExpression, compiler.NumericLiteral,
		compiler.StringLiteral, compiler.TextBlockLiteral, compiler.CharacterLiteral:
		return true
	default:
		return false
	}
}

func expressionInRange(sf *compiler.Node, start, end int) *compiler.Node {
	text := sf.AsSourceFile().Text
	var found *compiler.Node
	var visit compiler.Visitor
	visit = func(node *compiler.Node) bool {
		nodeStart := compiler.SkipTrivia(text, node.Pos)
		if nodeStart == start && node.End == end && isExtractExpressionKind(node.Kind) && found == nil {
			found = node
		}
		node.ForEachChild(visit)
		return false
	}
	visit(sf)
	return found
}

func enclosingStatementInBlock(node *compiler.Node) *compiler.Node {
	current := node.Parent
	child := node
	for current != nil {
		if current.Kind == compiler.Block {
			return child
		}
		child = current
		current = current.Parent
	}
	return nil
}

func indentationAt(text string, offset int) string {
	lineStart := strings.LastIndex(text[:offset], "\n") + 1
	return text[lineStart:offset]
}

func extractLocalVariable(sf *compiler.Node, start, end int) []CodeActionResult {
	if end <= start {
		return nil
	}
	text := sf.AsSourceFile().Text
	expression := expressionInRange(sf, start, end)
	if expression == nil {
		return nil
	}
	statement := enclosingStatementInBlock(expression)
	if statement == nil {
		return nil
	}
	statementStart := compiler.SkipTrivia(text, statement.Pos)
	indent := indentationAt(text, statementStart)
	exprText := text[start:end]
	name := "extracted"
	return []CodeActionResult{{
		Title: "Extract local variable",
		Kind:  "refactor.extract",
		Changes: []TextChange{
			{Start: statementStart, End: statementStart, NewText: "var " + name + " = " + exprText + ";\n" + indent},
			{Start: start, End: end, NewText: name},
		},
	}}
}

func needsParentheses(kind compiler.SyntaxKind) bool {
	switch kind {
	case compiler.BinaryExpression, compiler.ConditionalExpression, compiler.InstanceofExpression,
		compiler.AssignmentExpression, compiler.CastExpression, compiler.LambdaExpression:
		return true
	default:
		return false
	}
}

func isAssignmentTarget(use *compiler.Node) bool {
	return use.Parent.Kind == compiler.AssignmentExpression && use.Parent.AsAssignmentExpression().Left == use
}

func inlineLocalVariable(program *compiler.Program, checker *compiler.Checker, sf *compiler.Node, start int) []CodeActionResult {
	text := sf.AsSourceFile().Text
	identifier := compiler.GetIdentifierAtPosition(sf, start)
	if identifier == nil {
		return nil
	}
	symbol := checker.ResolveName(identifier)
	if symbol == nil || symbol.Flags&compiler.SymbolFlagsLocalVariable == 0 {
		return nil
	}
	declarator := symbol.ValueDeclaration
	if declarator == nil || declarator.Kind != compiler.VariableDeclarator {
		return nil
	}
	initializer := declarator.AsVariableDeclarator().Initializer
	if initializer == nil {
		return nil
	}
	statement := declarator.Parent
	if statement.Kind != compiler.LocalVariableDeclarationStatement ||
		statement.AsLocalVariableDeclarationStatement().Declarators.Len() != 1 {
		return nil
	}
	declName := declarator.AsVariableDeclarator().Name
	var uses []*compiler.Node
	for _, node := range compiler.FindReferences(symbol, program, checker.ResolveName) {
		if node != declName {
			uses = append(uses, node)
		}
	}
	for _, use := range uses {
		if isAssignmentTarget(use) {
			return nil
		}
	}
	initText := text[compiler.SkipTrivia(text, initializer.Pos):initializer.End]
	replacement := initText
	if needsParentheses(initializer.Kind) {
		replacement = "(" + initText + ")"
	}
	var changes []TextChange
	for _, use := range uses {
		changes = append(changes, TextChange{Start: compiler.SkipTrivia(text, use.Pos), End: use.End, NewText: replacement})
	}
	statementStart := compiler.SkipTrivia(text, statement.Pos)
	lineStart := strings.LastIndex(text[:statementStart], "\n") + 1
	afterNewline := strings.Index(text[statement.End:], "\n")
	lineEnd := len(text)
	if afterNewline >= 0 {
		lineEnd = statement.End + afterNewline + 1
	}
	changes = append(changes, TextChange{Start: lineStart, End: lineEnd, NewText: ""})
	return []CodeActionResult{{Title: "Inline local variable", Kind: "refactor.inline", Changes: changes}}
}

func listItemRemoval(text string, nodes []*compiler.Node, index int) TextChange {
	startOf := func(n *compiler.Node) int { return compiler.SkipTrivia(text, n.Pos) }
	if len(nodes) == 1 {
		return TextChange{Start: startOf(nodes[0]), End: nodes[0].End, NewText: ""}
	}
	if index < len(nodes)-1 {
		return TextChange{Start: startOf(nodes[index]), End: startOf(nodes[index+1]), NewText: ""}
	}
	return TextChange{Start: nodes[index-1].End, End: nodes[index].End, NewText: ""}
}

func callForMethodReference(reference *compiler.Node) *compiler.Node {
	parent := reference.Parent
	if parent.Kind == compiler.CallExpression && parent.AsCallExpression().Expression == reference {
		return parent
	}
	if parent.Kind == compiler.PropertyAccessExpression && parent.AsPropertyAccessExpression().Name == reference &&
		parent.Parent.Kind == compiler.CallExpression && parent.Parent.AsCallExpression().Expression == parent {
		return parent.Parent
	}
	return nil
}

func removeUnusedParameter(program *compiler.Program, checker *compiler.Checker, sf *compiler.Node, start int) []CodeActionResult {
	data := sf.AsSourceFile()
	identifier := compiler.GetIdentifierAtPosition(sf, start)
	if identifier == nil {
		return nil
	}
	symbol := checker.ResolveName(identifier)
	if symbol == nil || symbol.Flags&compiler.SymbolFlagsParameter == 0 {
		return nil
	}
	parameter := symbol.ValueDeclaration
	if parameter == nil || parameter.Kind != compiler.Parameter {
		return nil
	}
	method := parameter.Parent
	if method.Kind != compiler.MethodDeclaration || method.Symbol == nil {
		return nil
	}
	params := method.AsMethodDeclaration().Parameters
	paramIndex := -1
	for i, p := range params.Nodes {
		if p == parameter {
			paramIndex = i
			break
		}
	}
	if paramIndex < 0 {
		return nil
	}
	paramName := parameter.AsParameter().Name
	uses := 0
	for _, n := range compiler.FindReferences(symbol, program, checker.ResolveName) {
		if n != paramName {
			uses++
		}
	}
	if uses > 0 {
		return nil
	}
	if len(method.Symbol.Declarations) != 1 {
		return nil
	}
	var calls []*compiler.Node
	for _, reference := range compiler.FindReferences(method.Symbol, program, checker.ResolveName) {
		call := callForMethodReference(reference)
		if call == nil {
			continue
		}
		if compiler.GetSourceFileOfNode(call).AsSourceFile().FileName != data.FileName {
			return nil
		}
		if call.AsCallExpression().Arguments.Len() == params.Len() {
			calls = append(calls, call)
		}
	}
	name := paramName.AsIdentifier().Text
	changes := []TextChange{listItemRemoval(data.Text, params.Nodes, paramIndex)}
	for _, call := range calls {
		changes = append(changes, listItemRemoval(data.Text, call.AsCallExpression().Arguments.Nodes, paramIndex))
	}
	return []CodeActionResult{{Title: "Remove unused parameter '" + name + "'", Kind: "refactor.rewrite", Changes: changes}}
}

func removeUnusedImport(sf *compiler.Node, start, end int) []CodeActionResult {
	data := sf.AsSourceFile()
	if len(data.ParseDiagnostics) > 0 {
		return nil
	}
	var out []CodeActionResult
	for _, imp := range compiler.FindUnusedImports(sf) {
		if compiler.SkipTrivia(data.Text, imp.Pos) <= end && start <= imp.End {
			importStart := compiler.SkipTrivia(data.Text, imp.Pos)
			removeEnd := imp.End
			if removeEnd < len(data.Text) && data.Text[removeEnd] == '\r' {
				removeEnd++
			}
			if removeEnd < len(data.Text) && data.Text[removeEnd] == '\n' {
				removeEnd++
			}
			out = append(out, CodeActionResult{
				Title:   "Remove unused import '" + compiler.EntityNameToString(imp.AsImportDeclaration().Name) + "'",
				Kind:    "quickfix",
				Changes: []TextChange{{Start: importStart, End: removeEnd, NewText: ""}},
			})
		}
	}
	return out
}

// enclosingMethod returns the method declaration enclosing a position, or nil.
func enclosingMethod(root *compiler.Node, offset int) *compiler.Node {
	node := compiler.GetNodeAtPosition(root, offset)
	for node != nil && node.Kind != compiler.MethodDeclaration {
		node = node.Parent
	}
	return node
}

// overrideAnnotation returns the @Override annotation among a method's modifiers,
// or nil.
func overrideAnnotation(method *compiler.Node) *compiler.Node {
	mods := method.AsMethodDeclaration().Modifiers
	if mods == nil {
		return nil
	}
	for _, m := range mods.Nodes {
		if m.Kind != compiler.Annotation {
			continue
		}
		name := compiler.EntityNameToString(m.AsAnnotation().TypeName)
		if name == "Override" || strings.HasSuffix(name, ".Override") {
			return m
		}
	}
	return nil
}

// removeRedundantOverride offers to remove an erroneous @Override from a method
// flagged "does not override a supertype method" (1301). Port of
// removeRedundantOverride.
func removeRedundantOverride(checker *compiler.Checker, sf *compiler.Node, start int) []CodeActionResult {
	data := sf.AsSourceFile()
	if len(data.ParseDiagnostics) > 0 {
		return nil
	}
	method := enclosingMethod(sf, start)
	if method == nil {
		return nil
	}
	annotation := overrideAnnotation(method)
	if annotation == nil {
		return nil
	}
	wrong := false
	for _, d := range checker.GetSemanticDiagnostics(sf) {
		if d.Code == compiler.Diagnostics.MethodDoesNotOverrideASupertypeMethod.Code &&
			d.Pos >= method.Pos && d.End <= method.End {
			wrong = true
			break
		}
	}
	if !wrong {
		return nil
	}
	from := compiler.SkipTrivia(data.Text, annotation.Pos)
	to := compiler.SkipTrivia(data.Text, annotation.End)
	return []CodeActionResult{{
		Title:   "Remove redundant '@Override'",
		Kind:    "quickfix",
		Changes: []TextChange{{Start: from, End: to, NewText: ""}},
	}}
}

// GetCodeActions returns all code actions offered for a selection range.
func GetCodeActions(program *compiler.Program, checker *compiler.Checker, sf *compiler.Node, start, end int) []CodeActionResult {
	var out []CodeActionResult
	out = append(out, addMissingImport(program, checker, sf, start)...)
	out = append(out, organizeImports(sf)...)
	out = append(out, extractLocalVariable(sf, start, end)...)
	out = append(out, inlineLocalVariable(program, checker, sf, start)...)
	out = append(out, removeUnusedParameter(program, checker, sf, start)...)
	out = append(out, removeUnusedImport(sf, start, end)...)
	out = append(out, removeRedundantOverride(checker, sf, start)...)
	return out
}
