package mcp

// MCP tool handlers: a thin, name-addressed, JSON-returning layer over the
// engine (Program + Checker). Handlers are pure over the current Program state;
// disk freshness and transport live in server.go. Locations are 1-based and use
// filesystem paths so agents can act on them directly.
// Port of src/services/mcp.ts.

import (
	"slices"
	"strings"

	"github.com/nikeee/cappu/internal/compiler"
	"github.com/nikeee/cappu/internal/lsp"
	"github.com/nikeee/cappu/internal/services"
)

// McpLocation is a 1-based file location range.
type McpLocation struct {
	File      string `json:"file"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	EndLine   int    `json:"endLine"`
	EndColumn int    `json:"endColumn"`
}

// McpDiagnostic is a 1-based diagnostic.
type McpDiagnostic struct {
	File      string `json:"file"`
	Severity  string `json:"severity"`
	Code      int    `json:"code"`
	Message   string `json:"message"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	EndLine   int    `json:"endLine"`
	EndColumn int    `json:"endColumn"`
}

// McpMatch describes a symbol.
type McpMatch struct {
	Kind          string       `json:"kind"`
	Label         string       `json:"label"`
	Signature     string       `json:"signature,omitempty"`
	Documentation string       `json:"documentation,omitempty"`
	Definition    *McpLocation `json:"definition,omitempty"`
}

// McpMember is a member match plus whether it is inherited.
type McpMember struct {
	McpMatch
	Inherited bool `json:"inherited"`
}

// McpEdit is a rename edit (a location plus its replacement text).
type McpEdit struct {
	McpLocation
	NewText string `json:"newText"`
}

func severityOf(category compiler.DiagnosticCategory) string {
	switch category {
	case compiler.CategoryError:
		return "error"
	case compiler.CategoryWarning:
		return "warning"
	default:
		return "info"
	}
}

// displayFile surfaces a file:// uri as a plain path, keeping synthetic stub
// uris (jdk:///, classpath:///) verbatim.
func displayFile(uri string) string {
	if compiler.IsSyntheticURI(compiler.URI(uri)) {
		return uri
	}
	return uriToPath(uri)
}

func uriToPath(uri string) string {
	return strings.TrimPrefix(uri, "file://")
}

func pathToURI(path string) compiler.URI {
	return compiler.URI("file://" + path)
}

// nodeLocation reports a node's name location (skipping leading trivia).
func nodeLocation(node *compiler.Node) McpLocation {
	file := compiler.GetSourceFileOfNode(node).AsSourceFile()
	lineStarts := file.LineStarts()
	start := compiler.GetLineAndCharacterOfPosition(file.Text, lineStarts, compiler.SkipTrivia(file.Text, node.Pos))
	end := compiler.GetLineAndCharacterOfPosition(file.Text, lineStarts, node.End)
	return McpLocation{
		File:      displayFile(file.FileName),
		Line:      start.Line + 1,
		Column:    start.Character + 1,
		EndLine:   end.Line + 1,
		EndColumn: end.Character + 1,
	}
}

func formatDiagnostic(uri string, d compiler.Diagnostic, text string, lineStarts []int) McpDiagnostic {
	start := compiler.GetLineAndCharacterOfPosition(text, lineStarts, d.Pos)
	end := compiler.GetLineAndCharacterOfPosition(text, lineStarts, d.End)
	return McpDiagnostic{
		File:      displayFile(uri),
		Severity:  severityOf(d.Category),
		Code:      int(d.Code),
		Message:   d.MessageText,
		Line:      start.Line + 1,
		Column:    start.Character + 1,
		EndLine:   end.Line + 1,
		EndColumn: end.Character + 1,
	}
}

// Tools is the engine-backed MCP tool surface.
type Tools struct {
	program  *compiler.Program
	checker  *compiler.Checker
	features services.LanguageFeatures
}

// NewTools builds the tool surface over a program and checker.
func NewTools(program *compiler.Program, checker *compiler.Checker, features services.LanguageFeatures) *Tools {
	return &Tools{program: program, checker: checker, features: features}
}

