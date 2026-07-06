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
	SupportsVar               bool // SE10
	SupportsLambda            bool // SE8
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
		SupportsVar:               at(10),
		SupportsLambda:            at(8),
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
	out = append(out, convertToStringBuilder(program, checker, sf, start)...)
	return out
}
