package services

// Refactor / quick-fix code actions. Each action is computed as plain text
// changes (offset ranges + replacement text) over a single file, so the logic is
// pure and testable; the server maps them to LSP WorkspaceEdits. Actions are
// offered for a [start, end) selection range in one source file.
// Port of src/services/codeActions.ts.

import (
	"cmp"
	"slices"
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
	// AdditionalEdits holds edits to OTHER documents, keyed by uri; nil for
	// single-file actions.
	AdditionalEdits map[string][]TextChange
}

// LanguageFeatures records which language-level features the target Java version
// supports. Computed once (from the configured javac --release) and threaded into
// GetCodeActions, so each modern-Java rewrite just checks a boolean instead of a
// version number.
type LanguageFeatures struct {
	SupportsDiamond           bool // SE7
	SupportsMultiCatch        bool // SE7
	SupportsVar               bool // SE10
	SupportsLambda            bool // SE8
	SupportsArrowSwitch       bool // SE14
	SupportsRecord            bool // SE16
	SupportsInstanceofPattern bool // SE16
}

// NewLanguageFeatures derives the supported features from the configured release
// (javac --release). A nil release means the toolchain default (a modern JDK):
// everything on.
func NewLanguageFeatures(release *int) LanguageFeatures {
	at := func(min int) bool { return release == nil || *release >= min }
	return LanguageFeatures{
		SupportsDiamond:           at(7),
		SupportsMultiCatch:        at(7),
		SupportsVar:               at(10),
		SupportsLambda:            at(8),
		SupportsArrowSwitch:       at(14),
		SupportsRecord:            at(16),
		SupportsInstanceofPattern: at(16),
	}
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
	slices.Sort(candidates)
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
	sorted := slices.Clone(kept)
	slices.SortStableFunc(sorted, func(a, b *compiler.Node) int {
		da, db := a.AsImportDeclaration(), b.AsImportDeclaration()
		if da.IsStatic != db.IsStatic {
			if da.IsStatic {
				return 1
			}
			return -1
		}
		return cmp.Compare(importText(a), importText(b))
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

// makeFieldFinal offers to insert `final` on a field the checker flagged as
// "can be 'final'" (1317). Inserting right before the type lands after all
// existing modifiers/annotations, giving the conventional `private static
// final T` order. Port of makeFieldFinal.
func makeFieldFinal(checker *compiler.Checker, sf *compiler.Node, start int) []CodeActionResult {
	data := sf.AsSourceFile()
	if len(data.ParseDiagnostics) > 0 {
		return nil
	}
	node := compiler.GetNodeAtPosition(sf, start)
	for node != nil && node.Kind != compiler.FieldDeclaration {
		node = node.Parent
	}
	if node == nil {
		return nil
	}
	// Only when the checker actually flagged this field's declarators.
	flagged := false
	for _, d := range checker.GetSemanticDiagnostics(sf) {
		if d.Code == compiler.Diagnostics.Field0CanBeFinal.Code &&
			d.Pos >= node.Pos && d.End <= node.End {
			flagged = true
			break
		}
	}
	if !flagged {
		return nil
	}
	at := compiler.SkipTrivia(data.Text, node.AsFieldDeclaration().Type.Pos)
	return []CodeActionResult{{
		Title:   "Add 'final' modifier",
		Kind:    "quickfix",
		Changes: []TextChange{{Start: at, End: at, NewText: "final "}},
	}}
}

// replaceOptionalIfPresentWithNullCheck offers, for the checker's "can be
// replaced with a null check" warning (1318), the rewrite when it is provably
// safe: the chain is a whole statement, the ofNullable argument is a plain
// variable (so it is evaluated once either way), and the action is a lambda
// whose parameter can be renamed to that variable. Port of
// replaceOptionalIfPresentWithNullCheck.
func replaceOptionalIfPresentWithNullCheck(checker *compiler.Checker, sf *compiler.Node, start int) []CodeActionResult {
	data := sf.AsSourceFile()
	if len(data.ParseDiagnostics) > 0 {
		return nil
	}
	node := compiler.GetNodeAtPosition(sf, start)
	for node != nil && node.Kind != compiler.ExpressionStatement {
		node = node.Parent
	}
	if node == nil {
		return nil
	}
	stmt := node
	// Only when the checker actually flagged this statement's chain (the FQN
	// check against java.util.Optional lives there).
	flagged := false
	for _, d := range checker.GetSemanticDiagnostics(sf) {
		if d.Code == compiler.Diagnostics.OptionalOfNullableIfPresentCanBeReplacedWithANullCheck.Code &&
			d.Pos >= stmt.Pos && d.End <= stmt.End {
			flagged = true
			break
		}
	}
	if !flagged {
		return nil
	}
	expr := stmt.AsExpressionStatement().Expression
	if expr.Kind != compiler.CallExpression {
		return nil
	}
	outer := expr.AsCallExpression()
	if outer.Expression.Kind != compiler.PropertyAccessExpression {
		return nil
	}
	receiver := outer.Expression.AsPropertyAccessExpression().Expression
	if receiver.Kind != compiler.CallExpression {
		return nil
	}
	innerArgs := receiver.AsCallExpression().Arguments
	if innerArgs.Len() != 1 || innerArgs.Nodes[0].Kind != compiler.Identifier {
		return nil // expression: warn only
	}
	variable := innerArgs.Nodes[0].AsIdentifier().Text
	if outer.Arguments.Len() != 1 || outer.Arguments.Nodes[0].Kind != compiler.LambdaExpression {
		return nil // method ref: warn only
	}
	lambda := outer.Arguments.Nodes[0].AsLambdaExpression()
	if lambda.Parameters.Len() != 1 {
		return nil
	}
	param := lambda.Parameters.Nodes[0]
	var paramName string
	switch param.Kind {
	case compiler.Identifier:
		paramName = param.AsIdentifier().Text
	case compiler.Parameter:
		if param.AsParameter().Name == nil {
			return nil
		}
		paramName = param.AsParameter().Name.AsIdentifier().Text
	default:
		return nil
	}

	// Rename lambda-parameter uses to the variable. A use is a plain identifier
	// reference; the member name of `o.v` is not one.
	// ponytail: ignores a shadowing redeclaration of the parameter name inside
	// the body; resolve identifiers through the checker if that ever bites.
	type span struct{ start, end int }
	var renames []span
	if paramName != variable {
		var collect func(n, parent *compiler.Node)
		collect = func(n, parent *compiler.Node) {
			isMemberName := parent.Kind == compiler.PropertyAccessExpression &&
				parent.AsPropertyAccessExpression().Name == n
			if n.Kind == compiler.Identifier && n.AsIdentifier().Text == paramName && !isMemberName {
				renames = append(renames, span{compiler.SkipTrivia(data.Text, n.Pos), n.End})
			}
			n.ForEachChild(func(child *compiler.Node) bool {
				collect(child, n)
				return false
			})
		}
		lambda.Body.ForEachChild(func(child *compiler.Node) bool {
			collect(child, lambda.Body)
			return false
		})
	}
	renamed := func(from, to int) string {
		var out strings.Builder
		at := from
		for _, r := range renames {
			out.WriteString(data.Text[at:r.start])
			out.WriteString(variable)
			at = r.end
		}
		out.WriteString(data.Text[at:to])
		return out.String()
	}
	bodyStart := compiler.SkipTrivia(data.Text, lambda.Body.Pos)
	var body string
	if lambda.Body.Kind == compiler.Block {
		body = renamed(bodyStart, lambda.Body.End) // keeps the `{ ... }`
	} else {
		body = "{ " + renamed(bodyStart, lambda.Body.End) + "; }"
	}
	from := compiler.SkipTrivia(data.Text, stmt.Pos)
	return []CodeActionResult{{
		Title:   "Replace with null check",
		Kind:    "quickfix",
		Changes: []TextChange{{Start: from, End: stmt.End, NewText: "if (" + variable + " != null) " + body}},
	}}
}

// --- size()/length() compared to 0/1 -> isEmpty()/!isEmpty() (nikeee/cappu#42) ---

// countCallReceiver returns the receiver of a zero-arg `size()`/`length()`
// call, or nil. FQN isn't re-checked here: the diagnostic gate below already
// proved it. Port of countCallReceiver in src/services/codeActions.ts.
func countCallReceiver(n *compiler.Node) *compiler.Node {
	if n.Kind != compiler.CallExpression {
		return nil
	}
	call := n.AsCallExpression()
	if call.Arguments.Len() != 0 || call.Expression.Kind != compiler.PropertyAccessExpression {
		return nil
	}
	access := call.Expression.AsPropertyAccessExpression()
	name := access.Name.AsIdentifier().Text
	if name != "size" && name != "length" {
		return nil
	}
	return access.Expression
}

func flipComparison(op compiler.SyntaxKind) compiler.SyntaxKind {
	switch op {
	case compiler.LessThanToken:
		return compiler.GreaterThanToken
	case compiler.GreaterThanToken:
		return compiler.LessThanToken
	case compiler.LessThanEqualsToken:
		return compiler.GreaterThanEqualsToken
	case compiler.GreaterThanEqualsToken:
		return compiler.LessThanEqualsToken
	default:
		return op
	}
}

func countCheckNegates(op compiler.SyntaxKind, literal string) (negate bool, ok bool) {
	switch literal {
	case "0":
		switch op {
		case compiler.EqualsEqualsToken:
			return false, true
		case compiler.ExclamationEqualsToken:
			return true, true
		case compiler.GreaterThanToken:
			return true, true
		case compiler.LessThanEqualsToken:
			return false, true
		}
	case "1":
		switch op {
		case compiler.LessThanToken:
			return false, true
		case compiler.GreaterThanEqualsToken:
			return true, true
		}
	}
	return false, false
}

// Port of replaceCountComparedToZero in src/services/codeActions.ts.
func replaceCountComparedToZero(checker *compiler.Checker, sf *compiler.Node, start int) []CodeActionResult {
	data := sf.AsSourceFile()
	if len(data.ParseDiagnostics) > 0 {
		return nil
	}
	node := compiler.GetNodeAtPosition(sf, start)
	for node != nil && node.Kind != compiler.BinaryExpression {
		node = node.Parent
	}
	if node == nil {
		return nil
	}
	bin := node.AsBinaryExpression()
	flagged := false
	for _, d := range checker.GetSemanticDiagnostics(sf) {
		if d.Code == compiler.Diagnostics.CountCheck0CanBeReplacedWith1.Code &&
			d.Pos >= node.Pos && d.End <= node.End {
			flagged = true
			break
		}
	}
	if !flagged {
		return nil
	}
	receiver := countCallReceiver(bin.Left)
	var literalNode *compiler.Node
	op := bin.OperatorToken
	if receiver != nil && bin.Right.Kind == compiler.NumericLiteral {
		literalNode = bin.Right
	} else {
		receiver = countCallReceiver(bin.Right)
		if receiver == nil || bin.Left.Kind != compiler.NumericLiteral {
			return nil
		}
		literalNode = bin.Left
		op = flipComparison(op)
	}
	negate, ok := countCheckNegates(op, literalNode.AsLiteralExpression().Value)
	if !ok {
		return nil
	}
	receiverStart := compiler.SkipTrivia(data.Text, receiver.Pos)
	receiverText := data.Text[receiverStart:receiver.End]
	binStart := compiler.SkipTrivia(data.Text, node.Pos)
	after := receiverText + ".isEmpty()"
	if negate {
		after = "!" + after
	}
	return []CodeActionResult{{
		Title:   "Replace with isEmpty() check",
		Kind:    "quickfix",
		Changes: []TextChange{{Start: binStart, End: node.End, NewText: after}},
	}}
}

// --- == / != on Strings -> equals() (nikeee/cappu#42) --------------------------

// Expression kinds that can have `.equals(` appended directly without
// changing meaning (Java's primary/postfix expressions). Anything else (a
// binary/unary/ternary/cast/...) must be parenthesized first.
var safeEqualsReceiverKinds = map[compiler.SyntaxKind]bool{
	compiler.Identifier:               true,
	compiler.PropertyAccessExpression: true,
	compiler.CallExpression:           true,
	compiler.ParenthesizedExpression:  true,
	compiler.ThisExpression:           true,
	compiler.StringLiteral:            true,
	compiler.TextBlockLiteral:         true,
	compiler.ObjectCreationExpression: true,
}

// Port of replaceStringEquality in src/services/codeActions.ts.
func replaceStringEquality(checker *compiler.Checker, sf *compiler.Node, start int) []CodeActionResult {
	data := sf.AsSourceFile()
	if len(data.ParseDiagnostics) > 0 {
		return nil
	}
	node := compiler.GetNodeAtPosition(sf, start)
	for node != nil && node.Kind != compiler.BinaryExpression {
		node = node.Parent
	}
	if node == nil {
		return nil
	}
	bin := node.AsBinaryExpression()
	flagged := false
	for _, d := range checker.GetSemanticDiagnostics(sf) {
		if d.Code == compiler.Diagnostics.StringsShouldBeComparedWithEqualsNot0.Code &&
			d.Pos >= node.Pos && d.End <= node.End {
			flagged = true
			break
		}
	}
	if !flagged {
		return nil
	}
	negated := bin.OperatorToken == compiler.ExclamationEqualsToken
	leftStart := compiler.SkipTrivia(data.Text, bin.Left.Pos)
	leftText := data.Text[leftStart:bin.Left.End]
	rightStart := compiler.SkipTrivia(data.Text, bin.Right.Pos)
	rightText := data.Text[rightStart:bin.Right.End]
	receiver := leftText
	if !safeEqualsReceiverKinds[bin.Left.Kind] {
		receiver = "(" + leftText + ")"
	}
	binStart := compiler.SkipTrivia(data.Text, node.Pos)
	newText := receiver + ".equals(" + rightText + ")"
	if negated {
		newText = "!" + newText
	}
	return []CodeActionResult{{
		Title:   "Replace with equals()",
		Kind:    "quickfix",
		Changes: []TextChange{{Start: binStart, End: node.End, NewText: newText}},
	}}
}

// --- boxing constructors (`new Integer(...)`, ...) -> valueOf() (nikeee/cappu#42) ---

// Port of replaceBoxingConstructor in src/services/codeActions.ts.
func replaceBoxingConstructor(checker *compiler.Checker, sf *compiler.Node, start int) []CodeActionResult {
	data := sf.AsSourceFile()
	if len(data.ParseDiagnostics) > 0 {
		return nil
	}
	node := compiler.GetNodeAtPosition(sf, start)
	for node != nil && node.Kind != compiler.ObjectCreationExpression {
		node = node.Parent
	}
	if node == nil {
		return nil
	}
	creation := node.AsObjectCreationExpression()
	flagged := false
	for _, d := range checker.GetSemanticDiagnostics(sf) {
		if d.Code == compiler.Diagnostics.BoxingConstructorNew0IsDeprecated.Code &&
			d.Pos >= node.Pos && d.End <= node.End {
			flagged = true
			break
		}
	}
	if !flagged {
		return nil
	}
	if creation.Type.Kind != compiler.TypeReference {
		return nil
	}
	typeName := compiler.EntityNameToString(creation.Type.AsTypeReference().TypeName)
	from := compiler.SkipTrivia(data.Text, node.Pos)
	return []CodeActionResult{{
		Title:   "Replace with valueOf()",
		Kind:    "quickfix",
		Changes: []TextChange{{Start: from, End: creation.Type.End, NewText: typeName + ".valueOf"}},
	}}
}

// --- indexOf(...) != -1 -> contains(...) (nikeee/cappu#42) -----------------------

func isNegativeOneLiteral(n *compiler.Node) bool {
	return n.Kind == compiler.PrefixUnaryExpression &&
		n.AsPrefixUnaryExpression().Operator == compiler.MinusToken &&
		n.AsPrefixUnaryExpression().Operand.Kind == compiler.NumericLiteral &&
		n.AsPrefixUnaryExpression().Operand.AsLiteralExpression().Value == "1"
}

// indexOfCallExpr returns the `indexOf(...)` call, or nil. FQN isn't
// re-checked here: the diagnostic gate below already proved it.
func indexOfCallExpr(n *compiler.Node) *compiler.Node {
	if n.Kind != compiler.CallExpression {
		return nil
	}
	call := n.AsCallExpression()
	if call.Arguments.Len() != 1 || call.Expression.Kind != compiler.PropertyAccessExpression {
		return nil
	}
	if call.Expression.AsPropertyAccessExpression().Name.AsIdentifier().Text != "indexOf" {
		return nil
	}
	return n
}

// Port of replaceIndexOfComparedToNegativeOne in src/services/codeActions.ts.
func replaceIndexOfComparedToNegativeOne(checker *compiler.Checker, sf *compiler.Node, start int) []CodeActionResult {
	data := sf.AsSourceFile()
	if len(data.ParseDiagnostics) > 0 {
		return nil
	}
	node := compiler.GetNodeAtPosition(sf, start)
	for node != nil && node.Kind != compiler.BinaryExpression {
		node = node.Parent
	}
	if node == nil {
		return nil
	}
	bin := node.AsBinaryExpression()
	flagged := false
	for _, d := range checker.GetSemanticDiagnostics(sf) {
		if d.Code == compiler.Diagnostics.IndexOfCheck0CanBeReplacedWith1.Code &&
			d.Pos >= node.Pos && d.End <= node.End {
			flagged = true
			break
		}
	}
	if !flagged {
		return nil
	}
	var call *compiler.Node
	if isNegativeOneLiteral(bin.Right) {
		call = indexOfCallExpr(bin.Left)
	} else if isNegativeOneLiteral(bin.Left) {
		call = indexOfCallExpr(bin.Right)
	}
	if call == nil {
		return nil
	}
	access := call.AsCallExpression().Expression.AsPropertyAccessExpression()
	receiverStart := compiler.SkipTrivia(data.Text, access.Expression.Pos)
	receiverText := data.Text[receiverStart:access.Expression.End]
	arg := call.AsCallExpression().Arguments.Nodes[0]
	argStart := compiler.SkipTrivia(data.Text, arg.Pos)
	argText := data.Text[argStart:arg.End]
	negate := bin.OperatorToken == compiler.EqualsEqualsToken
	binStart := compiler.SkipTrivia(data.Text, node.Pos)
	newText := receiverText + ".contains(" + argText + ")"
	if negate {
		newText = "!" + newText
	}
	return []CodeActionResult{{
		Title:   "Replace with contains()",
		Kind:    "quickfix",
		Changes: []TextChange{{Start: binStart, End: node.End, NewText: newText}},
	}}
}

// --- redundant new String(...) (nikeee/cappu#42) ---------------------------------

// Port of removeRedundantNewString in src/services/codeActions.ts.
func removeRedundantNewString(checker *compiler.Checker, sf *compiler.Node, start int) []CodeActionResult {
	data := sf.AsSourceFile()
	if len(data.ParseDiagnostics) > 0 {
		return nil
	}
	node := compiler.GetNodeAtPosition(sf, start)
	for node != nil && node.Kind != compiler.ObjectCreationExpression {
		node = node.Parent
	}
	if node == nil {
		return nil
	}
	creation := node.AsObjectCreationExpression()
	flagged := false
	for _, d := range checker.GetSemanticDiagnostics(sf) {
		if d.Code == compiler.Diagnostics.NewString0CanBeReplacedWith1.Code &&
			d.Pos >= node.Pos && d.End <= node.End {
			flagged = true
			break
		}
	}
	if !flagged {
		return nil
	}
	// The diagnostic already proved: 0 args -> "", or 1 String-typed arg -> unwrap it.
	after := `""`
	if creation.Arguments.Len() > 0 {
		arg := creation.Arguments.Nodes[0]
		argStart := compiler.SkipTrivia(data.Text, arg.Pos)
		after = data.Text[argStart:arg.End]
	}
	from := compiler.SkipTrivia(data.Text, node.Pos)
	return []CodeActionResult{{
		Title:   "Remove redundant String wrapper",
		Kind:    "quickfix",
		Changes: []TextChange{{Start: from, End: node.End, NewText: after}},
	}}
}

// --- equals("") -> isEmpty() (nikeee/cappu#42) ------------------------------------

func isEmptyStringLiteral(n *compiler.Node) bool {
	return n.Kind == compiler.StringLiteral && n.AsLiteralExpression().Value == ""
}

// Only the `s.equals("")` direction: `"".equals(s)` is a deliberate null-safe
// idiom whose autofix would change NPE behavior, so it is warn-only (no fix
// offered here - the checker still flags it via the same diagnostic). Port of
// replaceEqualsEmptyString in src/services/codeActions.ts.
func replaceEqualsEmptyString(checker *compiler.Checker, sf *compiler.Node, start int) []CodeActionResult {
	data := sf.AsSourceFile()
	if len(data.ParseDiagnostics) > 0 {
		return nil
	}
	node := compiler.GetNodeAtPosition(sf, start)
	for node != nil && node.Kind != compiler.CallExpression {
		node = node.Parent
	}
	if node == nil {
		return nil
	}
	call := node.AsCallExpression()
	if call.Expression.Kind != compiler.PropertyAccessExpression {
		return nil
	}
	access := call.Expression.AsPropertyAccessExpression()
	if access.Name.AsIdentifier().Text != "equals" || call.Arguments.Len() != 1 {
		return nil
	}
	arg := call.Arguments.Nodes[0]
	if !isEmptyStringLiteral(arg) || isEmptyStringLiteral(access.Expression) {
		return nil
	}
	flagged := false
	for _, d := range checker.GetSemanticDiagnostics(sf) {
		if d.Code == compiler.Diagnostics.EqualsEmpty0CanBeReplacedWith1.Code &&
			d.Pos >= node.Pos && d.End <= node.End {
			flagged = true
			break
		}
	}
	if !flagged {
		return nil
	}
	receiverStart := compiler.SkipTrivia(data.Text, access.Expression.Pos)
	receiverText := data.Text[receiverStart:access.Expression.End]
	callStart := compiler.SkipTrivia(data.Text, node.Pos)
	return []CodeActionResult{{
		Title:   "Replace with isEmpty()",
		Kind:    "quickfix",
		Changes: []TextChange{{Start: callStart, End: node.End, NewText: receiverText + ".isEmpty()"}},
	}}
}

// --- convert a class to a record ---------------------------------------------

func hasKeyword(modifiers *compiler.NodeArray, kind compiler.SyntaxKind) bool {
	if modifiers == nil {
		return false
	}
	for _, m := range modifiers.Nodes {
		if m.Kind == kind {
			return true
		}
	}
	return false
}

func hasAnnotation(modifiers *compiler.NodeArray) bool {
	return hasKeyword(modifiers, compiler.Annotation)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// getterFieldName returns the single field name a trivial getter returns
// (`return f;` / `return this.f;`), or ("", false) if the method is not a plain
// field accessor.
func getterFieldName(method *compiler.Node) (string, bool) {
	m := method.AsMethodDeclaration()
	if m.Body == nil || m.Parameters.Len() > 0 {
		return "", false
	}
	if m.TypeParameters.Len() > 0 || m.Throws.Len() > 0 {
		return "", false
	}
	statements := m.Body.AsBlock().Statements
	if statements.Len() != 1 {
		return "", false
	}
	stmt := statements.Nodes[0]
	if stmt.Kind != compiler.ReturnStatement {
		return "", false
	}
	expr := stmt.AsReturnStatement().Expression
	if expr == nil {
		return "", false
	}
	if expr.Kind == compiler.Identifier {
		return expr.AsIdentifier().Text, true
	}
	if expr.Kind == compiler.PropertyAccessExpression {
		pa := expr.AsPropertyAccessExpression()
		if pa.Expression.Kind == compiler.ThisExpression && pa.Expression.AsThisExpression().Qualifier == nil {
			return pa.Name.AsIdentifier().Text, true
		}
	}
	return "", false
}

// ctorAssignment returns the field a `this.f = p` / `f = p` statement targets
// and the source it reads from, or ("", "", false) for any other shape.
func ctorAssignment(stmt *compiler.Node) (field, from string, ok bool) {
	if stmt.Kind != compiler.ExpressionStatement {
		return "", "", false
	}
	expr := stmt.AsExpressionStatement().Expression
	if expr.Kind != compiler.AssignmentExpression {
		return "", "", false
	}
	assign := expr.AsAssignmentExpression()
	if assign.OperatorToken != compiler.EqualsToken || assign.Right.Kind != compiler.Identifier {
		return "", "", false
	}
	from = assign.Right.AsIdentifier().Text
	left := assign.Left
	if left.Kind == compiler.Identifier {
		return left.AsIdentifier().Text, from, true
	}
	if left.Kind == compiler.PropertyAccessExpression &&
		left.AsPropertyAccessExpression().Expression.Kind == compiler.ThisExpression {
		return left.AsPropertyAccessExpression().Name.AsIdentifier().Text, from, true
	}
	return "", "", false
}

func isBooleanType(t *compiler.Node) bool {
	return t.Kind == compiler.PrimitiveType && t.AsPrimitiveType().Keyword == compiler.BooleanKeyword
}

// convertClassToRecord offers to convert a POJO (final private fields + trivial
// getters + one trivial canonical constructor) into a record, renaming accessor
// call sites across the workspace. Strict: any member that does not fit this
// exact shape suppresses the action, so the rewrite is only offered when it is
// guaranteed safe. Port of src/services/codeActions.ts convertClassToRecord.
func convertClassToRecord(program *compiler.Program, checker *compiler.Checker, sf *compiler.Node, start int) []CodeActionResult {
	data := sf.AsSourceFile()
	if len(data.ParseDiagnostics) > 0 {
		return nil
	}
	node := compiler.GetNodeAtPosition(sf, start)
	for node != nil && node.Kind != compiler.ClassDeclaration {
		node = node.Parent
	}
	if node == nil {
		return nil
	}
	cls := node.AsClassDeclaration()
	text := data.Text

	if hasKeyword(cls.Modifiers, compiler.AbstractKeyword) || cls.ExtendsType != nil {
		return nil
	}
	if node.Parent.Kind != compiler.SourceFile && !hasKeyword(cls.Modifiers, compiler.StaticKeyword) {
		return nil
	}
	if node.Symbol == nil {
		return nil
	}

	// Partition members into fields / getters / the sole constructor. Anything
	// else disqualifies.
	var fields []*compiler.Node
	type getter struct {
		method *compiler.Node
		field  string
	}
	var getters []getter
	var ctor *compiler.Node
	for _, member := range cls.Members.Nodes {
		switch member.Kind {
		case compiler.FieldDeclaration:
			field := member.AsFieldDeclaration()
			if !hasKeyword(field.Modifiers, compiler.PrivateKeyword) ||
				!hasKeyword(field.Modifiers, compiler.FinalKeyword) ||
				hasKeyword(field.Modifiers, compiler.StaticKeyword) ||
				hasAnnotation(field.Modifiers) {
				return nil
			}
			if field.Declarators.Len() != 1 || field.Declarators.Nodes[0].AsVariableDeclarator().Initializer != nil {
				return nil
			}
			fields = append(fields, member)
		case compiler.MethodDeclaration:
			if hasKeyword(member.AsMethodDeclaration().Modifiers, compiler.StaticKeyword) {
				return nil
			}
			name, ok := getterFieldName(member)
			if !ok {
				return nil
			}
			getters = append(getters, getter{member, name})
		case compiler.ConstructorDeclaration:
			if ctor != nil {
				return nil
			}
			ctor = member
		default:
			return nil
		}
	}
	if ctor == nil || ctor.AsConstructorDeclaration().Throws.Len() > 0 {
		return nil
	}

	fieldNames := make([]string, len(fields))
	for i, f := range fields {
		fieldNames[i] = f.AsFieldDeclaration().Declarators.Nodes[0].AsVariableDeclarator().Name.AsIdentifier().Text
	}
	typeText := func(t *compiler.Node) string { return text[compiler.SkipTrivia(text, t.Pos):t.End] }

	// Constructor parameters must equal the fields in declaration order (same
	// type text, same name), so the record's canonical constructor keeps
	// `new C(...)` calls valid without rewriting them.
	params := ctor.AsConstructorDeclaration().Parameters
	if params.Len() != len(fields) {
		return nil
	}
	for i, f := range fields {
		p := params.Nodes[i].AsParameter()
		if p.IsVarArgs || p.Name == nil || p.Name.AsIdentifier().Text != fieldNames[i] ||
			typeText(p.Type) != typeText(f.AsFieldDeclaration().Type) {
			return nil
		}
	}
	// ... and its body must assign every field exactly once from its own parameter.
	body := ctor.AsConstructorDeclaration().Body.AsBlock()
	if body.Statements.Len() != len(fields) {
		return nil
	}
	assigned := map[string]bool{}
	for _, stmt := range body.Statements.Nodes {
		field, from, ok := ctorAssignment(stmt)
		if !ok || !slices.Contains(fieldNames, field) || from != field || assigned[field] {
			return nil
		}
		assigned[field] = true
	}

	// Every getter must map to a declared field and be named getX / isX (isX only
	// for a boolean field).
	for _, g := range getters {
		idx := slices.Index(fieldNames, g.field)
		if idx < 0 {
			return nil
		}
		name := g.method.AsMethodDeclaration().Name.AsIdentifier().Text
		getName := "get" + capitalize(g.field)
		isName := "is" + capitalize(g.field)
		isBool := name == isName && isBooleanType(fields[idx].AsFieldDeclaration().Type)
		if name != getName && !isBool {
			return nil
		}
	}

	// Records are implicitly final: bail if any class in the program extends this one.
	for _, uri := range program.GetAllUris() {
		other := program.GetSourceFile(uri)
		if other == nil {
			continue
		}
		extended := false
		forEachDescendant(other, func(n *compiler.Node) {
			if n.Kind != compiler.ClassDeclaration {
				return
			}
			ext := n.AsClassDeclaration().ExtendsType
			if ext == nil || ext.Kind != compiler.TypeReference {
				return
			}
			tn := ext.AsTypeReference().TypeName
			id := tn
			if tn.Kind != compiler.Identifier {
				id = tn.AsQualifiedName().Right
			}
			if checker.ResolveName(id) == node.Symbol {
				extended = true
			}
		})
		if extended {
			return nil
		}
	}

	// Build the record header, preserving leading modifiers/annotations by
	// starting the replacement at the `class` keyword.
	classKeywordPos := compiler.SkipTrivia(text, node.Pos)
	if cls.Modifiers.Len() > 0 {
		classKeywordPos = compiler.SkipTrivia(text, cls.Modifiers.Nodes[cls.Modifiers.Len()-1].End)
	}
	typeParams := ""
	if cls.TypeParameters.Len() > 0 {
		parts := make([]string, cls.TypeParameters.Len())
		for i, tp := range cls.TypeParameters.Nodes {
			parts[i] = typeText(tp)
		}
		typeParams = "<" + strings.Join(parts, ", ") + ">"
	}
	components := make([]string, len(fields))
	for i, f := range fields {
		components[i] = typeText(f.AsFieldDeclaration().Type) + " " + fieldNames[i]
	}
	impls := ""
	if cls.ImplementsTypes.Len() > 0 {
		parts := make([]string, cls.ImplementsTypes.Len())
		for i, t := range cls.ImplementsTypes.Nodes {
			parts[i] = typeText(t)
		}
		impls = " implements " + strings.Join(parts, ", ")
	}
	header := "record " + cls.Name.AsIdentifier().Text + typeParams + "(" + strings.Join(components, ", ") + ")" + impls + " {\n}"
	changes := []TextChange{{Start: classKeywordPos, End: node.End, NewText: header}}
	additionalEdits := map[string][]TextChange{}

	// Rename accessor call sites getX()/isX() -> x() everywhere, skipping
	// references inside this class (declarations, which are being deleted).
	for _, g := range getters {
		if g.method.Symbol == nil || len(g.method.Symbol.Declarations) != 1 {
			return nil
		}
		for _, ref := range compiler.FindReferences(g.method.Symbol, program, checker.ResolveName) {
			refFile := compiler.GetSourceFileOfNode(ref)
			refData := refFile.AsSourceFile()
			inThisClass := refData.FileName == data.FileName && ref.Pos >= node.Pos && ref.End <= node.End
			if inThisClass {
				continue
			}
			edit := TextChange{Start: compiler.SkipTrivia(refData.Text, ref.Pos), End: ref.End, NewText: g.field}
			if refData.FileName == data.FileName {
				changes = append(changes, edit)
			} else {
				additionalEdits[refData.FileName] = append(additionalEdits[refData.FileName], edit)
			}
		}
	}

	result := CodeActionResult{Title: "Convert class to record", Kind: "refactor.rewrite", Changes: changes}
	if len(additionalEdits) > 0 {
		result.AdditionalEdits = additionalEdits
	}
	return []CodeActionResult{result}
}

// --- use 'var' for a local variable declaration (SE10) -----------------------

// varObviousInitializers are the initializer kinds whose type is obvious from
// the RHS, so replacing the written type with `var` neither hides it from a
// reader nor changes it: these are standalone (non-poly) expressions, so the
// inferred type equals the written one.
var varObviousInitializers = map[compiler.SyntaxKind]bool{
	compiler.ObjectCreationExpression: true,
	compiler.ArrayCreationExpression:  true,
	compiler.CastExpression:           true,
	compiler.NumericLiteral:           true,
	compiler.StringLiteral:            true,
	compiler.TextBlockLiteral:         true,
	compiler.CharacterLiteral:         true,
}

func convertToVar(sf *compiler.Node, start int) []CodeActionResult {
	node := compiler.GetNodeAtPosition(sf, start)
	for node != nil && node.Kind != compiler.LocalVariableDeclarationStatement {
		node = node.Parent
	}
	if node == nil {
		return nil
	}
	decl := node.AsLocalVariableDeclarationStatement()
	if decl.Type.Kind == compiler.VarType { // already `var`
		return nil
	}
	if decl.Declarators == nil || len(decl.Declarators.Nodes) != 1 {
		return nil
	}
	initializer := decl.Declarators.Nodes[0].AsVariableDeclarator().Initializer
	if initializer == nil || !varObviousInitializers[initializer.Kind] {
		return nil
	}
	// `var m = new HashMap<>()` is a compile error: bail on a diamond `new`.
	if initializer.Kind == compiler.ObjectCreationExpression {
		t := initializer.AsObjectCreationExpression().Type
		if t.Kind == compiler.TypeReference {
			ta := t.AsTypeReference().TypeArguments
			if ta != nil && len(ta.Nodes) == 0 {
				return nil
			}
		}
	}
	at := compiler.SkipTrivia(sf.AsSourceFile().Text, decl.Type.Pos)
	return []CodeActionResult{{
		Title:   "Use 'var' for local variable",
		Kind:    "refactor.rewrite",
		Changes: []TextChange{{Start: at, End: decl.Type.End, NewText: "var"}},
	}}
}

// --- convert an anonymous class to a lambda (SE8) ----------------------------

// isObjectMethod reports whether decl is one of the java.lang.Object public
// methods that JLS 9.8 says do NOT count toward a functional interface's single
// abstract method, matched by name and arity.
func isObjectMethod(decl *compiler.MethodDeclarationData) bool {
	name := decl.Name.AsIdentifier().Text
	arity := decl.Parameters.Len()
	return (name == "equals" && arity == 1) ||
		(name == "hashCode" && arity == 0) ||
		(name == "toString" && arity == 0)
}

// isAbstractInterfaceMethod reports whether decl is the abstract kind a lambda
// implements: no body and not a default/static/private interface method.
func isAbstractInterfaceMethod(decl *compiler.MethodDeclarationData) bool {
	if decl.Body != nil {
		return false
	}
	return !hasKeyword(decl.Modifiers, compiler.DefaultKeyword) &&
		!hasKeyword(decl.Modifiers, compiler.StaticKeyword) &&
		!hasKeyword(decl.Modifiers, compiler.PrivateKeyword)
}

// functionalInterfaceSam returns the single abstract method of a functional
// interface (its SAM), searched through inherited interfaces, or nil when the
// type is not a genuine functional interface (zero or more than one abstract
// method). Counts and excludes default/static/private and java.lang.Object
// methods, so it is a correct SAM test. Port of the TS functionalInterfaceSam.
func functionalInterfaceSam(typeSymbol *compiler.Symbol, program *compiler.Program) *compiler.Node {
	type key struct {
		name  string
		arity int
	}
	abstracts := map[key]*compiler.Node{}
	seen := map[*compiler.Symbol]bool{}
	var collect func(sym *compiler.Symbol)
	collect = func(sym *compiler.Symbol) {
		if seen[sym] {
			return
		}
		seen[sym] = true
		for _, member := range sym.Members {
			if member.Flags&compiler.SymbolFlagsMethod == 0 {
				continue
			}
			var decl *compiler.Node
			for _, d := range member.Declarations {
				if d.Kind == compiler.MethodDeclaration {
					decl = d
					break
				}
			}
			if decl == nil {
				continue
			}
			m := decl.AsMethodDeclaration()
			if !isAbstractInterfaceMethod(m) || isObjectMethod(m) {
				continue
			}
			abstracts[key{m.Name.AsIdentifier().Text, m.Parameters.Len()}] = decl
		}
		for _, superSymbol := range compiler.GetDirectSuperTypeSymbols(sym, program) {
			collect(superSymbol)
		}
	}
	collect(typeSymbol)
	if len(abstracts) != 1 {
		return nil
	}
	for _, decl := range abstracts {
		return decl
	}
	return nil
}

// convertAnonymousClassToLambda offers to convert an anonymous class implementing
// a functional interface into a lambda. Strict: exactly one method whose body
// does not reference the anonymous instance (this/super rebind in a lambda), a
// resolvable functional interface, and a matching SAM - so the rewrite is only
// offered when guaranteed safe. Port of the TS convertAnonymousClassToLambda.
func convertAnonymousClassToLambda(program *compiler.Program, sf *compiler.Node, start int) []CodeActionResult {
	data := sf.AsSourceFile()
	if len(data.ParseDiagnostics) > 0 {
		return nil
	}
	node := compiler.GetNodeAtPosition(sf, start)
	for node != nil && node.Kind != compiler.ObjectCreationExpression {
		node = node.Parent
	}
	if node == nil {
		return nil
	}
	oce := node.AsObjectCreationExpression()
	// An interface instantiation takes no constructor arguments and has a body.
	if oce.ClassBody == nil || oce.ClassBody.Len() != 1 || oce.Arguments.Len() > 0 {
		return nil
	}
	member := oce.ClassBody.Nodes[0]
	if member.Kind != compiler.MethodDeclaration {
		return nil
	}
	method := member.AsMethodDeclaration()
	if method.Body == nil {
		return nil
	}

	if oce.Type.Kind != compiler.TypeReference {
		return nil
	}
	typeSymbol := compiler.ResolveTypeEntityName(oce.Type.AsTypeReference().TypeName, node, program)
	if typeSymbol == nil || typeSymbol.Flags&compiler.SymbolFlagsInterface == 0 {
		return nil
	}
	sam := functionalInterfaceSam(typeSymbol, program)
	if sam == nil {
		return nil
	}
	samData := sam.AsMethodDeclaration()
	if samData.Name.AsIdentifier().Text != method.Name.AsIdentifier().Text {
		return nil
	}
	if samData.Parameters.Len() != method.Parameters.Len() {
		return nil
	}

	// Lambda parameters keep only their names (legal against the known target type).
	var params []string
	for _, p := range method.Parameters.Nodes {
		pd := p.AsParameter()
		if pd.IsReceiver || pd.Name == nil {
			return nil
		}
		params = append(params, pd.Name.AsIdentifier().Text)
	}

	// Bail if the body references the anonymous instance: this/super would rebind
	// to the enclosing instance in a lambda, changing semantics.
	referencesInstance := false
	forEachDescendant(method.Body, func(n *compiler.Node) {
		if n.Kind == compiler.ThisExpression || n.Kind == compiler.SuperExpression {
			referencesInstance = true
		}
	})
	if referencesInstance {
		return nil
	}

	text := data.Text
	bodyText := text[compiler.SkipTrivia(text, method.Body.Pos):method.Body.End]
	from := compiler.SkipTrivia(text, node.Pos)
	return []CodeActionResult{{
		Title:   "Convert anonymous class to lambda",
		Kind:    "refactor.rewrite",
		Changes: []TextChange{{Start: from, End: node.End, NewText: "(" + strings.Join(params, ", ") + ") -> " + bodyText}},
	}}
}

// --- convert instanceof + cast to a pattern binding (SE16) -------------------

// convertInstanceofToPattern offers to fold `if (o instanceof T) { T t = (T) o;
// ... }` into a pattern `if (o instanceof T t) { ... }`, deleting the redundant
// cast declaration. Strict: the instanceof must be the whole `if` condition (so
// the binding is in scope for the block) and the first statement must be exactly
// that cast, so the rewrite is only offered when guaranteed safe. Port of the TS
// convertInstanceofToPattern.
func convertInstanceofToPattern(sf *compiler.Node, start int) []CodeActionResult {
	data := sf.AsSourceFile()
	if len(data.ParseDiagnostics) > 0 {
		return nil
	}
	node := compiler.GetNodeAtPosition(sf, start)
	for node != nil && node.Kind != compiler.InstanceofExpression {
		node = node.Parent
	}
	if node == nil {
		return nil
	}
	instanceOf := node.AsInstanceofExpression()
	// Must be a plain type test that is not already a pattern.
	if instanceOf.Type == nil || instanceOf.Name != nil || instanceOf.Pattern != nil {
		return nil
	}

	// It must be exactly the `if` condition (not negated, not a sub-term of
	// &&/||), which keeps the pattern variable's scope trivially correct.
	if node.Parent == nil || node.Parent.Kind != compiler.IfStatement {
		return nil
	}
	ifStmt := node.Parent.AsIfStatement()
	if ifStmt.Condition != node || ifStmt.ThenStatement.Kind != compiler.Block {
		return nil
	}
	block := ifStmt.ThenStatement.AsBlock()
	if block.Statements.Len() == 0 {
		return nil
	}
	first := block.Statements.Nodes[0]
	if first.Kind != compiler.LocalVariableDeclarationStatement {
		return nil
	}
	decl := first.AsLocalVariableDeclarationStatement()
	if (decl.Modifiers != nil && decl.Modifiers.Len() > 0) || decl.Declarators.Len() != 1 {
		return nil
	}
	declarator := decl.Declarators.Nodes[0].AsVariableDeclarator()
	if declarator.ArrayRankAfterName != 0 || declarator.Initializer == nil {
		return nil
	}
	if declarator.Initializer.Kind != compiler.CastExpression {
		return nil
	}
	cast := declarator.Initializer.AsCastExpression()
	if cast.Bounds != nil && cast.Bounds.Len() > 0 { // intersection cast: not a simple binding
		return nil
	}

	text := data.Text
	span := func(n *compiler.Node) string { return text[compiler.SkipTrivia(text, n.Pos):n.End] }
	// The cast must recover exactly the tested type from the tested operand.
	if span(cast.Type) != span(instanceOf.Type) || span(cast.Expression) != span(instanceOf.Expression) {
		return nil
	}

	name := declarator.Name.AsIdentifier().Text
	// Insert ` name` after the tested type, and delete the whole cast-decl line
	// (indentation through the trailing newline). The binding keeps the local's
	// name, so every later use stays valid without a rename.
	lineStart := strings.LastIndex(text[:compiler.SkipTrivia(text, first.Pos)], "\n") + 1
	lineEnd := len(text)
	if nl := strings.Index(text[first.End:], "\n"); nl >= 0 {
		lineEnd = first.End + nl + 1
	}
	return []CodeActionResult{{
		Title: "Replace cast with pattern binding",
		Kind:  "quickfix",
		Changes: []TextChange{
			{Start: instanceOf.Type.End, End: instanceOf.Type.End, NewText: " " + name},
			{Start: lineStart, End: lineEnd, NewText: ""},
		},
	}}
}

// --- use the diamond operator (SE7) ------------------------------------------

// typeArgumentsText returns the explicit type arguments on a generic type
// reference as source text (`<String, Integer>`), or "" and false when there are
// none. Port of the TS typeArgumentsText.
func typeArgumentsText(text string, typ *compiler.Node) (string, bool) {
	if typ.Kind != compiler.TypeReference {
		return "", false
	}
	ref := typ.AsTypeReference()
	if ref.TypeArguments == nil || ref.TypeArguments.Len() == 0 {
		return "", false
	}
	return text[ref.TypeName.End:typ.End], true
}

// convertToDiamond offers to drop redundant type arguments on a `new` whose type
// is fixed by the declared type. Only when the RHS arguments equal the LHS
// arguments, so `<>` infers the same type. Port of the TS convertToDiamond.
func convertToDiamond(sf *compiler.Node, start int) []CodeActionResult {
	node := compiler.GetNodeAtPosition(sf, start)
	for node != nil &&
		node.Kind != compiler.LocalVariableDeclarationStatement &&
		node.Kind != compiler.FieldDeclaration {
		node = node.Parent
	}
	if node == nil {
		return nil
	}
	text := sf.AsSourceFile().Text
	var declType *compiler.Node
	var declarators *compiler.NodeArray
	if node.Kind == compiler.LocalVariableDeclarationStatement {
		d := node.AsLocalVariableDeclarationStatement()
		declType, declarators = d.Type, d.Declarators
	} else {
		d := node.AsFieldDeclaration()
		declType, declarators = d.Type, d.Declarators
	}
	if declarators.Len() != 1 {
		return nil
	}
	lhsArgs, ok := typeArgumentsText(text, declType)
	if !ok { // LHS is not an explicit generic type (e.g. var)
		return nil
	}
	initializer := declarators.Nodes[0].AsVariableDeclarator().Initializer
	if initializer == nil || initializer.Kind != compiler.ObjectCreationExpression {
		return nil
	}
	oce := initializer.AsObjectCreationExpression()
	if oce.ClassBody != nil { // anonymous-class diamond is SE9: stay conservative
		return nil
	}
	if oce.Type.Kind != compiler.TypeReference {
		return nil
	}
	rhsArgs, ok := typeArgumentsText(text, oce.Type)
	if !ok || rhsArgs != lhsArgs { // already <> or a type change
		return nil
	}
	rhs := oce.Type.AsTypeReference()
	return []CodeActionResult{{
		Title:   "Use diamond operator",
		Kind:    "refactor.rewrite",
		Changes: []TextChange{{Start: rhs.TypeName.End, End: oce.Type.End, NewText: "<>"}},
	}}
}

// --- convert a string accumulation to StringBuilder --------------------------

var loopKinds = map[compiler.SyntaxKind]bool{
	compiler.ForStatement:     true,
	compiler.ForEachStatement: true,
	compiler.WhileStatement:   true,
	compiler.DoStatement:      true,
}

func isInsideLoop(node *compiler.Node) bool {
	for n := node.Parent; n != nil; n = n.Parent {
		if loopKinds[n.Kind] {
			return true
		}
	}
	return false
}

func isStringType(typ *compiler.Node) bool {
	if typ.Kind != compiler.TypeReference {
		return false
	}
	name := compiler.EntityNameToString(typ.AsTypeReference().TypeName)
	return name == "String" || name == "java.lang.String"
}

// convertToStringBuilder offers to convert `String s = ""; ... s += x; ...` (with
// an accumulation inside a loop) into a StringBuilder: `s += x` becomes
// `s.append(x)` and every read of `s` becomes `s.toString()`. Strict: every use
// of `s` must be either a plain `s += expr` statement or a read, so the type
// change is always safe. Port of the TS convertToStringBuilder.
func convertToStringBuilder(program *compiler.Program, checker *compiler.Checker, sf *compiler.Node, start int) []CodeActionResult {
	node := compiler.GetNodeAtPosition(sf, start)
	for node != nil && node.Kind != compiler.LocalVariableDeclarationStatement {
		node = node.Parent
	}
	if node == nil {
		return nil
	}
	decl := node.AsLocalVariableDeclarationStatement()
	if decl.Declarators.Len() != 1 || !isStringType(decl.Type) {
		return nil
	}
	declarator := decl.Declarators.Nodes[0].AsVariableDeclarator()
	init := declarator.Initializer
	// Empty-string init: an empty StringBuilder. (A `""` literal only assigns to
	// java.lang.String, so this also proves the declared type.)
	if init == nil || init.Kind != compiler.StringLiteral || init.AsLiteralExpression().Value != "" {
		return nil
	}
	symbol := checker.ResolveName(declarator.Name)
	if symbol == nil || symbol.Flags&compiler.SymbolFlagsLocalVariable == 0 {
		return nil
	}

	var refs []*compiler.Node
	for _, r := range compiler.FindReferences(symbol, program, checker.ResolveName) {
		if r != declarator.Name {
			refs = append(refs, r)
		}
	}
	text := sf.AsSourceFile().Text

	var accumulations []*compiler.Node // AssignmentExpression nodes
	var reads []*compiler.Node
	for _, ref := range refs {
		parent := ref.Parent
		if parent.Kind == compiler.AssignmentExpression && parent.AsAssignmentExpression().Left == ref {
			assign := parent.AsAssignmentExpression()
			if assign.OperatorToken != compiler.PlusEqualsToken { // reset/other assign
				return nil
			}
			if parent.Parent.Kind != compiler.ExpressionStatement { // += used as a value
				return nil
			}
			// The appended expression must not itself read `s`.
			readsS := false
			for _, r := range refs {
				if r != ref && r.Pos >= assign.Right.Pos && r.End <= assign.Right.End {
					readsS = true
					break
				}
			}
			if readsS {
				return nil
			}
			accumulations = append(accumulations, parent)
			continue
		}
		if parent.Kind == compiler.BinaryExpression {
			op := parent.AsBinaryExpression().OperatorToken
			if op == compiler.EqualsEqualsToken || op == compiler.ExclamationEqualsToken {
				return nil
			}
		}
		reads = append(reads, ref)
	}
	inLoop := false
	for _, a := range accumulations {
		if isInsideLoop(a) {
			inLoop = true
			break
		}
	}
	if !inLoop { // the whole point is a loop
		return nil
	}

	name := declarator.Name.AsIdentifier().Text
	changes := []TextChange{
		{Start: compiler.SkipTrivia(text, decl.Type.Pos), End: decl.Type.End, NewText: "StringBuilder"},
		{Start: compiler.SkipTrivia(text, init.Pos), End: init.End, NewText: "new StringBuilder()"},
	}
	for _, node := range accumulations {
		assign := node.AsAssignmentExpression()
		rhs := text[compiler.SkipTrivia(text, assign.Right.Pos):assign.Right.End]
		changes = append(changes, TextChange{
			Start:   compiler.SkipTrivia(text, node.Pos),
			End:     node.End,
			NewText: name + ".append(" + rhs + ")",
		})
	}
	for _, ref := range reads {
		changes = append(changes, TextChange{
			Start:   compiler.SkipTrivia(text, ref.Pos),
			End:     ref.End,
			NewText: name + ".toString()",
		})
	}
	return []CodeActionResult{{Title: "Convert to StringBuilder", Kind: "refactor.rewrite", Changes: changes}}
}

// --- convert a colon switch to an arrow switch (SE14) ------------------------

func terminatesClause(stmt *compiler.Node) bool {
	switch stmt.Kind {
	case compiler.BreakStatement, compiler.ContinueStatement, compiler.ReturnStatement, compiler.ThrowStatement:
		return true
	default:
		return false
	}
}

func isUnlabeledBreak(stmt *compiler.Node) bool {
	return stmt.Kind == compiler.BreakStatement && stmt.AsLabelStatement().Label == nil
}

// hasSwitchBreak reports whether stmt contains an unlabeled `break` that targets
// the enclosing switch (i.e. not one captured by a nested loop or switch). Such a
// break has no arrow equivalent, so its presence suppresses the rewrite.
func hasSwitchBreak(stmt *compiler.Node) bool {
	found := false
	var visit func(n *compiler.Node)
	visit = func(n *compiler.Node) {
		if found {
			return
		}
		if isUnlabeledBreak(n) {
			found = true
			return
		}
		if loopKinds[n.Kind] || n.Kind == compiler.SwitchStatement || n.Kind == compiler.SwitchExpression {
			return // this construct captures its own breaks
		}
		n.ForEachChild(func(c *compiler.Node) bool {
			visit(c)
			return false
		})
	}
	visit(stmt)
	return found
}

type arrowGroup struct {
	label string
	body  []*compiler.Node
}

// convertToArrowSwitch offers to rewrite a classic colon `switch` into the SE14
// arrow form: `case A: foo(); break;` -> `case A -> foo();`, with
// fall-through-only labels merged (`case A: case B:` -> `case A, B ->`). Bails on
// anything the arrow form cannot express: real fall-through, a switch-targeting
// break inside a body, a `default` that falls through, or an SE21 `when` guard.
// Port of the TS convertToArrowSwitch.
func convertToArrowSwitch(sf *compiler.Node, start int) []CodeActionResult {
	data := sf.AsSourceFile()
	if len(data.ParseDiagnostics) > 0 {
		return nil
	}
	node := compiler.GetNodeAtPosition(sf, start)
	for node != nil && node.Kind != compiler.SwitchStatement {
		node = node.Parent
	}
	if node == nil {
		return nil
	}
	sw := node.AsSwitchStatement()
	clauses := sw.Clauses
	if clauses.Len() == 0 {
		return nil
	}
	for _, c := range clauses.Nodes {
		if c.AsSwitchClause().IsArrow {
			return nil // already arrow (or mixed): leave alone
		}
	}

	text := data.Text
	span := func(n *compiler.Node) string { return text[compiler.SkipTrivia(text, n.Pos):n.End] }
	labelsText := func(c *compiler.SwitchClauseData) string {
		if c.Labels == nil {
			return ""
		}
		parts := make([]string, 0, c.Labels.Len())
		for _, l := range c.Labels.Nodes {
			parts = append(parts, span(l))
		}
		return strings.Join(parts, ", ")
	}

	var groups []arrowGroup
	var pending []string // case labels stacked by empty fall-through clauses
	for i, cn := range clauses.Nodes {
		c := cn.AsSwitchClause()
		isLast := i == clauses.Len()-1
		if c.Guard != nil {
			return nil // SE21 guarded pattern: out of scope
		}

		if c.Statements.Len() == 0 {
			if isLast {
				label := "default"
				if !c.IsDefault {
					label = "case " + strings.Join(append(pending, labelsText(c)), ", ")
				}
				groups = append(groups, arrowGroup{label: label})
				pending = nil
			} else {
				if c.IsDefault {
					return nil // default cannot fall through in the arrow form
				}
				pending = append(pending, labelsText(c))
			}
			continue
		}

		stmts := c.Statements.Nodes
		last := stmts[len(stmts)-1]
		if !isLast && !terminatesClause(last) {
			return nil // real fall-through with code
		}
		body := stmts
		if isUnlabeledBreak(last) {
			body = stmts[:len(stmts)-1]
		}
		for _, s := range body {
			if hasSwitchBreak(s) {
				return nil // a break that targets this switch
			}
		}
		if c.IsDefault {
			if len(pending) > 0 {
				return nil // a case fell into default
			}
			groups = append(groups, arrowGroup{label: "default", body: body})
		} else {
			groups = append(groups, arrowGroup{label: "case " + strings.Join(append(pending, labelsText(c)), ", "), body: body})
		}
		pending = nil
	}
	if len(pending) > 0 {
		return nil // labels with no body (defensive)
	}

	switchStart := compiler.SkipTrivia(text, node.Pos)
	indent := indentationAt(text, switchStart)
	caseIndent := indent + "    "
	renderBody := func(body []*compiler.Node) string {
		if len(body) == 0 {
			return "{}"
		}
		only := body[0]
		if len(body) == 1 && (only.Kind == compiler.ExpressionStatement || only.Kind == compiler.ThrowStatement) {
			return span(only)
		}
		// Block form: reindent the original statement lines under the arrow.
		bodyStart := compiler.SkipTrivia(text, only.Pos)
		originalIndent := indentationAt(text, bodyStart)
		inner := caseIndent + "    "
		raw := text[bodyStart:body[len(body)-1].End]
		lines := strings.Split(raw, "\n")
		for idx, line := range lines {
			switch {
			case idx == 0:
				lines[idx] = inner + line
			case strings.HasPrefix(line, originalIndent):
				lines[idx] = inner + line[len(originalIndent):]
			default:
				lines[idx] = inner + strings.TrimLeft(line, " \t")
			}
		}
		return "{\n" + strings.Join(lines, "\n") + "\n" + caseIndent + "}"
	}

	out := []string{"switch (" + span(sw.Expression) + ") {"}
	for _, g := range groups {
		out = append(out, caseIndent+g.label+" -> "+renderBody(g.body))
	}
	out = append(out, indent+"}")

	return []CodeActionResult{{
		Title:   "Convert to arrow switch",
		Kind:    "refactor.rewrite",
		Changes: []TextChange{{Start: switchStart, End: node.End, NewText: strings.Join(out, "\n")}},
	}}
}

// --- merge catch clauses with identical bodies into a multi-catch (SE7) -------

func catchTypeSymbol(clause *compiler.CatchClauseData, program *compiler.Program) *compiler.Symbol {
	if clause.CatchTypes.Len() != 1 {
		return nil
	}
	t := clause.CatchTypes.Nodes[0]
	if t.Kind != compiler.TypeReference {
		return nil
	}
	return compiler.ResolveTypeEntityName(t.AsTypeReference().TypeName, clause.Name, program)
}

// isCatchSubtype reports whether source's class symbol is a subtype of target's
// (walks extends/implements).
func isCatchSubtype(sourceSym, targetSym *compiler.Symbol, program *compiler.Program) bool {
	seen := map[*compiler.Symbol]bool{}
	queue := []*compiler.Symbol{sourceSym}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == targetSym {
			return true
		}
		if seen[cur] {
			continue
		}
		seen[cur] = true
		queue = append(queue, compiler.GetDirectSuperTypeSymbols(cur, program)...)
	}
	return false
}

// mergeCatchClauses merges each maximal run of adjacent catch clauses that share
// the same parameter (name + modifiers) and byte-identical body into one
// `catch (A | B e)`. A union alternative may not be a subtype of another
// (JLS 14.20), so the caught types must resolve and be pairwise unrelated -
// otherwise that run is skipped. Port of the TS mergeCatchClauses.
func mergeCatchClauses(program *compiler.Program, sf *compiler.Node, start int) []CodeActionResult {
	data := sf.AsSourceFile()
	if len(data.ParseDiagnostics) > 0 {
		return nil
	}
	node := compiler.GetNodeAtPosition(sf, start)
	for node != nil && node.Kind != compiler.TryStatement {
		node = node.Parent
	}
	if node == nil {
		return nil
	}
	clauses := node.AsTryStatement().CatchClauses
	if clauses.Len() < 2 {
		return nil
	}

	text := data.Text
	span := func(n *compiler.Node) string { return text[compiler.SkipTrivia(text, n.Pos):n.End] }
	modText := func(c *compiler.CatchClauseData) string {
		if c.Modifiers == nil || c.Modifiers.Len() == 0 {
			return ""
		}
		return text[compiler.SkipTrivia(text, c.Modifiers.Nodes[0].Pos):c.Modifiers.Nodes[c.Modifiers.Len()-1].End]
	}
	mergeable := func(a, b *compiler.CatchClauseData) bool {
		return a.CatchTypes.Len() == 1 && b.CatchTypes.Len() == 1 &&
			a.Name.AsIdentifier().Text == b.Name.AsIdentifier().Text &&
			modText(a) == modText(b) && span(a.Block) == span(b.Block)
	}

	var actions []CodeActionResult
	i := 0
	for i < clauses.Len() {
		j := i + 1
		for j < clauses.Len() && mergeable(clauses.Nodes[j-1].AsCatchClause(), clauses.Nodes[j].AsCatchClause()) {
			j++
		}
		if j-i >= 2 {
			run := clauses.Nodes[i:j]
			syms := make([]*compiler.Symbol, len(run))
			allResolved := true
			for k, c := range run {
				syms[k] = catchTypeSymbol(c.AsCatchClause(), program)
				if syms[k] == nil {
					allResolved = false
				}
			}
			ok := allResolved
			for a := 0; ok && a < len(syms); a++ {
				for b := a + 1; ok && b < len(syms); b++ {
					if isCatchSubtype(syms[a], syms[b], program) || isCatchSubtype(syms[b], syms[a], program) {
						ok = false
					}
				}
			}
			if ok {
				first := run[0].AsCatchClause()
				prefix := ""
				if m := modText(first); m != "" {
					prefix = m + " "
				}
				types := make([]string, len(run))
				for k, c := range run {
					types[k] = span(c.AsCatchClause().CatchTypes.Nodes[0])
				}
				actions = append(actions, CodeActionResult{
					Title: "Merge catch clauses",
					Kind:  "refactor.rewrite",
					Changes: []TextChange{{
						Start:   compiler.SkipTrivia(text, run[0].Pos),
						End:     run[len(run)-1].End,
						NewText: "catch (" + prefix + strings.Join(types, " | ") + " " + first.Name.AsIdentifier().Text + ") " + span(first.Block),
					}},
				})
			}
		}
		i = j
	}
	return actions
}

// GetCodeActions returns all code actions offered for a selection range. features
// gates modern-Java rewrites to the target version that supports them.
func GetCodeActions(program *compiler.Program, checker *compiler.Checker, sf *compiler.Node, start, end int, features LanguageFeatures) []CodeActionResult {
	var out []CodeActionResult
	out = append(out, addMissingImport(program, checker, sf, start)...)
	out = append(out, organizeImports(sf)...)
	// extract-local emits a `var` declaration (SE10).
	if features.SupportsVar {
		out = append(out, extractLocalVariable(sf, start, end)...)
	}
	out = append(out, inlineLocalVariable(program, checker, sf, start)...)
	out = append(out, removeUnusedParameter(program, checker, sf, start)...)
	out = append(out, removeUnusedImport(sf, start, end)...)
	out = append(out, removeRedundantOverride(checker, sf, start)...)
	out = append(out, makeFieldFinal(checker, sf, start)...)
	out = append(out, replaceOptionalIfPresentWithNullCheck(checker, sf, start)...)
	out = append(out, replaceCountComparedToZero(checker, sf, start)...)
	out = append(out, replaceStringEquality(checker, sf, start)...)
	out = append(out, replaceBoxingConstructor(checker, sf, start)...)
	out = append(out, replaceIndexOfComparedToNegativeOne(checker, sf, start)...)
	out = append(out, removeRedundantNewString(checker, sf, start)...)
	out = append(out, replaceEqualsEmptyString(checker, sf, start)...)
	if features.SupportsRecord {
		out = append(out, convertClassToRecord(program, checker, sf, start)...)
	}
	if features.SupportsVar {
		out = append(out, convertToVar(sf, start)...)
	}
	if features.SupportsLambda {
		out = append(out, convertAnonymousClassToLambda(program, sf, start)...)
	}
	if features.SupportsInstanceofPattern {
		out = append(out, convertInstanceofToPattern(sf, start)...)
	}
	if features.SupportsDiamond {
		out = append(out, convertToDiamond(sf, start)...)
	}
	if features.SupportsArrowSwitch {
		out = append(out, convertToArrowSwitch(sf, start)...)
	}
	if features.SupportsMultiCatch {
		out = append(out, mergeCatchClauses(program, sf, start)...)
	}
	out = append(out, convertToStringBuilder(program, checker, sf, start)...)
	return out
}
