package lspserver

// Java language server. The LSP protocol/transport is handled by internal/lsp;
// everything semantic comes from the Program (parse+bind, cached per document
// version), the checker, and the language-services layer. Port of
// src/services/server.ts.

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"

	"github.com/nikeee/cappu/internal/compiler"
	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/lsp"
	"github.com/nikeee/cappu/internal/packages"
	"github.com/nikeee/cappu/internal/services"
)

// Server holds all language-server state.
type Server struct {
	conn    *lsp.Conn
	program *compiler.Program
	checker *compiler.Checker
	config  *config.Config

	docs             map[compiler.URI]string // open document text (java + cappu.json)
	inlayHints       services.InlayHintsSettings
	packageSourceURL []string
	packageSources   []packages.PackageSource
	latestCache      map[string]latestEntry
}

type latestEntry struct {
	value string
	ok    bool
	at    time.Time
}

const latestTTL = 5 * time.Minute

// NewServer builds a server. cfg may be nil (no project config).
func NewServer(cfg *config.Config) *Server {
	program := compiler.NewProgram()
	compiler.LoadJdkStub(program)
	s := &Server{
		program:          program,
		checker:          compiler.NewChecker(program),
		config:           cfg,
		docs:             map[compiler.URI]string{},
		inlayHints:       services.DefaultInlayHints,
		packageSourceURL: config.DefaultPackageSources,
		latestCache:      map[string]latestEntry{},
	}
	if cfg != nil {
		s.packageSourceURL = cfg.PackageSources
		if h := cfg.LspOptions.InlayHints; h != nil {
			s.inlayHints = services.InlayHintsSettings{ParameterNames: h.ParameterNames, VarTypes: h.VarTypes}
		}
		loadConfiguredSources(program, cfg)
	}
	return s
}

// warnMissingConfiguredPaths logs a warning per configured classPath/sourcePath
// that does not exist on disk (treated as empty), once the connection is up.
func (s *Server) warnMissingConfiguredPaths() {
	if s.config == nil {
		return
	}
	for _, path := range missingConfiguredPaths(s.config) {
		_ = s.conn.Notify("window/logMessage", map[string]any{
			"type":    2, // Warning
			"message": "configured path not found (treated as empty): " + path,
		})
	}
}

// inlayHintsRaw preserves per-field presence (a missing key leaves the setting
// untouched), matching the client-configuration semantics of the TS server.
type inlayHintsRaw struct {
	ParameterNames *bool `json:"parameterNames"`
	VarTypes       *bool `json:"varTypes"`
}

// Run wires every provider and serves JSON-RPC over the reader/writer until EOF.
func (s *Server) Run(reader io.Reader, writer io.Writer) error {
	s.conn = lsp.NewConn(reader, writer)
	s.register()
	return s.conn.Run()
}

// Serve starts the server over stdio.
func Serve(cfg *config.Config) error {
	return NewServer(cfg).Run(os.Stdin, os.Stdout)
}

func applyInlayHintSettings(settings *services.InlayHintsSettings, raw *inlayHintsRaw) {
	if raw == nil {
		return
	}
	if raw.ParameterNames != nil {
		settings.ParameterNames = *raw.ParameterNames
	}
	if raw.VarTypes != nil {
		settings.VarTypes = *raw.VarTypes
	}
}

func decode[T any](params json.RawMessage) T {
	var v T
	_ = json.Unmarshal(params, &v)
	return v
}