// Diagnostics reports parse/bind/semantic diagnostics for the given files (all
// files when none are named).
func (t *Tools) Diagnostics(files []string) []McpDiagnostic {
	var uris []compiler.URI
	if len(files) > 0 {
		for _, f := range files {
			uris = append(uris, pathToURI(f))
		}
	} else {
		uris = t.program.GetAllUris()
	}
	out := []McpDiagnostic{}
	for _, uri := range uris {
		sourceFile := t.program.GetSourceFile(uri)
		if sourceFile == nil {
			continue
		}
		data := sourceFile.AsSourceFile()
		lineStarts := data.LineStarts()
		all := append(append(append([]compiler.Diagnostic{}, data.ParseDiagnostics...), data.BindDiagnostics...), t.checker.GetSemanticDiagnostics(sourceFile)...)
		for _, d := range all {
			out = append(out, formatDiagnostic(string(uri), d, data.Text, lineStarts))
		}
	}
	return out
}

// McpDeprecatedUse is a use of a @Deprecated method or type.
type McpDeprecatedUse struct {
	File       string `json:"file"`
	Line       int    `json:"line"`
	Column     int    `json:"column"`
	EndLine    int    `json:"endLine"`
	EndColumn  int    `json:"endColumn"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Since      string `json:"since,omitempty"`
	ForRemoval bool   `json:"forRemoval"`
	Message    string `json:"message"`
}

// DeprecatedUses finds uses of @Deprecated methods and types across the given
// files (all files when none are named), with each declaration's since/forRemoval.
func (t *Tools) DeprecatedUses(files []string) []McpDeprecatedUse {
	var uris []compiler.URI
	if len(files) > 0 {
		for _, f := range files {
			uris = append(uris, pathToURI(f))
		}
	} else {
		uris = t.program.GetAllUris()
	}
	out := []McpDeprecatedUse{}
	for _, uri := range uris {
		sourceFile := t.program.GetSourceFile(uri)
		if sourceFile == nil {
			continue
		}
		data := sourceFile.AsSourceFile()
		lineStarts := data.LineStarts()
		for _, u := range t.checker.GetDeprecatedUses(sourceFile) {
			start := compiler.GetLineAndCharacterOfPosition(data.Text, lineStarts, u.Pos)
			end := compiler.GetLineAndCharacterOfPosition(data.Text, lineStarts, u.End)
			kindWord := map[string]string{"method": "Method", "type": "Type", "field": "Field"}[u.Kind]
			message := kindWord + " '" + u.Name + "' is deprecated"
			if u.HasSince {
				message += " (since " + u.Since + ")"
			}
			if u.ForRemoval {
				message += "; marked for removal"
			}
			message += "."
			out = append(out, McpDeprecatedUse{
				File:       displayFile(string(uri)),
				Line:       start.Line + 1,
				Column:     start.Character + 1,
				EndLine:    end.Line + 1,
				EndColumn:  end.Character + 1,
				Name:       u.Name,
				Kind:       u.Kind,
				Since:      u.Since,
				ForRemoval: u.ForRemoval,
				Message:    message,
			})
		}
	}
	return out
}

// Outline returns the top-level outline of a file.
func (t *Tools) Outline(file string) []lsp.DocumentSymbol {
	sourceFile := t.program.GetSourceFile(pathToURI(file))
	if sourceFile == nil {
		return []lsp.DocumentSymbol{}
	}
	return services.GetDocumentSymbols(sourceFile, sourceFile.AsSourceFile().LineStarts())
}

// SearchSymbols matches type FQNs case-insensitively by substring.
func (t *Tools) SearchSymbols(query string) []string {
	q := strings.ToLower(query)
	var matches []string
	for _, fqn := range t.program.GetGlobalIndex().GetAllTypeFqns() {
		if strings.Contains(strings.ToLower(string(fqn)), q) {
			matches = append(matches, string(fqn))
		}
	}
	slices.Sort(matches) // Go maps are unordered; sort for a stable result
	return matches
}

func (t *Tools) describe(symbol *compiler.Symbol) McpMatch {
	declaration := compiler.GetDeclarationNameNode(symbol)
	m := McpMatch{
		Kind:  services.SymbolKindWord(symbol.Flags),
		Label: services.GetHoverText(t.checker, symbol, nil),
	}
	if signature, ok := t.checker.SignatureOfSymbol(symbol); ok {
		m.Signature = signature
	}
	if documentation, ok := t.checker.GetDocumentation(symbol); ok {
		m.Documentation = documentation
	}
	if declaration != nil {
		loc := nodeLocation(declaration)
		m.Definition = &loc
	}
	return m
}

// DescribeSymbol returns kind/label/signature/definition for each symbol a ref
// resolves to.
func (t *Tools) DescribeSymbol(ref string) []McpMatch {
	out := []McpMatch{}
	for _, symbol := range ResolveSymbolRef(ref, t.program.GetGlobalIndex()) {
		out = append(out, t.describe(symbol))
	}
	return out
}

// FindDefinition returns the declaration location of each symbol a ref resolves to.
func (t *Tools) FindDefinition(ref string) []McpLocation {
	out := []McpLocation{}
	for _, symbol := range ResolveSymbolRef(ref, t.program.GetGlobalIndex()) {
		if declaration := compiler.GetDeclarationNameNode(symbol); declaration != nil {
			out = append(out, nodeLocation(declaration))
		}
	}
	return out
}

// ambiguity reports >1 candidate as ambiguous, exactly-1 as the resolved symbol.
func (t *Tools) resolveOne(ref string) (*compiler.Symbol, int) {
	symbols := ResolveSymbolRef(ref, t.program.GetGlobalIndex())
	if len(symbols) == 1 {
		return symbols[0], 1
	}
	return nil, len(symbols)
}

// FindReferencesResult is the references tool result.
type FindReferencesResult struct {
	References []McpLocation `json:"references"`
	Ambiguous  bool          `json:"ambiguous,omitempty"`
	Candidates int           `json:"candidates,omitempty"`
}

// FindReferences returns every use of the symbol a ref resolves to.
func (t *Tools) FindReferences(ref string) FindReferencesResult {
	symbol, count := t.resolveOne(ref)
	if symbol == nil {
		if count > 1 {
			return FindReferencesResult{References: []McpLocation{}, Ambiguous: true, Candidates: count}
		}
		return FindReferencesResult{References: []McpLocation{}}
	}
	refs := []McpLocation{}
	for _, node := range compiler.FindReferences(symbol, t.program, t.checker.ResolveName) {
		refs = append(refs, nodeLocation(node))
	}
	return FindReferencesResult{References: refs}
}

// FindImplementationsResult is the implementations tool result.
type FindImplementationsResult struct {
	Implementations []McpMatch `json:"implementations"`
	Ambiguous       bool       `json:"ambiguous,omitempty"`
	Candidates      int        `json:"candidates,omitempty"`
}

// FindImplementations returns the subtypes of a type, or the overrides of a method.
func (t *Tools) FindImplementations(ref string) FindImplementationsResult {
	symbol, count := t.resolveOne(ref)
	if symbol == nil {
		if count > 1 {
			return FindImplementationsResult{Implementations: []McpMatch{}, Ambiguous: true, Candidates: count}
		}
		return FindImplementationsResult{Implementations: []McpMatch{}}
	}
	var impls []*compiler.Symbol
	if symbol.Flags&(compiler.SymbolFlagsMethod|compiler.SymbolFlagsConstructor) != 0 {
		if declaration := symbolDeclaration(symbol); declaration != nil {
			for _, override := range services.FindMethodImplementations(declaration, t.program) {
				if override.Symbol != nil {
					impls = append(impls, override.Symbol)
				}
			}
		}
	} else {
		impls = services.GetSubtypeIndex(t.program).AllSubtypesOf(symbol)
	}
	out := []McpMatch{}
	for _, s := range impls {
		out = append(out, t.describe(s))
	}
	return FindImplementationsResult{Implementations: out}
}

func symbolDeclaration(symbol *compiler.Symbol) *compiler.Node {
	if symbol.ValueDeclaration != nil {
		return symbol.ValueDeclaration
	}
	if len(symbol.Declarations) > 0 {
		return symbol.Declarations[0]
	}
	return nil
}

// supertypesOf returns the transitive supertypes of a type, nearest first, deduped.
func (t *Tools) supertypesOf(typeSymbol *compiler.Symbol) []*compiler.Symbol {
	var out []*compiler.Symbol
	seen := map[*compiler.Symbol]bool{typeSymbol: true}
	queue := append([]*compiler.Symbol{}, compiler.GetDirectSuperTypeSymbols(typeSymbol, t.program)...)
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if seen[next] {
			continue
		}
		seen[next] = true
		out = append(out, next)
		queue = append(queue, compiler.GetDirectSuperTypeSymbols(next, t.program)...)
	}
	return out
}

// ListMembersResult is the members tool result.
type ListMembersResult struct {
	Members    []McpMember `json:"members"`
	Ambiguous  bool        `json:"ambiguous,omitempty"`
	Candidates int         `json:"candidates,omitempty"`
}

// ListMembers returns declared and inherited members with an inherited flag.
func (t *Tools) ListMembers(ref string) ListMembersResult {
	symbol, count := t.resolveOne(ref)
	if symbol == nil {
		if count > 1 {
			return ListMembersResult{Members: []McpMember{}, Ambiguous: true, Candidates: count}
		}
		return ListMembersResult{Members: []McpMember{}}
	}
	members := []McpMember{}
	seenNames := map[string]bool{}
	addFrom := func(typeSymbol *compiler.Symbol, inherited bool) {
		for name, member := range typeSymbol.Members {
			if seenNames[name] {
				continue
			}
			seenNames[name] = true
			members = append(members, McpMember{McpMatch: t.describe(member), Inherited: inherited})
		}
	}
	addFrom(symbol, false)
	for _, superType := range t.supertypesOf(symbol) {
		addFrom(superType, true)
	}
	return ListMembersResult{Members: members}
}

// FindCallersResult is the callers tool result.
type FindCallersResult struct {
	Callers    []McpLocation `json:"callers"`
	Ambiguous  bool          `json:"ambiguous,omitempty"`
	Candidates int           `json:"candidates,omitempty"`
}

// FindCallers returns the call sites of a method.
func (t *Tools) FindCallers(ref string) FindCallersResult {
	symbol, count := t.resolveOne(ref)
	if symbol == nil {
		if count > 1 {
			return FindCallersResult{Callers: []McpLocation{}, Ambiguous: true, Candidates: count}
		}
		return FindCallersResult{Callers: []McpLocation{}}
	}
	callers := []McpLocation{}
	for _, node := range compiler.FindReferences(symbol, t.program, t.checker.ResolveName) {
		if services.EnclosingCall(node) != nil {
			callers = append(callers, nodeLocation(node))
		}
	}
	return FindCallersResult{Callers: callers}
}

// TypeHierarchyResult is the type-hierarchy tool result.
type TypeHierarchyResult struct {
	Supertypes []McpMatch `json:"supertypes"`
	Subtypes   []McpMatch `json:"subtypes"`
	Ambiguous  bool       `json:"ambiguous,omitempty"`
	Candidates int        `json:"candidates,omitempty"`
}

// TypeHierarchy returns the transitive supertypes and subtypes of a type.
func (t *Tools) TypeHierarchy(ref string) TypeHierarchyResult {
	symbol, count := t.resolveOne(ref)
	if symbol == nil {
		if count > 1 {
			return TypeHierarchyResult{Supertypes: []McpMatch{}, Subtypes: []McpMatch{}, Ambiguous: true, Candidates: count}
		}
		return TypeHierarchyResult{Supertypes: []McpMatch{}, Subtypes: []McpMatch{}}
	}
	supertypes := []McpMatch{}
	for _, s := range t.supertypesOf(symbol) {
		supertypes = append(supertypes, t.describe(s))
	}
	subtypes := []McpMatch{}
	for _, s := range services.GetSubtypeIndex(t.program).AllSubtypesOf(symbol) {
		subtypes = append(subtypes, t.describe(s))
	}
	return TypeHierarchyResult{Supertypes: supertypes, Subtypes: subtypes}
}

// ResolveImport returns the import candidates (dotted FQNs) for a simple type name.
func (t *Tools) ResolveImport(name string) []string {
	imports := []string{}
	for _, fqn := range t.program.GetGlobalIndex().FindFqnsBySimpleName(name) {
		if strings.Contains(string(fqn), ".") {
			imports = append(imports, string(fqn))
		}
	}
	slices.Sort(imports)
	return imports
}

// RenameSymbolResult is the rename tool result.
type RenameSymbolResult struct {
	Edits      []McpEdit `json:"edits"`
	Error      string    `json:"error,omitempty"`
	Ambiguous  bool      `json:"ambiguous,omitempty"`
	Candidates int       `json:"candidates,omitempty"`
}

// RenameSymbol returns the edits a workspace rename would make (it never writes).
func (t *Tools) RenameSymbol(ref, newName string) RenameSymbolResult {
	if !compiler.IsValidIdentifier(newName) {
		return RenameSymbolResult{Edits: []McpEdit{}, Error: "'" + newName + "' is not a valid Java identifier."}
	}
	symbol, count := t.resolveOne(ref)
	if symbol == nil {
		if count > 1 {
			return RenameSymbolResult{Edits: []McpEdit{}, Ambiguous: true, Candidates: count}
		}
		return RenameSymbolResult{Edits: []McpEdit{}}
	}
	if compiler.IsStubSymbol(symbol) {
		return RenameSymbolResult{Edits: []McpEdit{}, Error: "Cannot rename a symbol defined by the JDK."}
	}
	edits := []McpEdit{}
	for _, node := range compiler.FindReferences(symbol, t.program, t.checker.ResolveName) {
		edits = append(edits, McpEdit{McpLocation: nodeLocation(node), NewText: newName})
	}
	return RenameSymbolResult{Edits: edits}
}

// McpCodeAction is one offered code action (refactoring or quick fix), with its
// edits as 1-based locations for the agent to apply.
type McpCodeAction struct {
	Title string `json:"title"`
	// Kind is an LSP CodeActionKind, e.g. "quickfix" or "refactor.extract".
	Kind  string    `json:"kind"`
	Edits []McpEdit `json:"edits"`
	// AdditionalEdits holds edits to other files, keyed by path; unset for
	// single-file actions.
	AdditionalEdits map[string][]McpEdit `json:"additionalEdits,omitempty"`
}

// CodeActionsResult wraps the offered actions.
type CodeActionsResult struct {
	Actions []McpCodeAction `json:"actions"`
}

// CodeActions returns the refactorings and quick fixes offered for a selection
// range in a file, mirroring the LSP codeAction provider. Edits are returned for
// the agent to apply itself; nothing is written. Positions are 1-based (as
// elsewhere here); endLine/endColumn default to the start for a collapsed caret.
func (t *Tools) CodeActions(file string, startLine, startColumn int, endLine, endColumn *int) CodeActionsResult {
	sourceFile := t.program.GetSourceFile(pathToURI(file))
	if sourceFile == nil {
		return CodeActionsResult{Actions: []McpCodeAction{}}
	}
	text := sourceFile.AsSourceFile().Text
	lineStarts := sourceFile.AsSourceFile().LineStarts()
	el, ec := startLine, startColumn
	if endLine != nil {
		el = *endLine
	}
	if endColumn != nil {
		ec = *endColumn
	}
	start := compiler.GetPositionOfLineAndCharacter(text, lineStarts, startLine-1, startColumn-1)
	end := compiler.GetPositionOfLineAndCharacter(text, lineStarts, el-1, ec-1)
	mapEdits := func(f, txt string, ls []int, cs []services.TextChange) []McpEdit {
		edits := []McpEdit{}
		for _, c := range cs {
			s := compiler.GetLineAndCharacterOfPosition(txt, ls, c.Start)
			e := compiler.GetLineAndCharacterOfPosition(txt, ls, c.End)
			edits = append(edits, McpEdit{
				McpLocation: McpLocation{
					File:      f,
					Line:      s.Line + 1,
					Column:    s.Character + 1,
					EndLine:   e.Line + 1,
					EndColumn: e.Character + 1,
				},
				NewText: c.NewText,
			})
		}
		return edits
	}
	actions := []McpCodeAction{}
	for _, action := range services.GetCodeActions(t.program, t.checker, sourceFile, start, end, t.features) {
		result := McpCodeAction{
			Title: action.Title,
			Kind:  action.Kind,
			Edits: mapEdits(displayFile(sourceFile.AsSourceFile().FileName), text, lineStarts, action.Changes),
		}
		if len(action.AdditionalEdits) > 0 {
			additional := map[string][]McpEdit{}
			for uri, cs := range action.AdditionalEdits {
				other := t.program.GetSourceFile(compiler.URI(uri))
				if other == nil {
					continue
				}
				path := displayFile(uri)
				additional[path] = mapEdits(path, other.AsSourceFile().Text, other.AsSourceFile().LineStarts(), cs)
			}
			if len(additional) > 0 {
				result.AdditionalEdits = additional
			}
		}
		actions = append(actions, result)
	}
	return CodeActionsResult{Actions: actions}
}