func (s *Server) register() {
	c := s.conn
	c.OnRequest("initialize", s.onInitialize)
	c.OnNotification("initialized", func(json.RawMessage) {
		_ = c.Request("client/registerCapability", map[string]any{
			"registrations": []map[string]any{{
				"id":              "watch-java",
				"method":          "workspace/didChangeWatchedFiles",
				"registerOptions": map[string]any{"watchers": []map[string]any{{"globPattern": "**/*.java"}}},
			}},
		})
		s.warnMissingConfiguredPaths()
	})
	c.OnNotification("textDocument/didOpen", s.onDidOpen)
	c.OnNotification("textDocument/didChange", s.onDidChange)
	c.OnNotification("textDocument/didClose", s.onDidClose)
	c.OnNotification("workspace/didChangeWatchedFiles", s.onDidChangeWatchedFiles)
	c.OnNotification("workspace/didChangeConfiguration", s.onDidChangeConfiguration)

	c.OnRequest("textDocument/documentSymbol", s.onDocumentSymbol)
	c.OnRequest("textDocument/definition", s.onDefinition)
	c.OnRequest("textDocument/typeDefinition", s.onTypeDefinition)
	c.OnRequest("textDocument/references", s.onReferences)
	c.OnRequest("textDocument/hover", s.onHover)
	c.OnRequest("textDocument/completion", s.onCompletion)
	c.OnRequest("textDocument/signatureHelp", s.onSignatureHelp)
	c.OnRequest("textDocument/prepareRename", s.onPrepareRename)
	c.OnRequest("textDocument/rename", s.onRename)
	c.OnRequest("textDocument/codeAction", s.onCodeAction)
	c.OnRequest("workspace/symbol", s.onWorkspaceSymbol)
	c.OnRequest("textDocument/inlayHint", s.onInlayHint)
	c.OnRequest("textDocument/documentHighlight", s.onDocumentHighlight)
	c.OnRequest("textDocument/foldingRange", s.onFoldingRange)
	c.OnRequest("textDocument/semanticTokens/full", s.onSemanticTokens)
	c.OnRequest("textDocument/codeLens", s.onCodeLens)
	c.OnRequest("textDocument/implementation", s.onImplementation)
	c.OnRequest("shutdown", func(json.RawMessage) (any, *lsp.ResponseError) { return nil, nil })
}

func (s *Server) onInitialize(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode[lsp.InitializeParams](params)
	var initOpts struct {
		InlayHints *inlayHintsRaw `json:"inlayHints"`
	}
	if len(p.InitializationOptions) > 0 {
		_ = json.Unmarshal(p.InitializationOptions, &initOpts)
	}
	applyInlayHintSettings(&s.inlayHints, initOpts.InlayHints)

	var roots []string
	for _, f := range p.WorkspaceFolders {
		roots = append(roots, f.URI)
	}
	if len(roots) == 0 && p.RootURI != "" {
		roots = []string{p.RootURI}
	}
	for _, root := range roots {
		for _, f := range loadJavaFiles(uriToPath(compiler.URI(root))) {
			s.program.AddProjectFile(compiler.URI(f[0]), f[1])
		}
	}
	return lsp.InitializeResult{Capabilities: lsp.ServerCapabilities{
		TextDocumentSync:          lsp.TextDocumentSyncIncremental,
		DocumentSymbolProvider:    true,
		DefinitionProvider:        true,
		ReferencesProvider:        true,
		HoverProvider:             true,
		CompletionProvider:        &lsp.CompletionOptions{TriggerCharacters: []string{"."}},
		SignatureHelpProvider:     &lsp.SignatureHelpOptions{TriggerCharacters: []string{"(", ","}},
		RenameProvider:            &lsp.RenameOptions{PrepareProvider: true},
		CodeActionProvider:        true,
		WorkspaceSymbolProvider:   true,
		InlayHintProvider:         true,
		DocumentHighlightProvider: true,
		FoldingRangeProvider:      true,
		TypeDefinitionProvider:    true,
		SemanticTokensProvider: &lsp.SemanticTokensOptions{
			Legend: lsp.SemanticTokensLegend{TokenTypes: services.TokenTypes, TokenModifiers: services.TokenModifiers},
			Full:   true,
		},
		CodeLensProvider:       &lsp.CodeLensOptions{ResolveProvider: false},
		ImplementationProvider: true,
	}}, nil
}

// --- document sync ------------------------------------------------------------

func isJavaURI(uri string) bool { return strings.HasSuffix(uri, ".java") }

func (s *Server) onDidOpen(params json.RawMessage) {
	p := decode[lsp.DidOpenTextDocumentParams](params)
	uri := compiler.URI(p.TextDocument.URI)
	s.docs[uri] = p.TextDocument.Text
	if isJavaURI(p.TextDocument.URI) {
		s.program.SetOpenDocument(uri, p.TextDocument.Text, p.TextDocument.Version)
		s.validate(uri)
	}
}

func (s *Server) onDidChange(params json.RawMessage) {
	p := decode[lsp.DidChangeTextDocumentParams](params)
	uri := compiler.URI(p.TextDocument.URI)
	text := s.docs[uri]
	for _, change := range p.ContentChanges {
		text = applyContentChange(text, change)
	}
	s.docs[uri] = text
	if isJavaURI(p.TextDocument.URI) {
		s.program.SetOpenDocument(uri, text, p.TextDocument.Version)
		s.validate(uri)
	}
}

func (s *Server) onDidClose(params json.RawMessage) {
	p := decode[lsp.DidCloseTextDocumentParams](params)
	uri := compiler.URI(p.TextDocument.URI)
	delete(s.docs, uri)
	if isJavaURI(p.TextDocument.URI) {
		s.program.CloseDocument(uri)
		_ = s.conn.Notify("textDocument/publishDiagnostics", lsp.PublishDiagnosticsParams{URI: p.TextDocument.URI, Diagnostics: []lsp.Diagnostic{}})
	}
}

func applyContentChange(text string, change lsp.TextDocumentContentChangeEvent) string {
	if change.Range == nil {
		return change.Text
	}
	lineStarts := compiler.ComputeLineStarts(text)
	start := compiler.GetPositionOfLineAndCharacter(text, lineStarts, change.Range.Start.Line, change.Range.Start.Character)
	end := compiler.GetPositionOfLineAndCharacter(text, lineStarts, change.Range.End.Line, change.Range.End.Character)
	if start < 0 || end > len(text) || start > end {
		return change.Text // out of range: treat as full replace, defensive
	}
	return text[:start] + change.Text + text[end:]
}

func (s *Server) onDidChangeWatchedFiles(params json.RawMessage) {
	p := decode[lsp.DidChangeWatchedFilesParams](params)
	for _, event := range p.Changes {
		uri := compiler.URI(event.URI)
		if event.Type == lsp.FileChangeDeleted {
			s.program.RemoveProjectFile(uri)
			continue
		}
		text, err := os.ReadFile(uriToPath(uri))
		if err != nil {
			s.program.RemoveProjectFile(uri)
			continue
		}
		s.program.AddProjectFile(uri, string(text))
	}
	for _, uri := range s.program.GetOpenUris() {
		s.validate(uri)
	}
}

func (s *Server) onDidChangeConfiguration(params json.RawMessage) {
	var p struct {
		Settings struct {
			Javalsp struct {
				InlayHints *inlayHintsRaw `json:"inlayHints"`
			} `json:"javalsp"`
		} `json:"settings"`
	}
	_ = json.Unmarshal(params, &p)
	applyInlayHintSettings(&s.inlayHints, p.Settings.Javalsp.InlayHints)
}

// --- diagnostics --------------------------------------------------------------

func toSeverity(category compiler.DiagnosticCategory) int {
	switch category {
	case compiler.CategoryError:
		return lsp.SeverityError
	case compiler.CategoryWarning:
		return lsp.SeverityWarning
	default:
		return lsp.SeverityInformation
	}
}

func toLspDiagnostic(d compiler.Diagnostic, text string, lineStarts []int) lsp.Diagnostic {
	out := lsp.Diagnostic{
		Severity: toSeverity(d.Category),
		Range:    lspRange(text, lineStarts, d.Pos, d.End),
		Message:  d.MessageText,
		Source:   "javalsp",
		Code:     d.Code,
	}
	if d.Code == 1305 { // unused import: editors fade it out
		out.Tags = []int{lsp.DiagnosticTagUnnecessary}
	}
	return out
}

func (s *Server) validate(uri compiler.URI) {
	sourceFile := s.program.GetSourceFile(uri)
	if sourceFile == nil {
		return
	}
	data := sourceFile.AsSourceFile()
	lineStarts := compiler.ComputeLineStarts(data.Text)
	var diags []lsp.Diagnostic
	add := func(list []compiler.Diagnostic) {
		for _, d := range list {
			diags = append(diags, toLspDiagnostic(d, data.Text, lineStarts))
		}
	}
	add(data.ParseDiagnostics)
	add(data.BindDiagnostics)
	add(s.checker.GetSemanticDiagnostics(sourceFile))
	if diags == nil {
		diags = []lsp.Diagnostic{}
	}
	_ = s.conn.Notify("textDocument/publishDiagnostics", lsp.PublishDiagnosticsParams{URI: string(uri), Diagnostics: diags})
}

// --- position / range helpers -------------------------------------------------

func lspPos(text string, lineStarts []int, offset int) lsp.Position {
	lc := compiler.GetLineAndCharacterOfPosition(text, lineStarts, offset)
	return lsp.Position{Line: lc.Line, Character: lc.Character}
}

func lspRange(text string, lineStarts []int, pos, end int) lsp.Range {
	return lsp.Range{Start: lspPos(text, lineStarts, pos), End: lspPos(text, lineStarts, end)}
}

func (s *Server) rangeOf(node *compiler.Node) lsp.Range {
	file := compiler.GetSourceFileOfNode(node).AsSourceFile()
	lineStarts := compiler.ComputeLineStarts(file.Text)
	start := compiler.SkipTrivia(file.Text, node.Pos)
	return lspRange(file.Text, lineStarts, start, node.End)
}

func (s *Server) locationOf(node *compiler.Node) lsp.Location {
	file := compiler.GetSourceFileOfNode(node).AsSourceFile()
	return lsp.Location{URI: file.FileName, Range: s.rangeOf(node)}
}

func (s *Server) sourceAndOffset(uri compiler.URI, pos lsp.Position) (*compiler.Node, int, bool) {
	sourceFile := s.program.GetSourceFile(uri)
	if sourceFile == nil {
		return nil, 0, false
	}
	text := sourceFile.AsSourceFile().Text
	lineStarts := compiler.ComputeLineStarts(text)
	offset := compiler.GetPositionOfLineAndCharacter(text, lineStarts, pos.Line, pos.Character)
	return sourceFile, offset, true
}

func (s *Server) identifierAt(uri compiler.URI, pos lsp.Position) *compiler.Node {
	sourceFile, offset, ok := s.sourceAndOffset(uri, pos)
	if !ok {
		return nil
	}
	return compiler.GetIdentifierAtPosition(sourceFile, offset)
}

func isStubSymbol(symbol *compiler.Symbol) bool {
	declaration := compiler.GetDeclarationNameNode(symbol)
	return declaration != nil && strings.HasPrefix(compiler.GetSourceFileOfNode(declaration).AsSourceFile().FileName, "jdk:///")
}

// typeDefinitionOf returns the location of an expression's (inferred) type
// declaration: its class, or an array's element class.
func (s *Server) typeDefinitionOf(expr *compiler.Node) any {
	t := s.checker.GetTypeOfExpression(expr)
	var classSymbol *compiler.Symbol
	switch {
	case t.Kind == compiler.TypeKindClass:
		classSymbol = t.Symbol
	case t.Kind == compiler.TypeKindArray && t.ElementType.Kind == compiler.TypeKindClass:
		classSymbol = t.ElementType.Symbol
	}
	if classSymbol == nil {
		return nil
	}
	if name := compiler.GetDeclarationNameNode(classSymbol); name != nil {
		return s.locationOf(name)
	}
	return nil
}

// varInferredFrom returns the variable name a `var` type infers from.
func varInferredFrom(varType *compiler.Node) *compiler.Node {
	parent := varType.Parent
	switch parent.Kind {
	case compiler.LocalVariableDeclarationStatement:
		decls := parent.AsLocalVariableDeclarationStatement().Declarators
		if decls != nil && decls.Len() > 0 {
			return decls.Nodes[0].AsVariableDeclarator().Name
		}
	case compiler.Parameter:
		return parent.AsParameter().Name
	}
	return nil
}
