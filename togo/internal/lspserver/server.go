package lspserver

// Java language server. The LSP protocol/transport is handled by internal/lsp;
// everything semantic comes from the Program (parse+bind, cached per document
// version), the checker, and the language-services layer. Port of
// src/services/server.ts.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

	features         services.LanguageFeatures // language-level features of the target release, set once
	docs             map[compiler.URI]string   // open document text (java + cappu.json)
	roots            []string                  // workspace root URIs (re-scanned on rebuild)
	inlayHints       services.InlayHintsSettings
	packageSourceURL []string
	packageSources   []packages.PackageSource
	latestCache      map[string]latestEntry

	initialized      bool // initialize request seen
	shutdownReceived bool // shutdown request seen (exit code contract)
}

// ErrExitWithoutShutdown is returned by Run when the client sends `exit`
// without a prior `shutdown`; the process must then exit 1 (LSP spec, matching
// vscode-languageserver in the TS build).
var ErrExitWithoutShutdown = errors.New("exit without shutdown")

type latestEntry struct {
	value string
	ok    bool
	at    time.Time
}

const latestTTL = 5 * time.Minute

// nullnessConfig returns the jspecify nullness options from cfg, or nil when
// there is no config (nikeee/cappu#25).
func nullnessConfig(cfg *config.Config) *config.Nullness {
	if cfg == nil {
		return nil
	}
	return cfg.CompilerOptions.Nullness
}

// releaseOf returns the configured javac --release, or nil (toolchain default).
func releaseOf(cfg *config.Config) *int {
	if cfg == nil {
		return nil
	}
	return cfg.CompilerOptions.Release
}

// NewServer builds a server. cfg may be nil (no project config).
func NewServer(cfg *config.Config) *Server {
	program := compiler.NewProgram()
	compiler.InstallJdkTypes(program, cfg)
	s := &Server{
		program:          program,
		checker:          compiler.NewChecker(program, nullnessConfig(cfg)),
		config:           cfg,
		features:         services.NewLanguageFeatures(releaseOf(cfg)),
		docs:             map[compiler.URI]string{},
		inlayHints:       services.DefaultInlayHints,
		packageSourceURL: config.DefaultPackageSources,
		latestCache:      map[string]latestEntry{},
	}
	if cfg != nil {
		s.packageSourceURL = cfg.PackageSources
		if h := cfg.LspOptions.InlayHints; h != nil {
			s.inlayHints = services.InlayHintsSettings{ParameterNames: *h.ParameterNames, VarTypes: *h.VarTypes}
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
	// Requests before `initialize` are rejected with ServerNotInitialized
	// (-32002), matching vscode-languageserver in the TS build.
	req := func(method string, h lsp.RequestHandler) {
		c.OnRequest(method, func(params json.RawMessage) (any, *lsp.ResponseError) {
			if !s.initialized && method != "initialize" {
				return nil, &lsp.ResponseError{Code: -32002, Message: "server not initialized"}
			}
			return h(params)
		})
	}
	req("initialize", s.onInitialize)
	c.OnNotification("initialized", func(json.RawMessage) {
		_ = c.Request("client/registerCapability", map[string]any{
			"registrations": []map[string]any{{
				"id":              "watch-java",
				"method":          "workspace/didChangeWatchedFiles",
				"registerOptions": map[string]any{"watchers": s.fileWatchers()},
			}},
		})
		s.warnMissingConfiguredPaths()
	})
	c.OnNotification("textDocument/didOpen", s.onDidOpen)
	c.OnNotification("textDocument/didChange", s.onDidChange)
	c.OnNotification("textDocument/didClose", s.onDidClose)
	c.OnNotification("workspace/didChangeWatchedFiles", s.onDidChangeWatchedFiles)
	c.OnNotification("workspace/didChangeConfiguration", s.onDidChangeConfiguration)

	req("textDocument/documentSymbol", s.onDocumentSymbol)
	req("textDocument/definition", s.onDefinition)
	req("textDocument/typeDefinition", s.onTypeDefinition)
	req("textDocument/references", s.onReferences)
	req("textDocument/hover", s.onHover)
	req("textDocument/completion", s.onCompletion)
	req("textDocument/signatureHelp", s.onSignatureHelp)
	req("textDocument/prepareRename", s.onPrepareRename)
	req("textDocument/rename", s.onRename)
	req("textDocument/codeAction", s.onCodeAction)
	req("workspace/symbol", s.onWorkspaceSymbol)
	req("textDocument/inlayHint", s.onInlayHint)
	req("textDocument/documentHighlight", s.onDocumentHighlight)
	req("textDocument/foldingRange", s.onFoldingRange)
	req("textDocument/semanticTokens/full", s.onSemanticTokens)
	req("textDocument/codeLens", s.onCodeLens)
	req("textDocument/implementation", s.onImplementation)
	req("textDocument/prepareTypeHierarchy", s.onPrepareTypeHierarchy)
	req("typeHierarchy/supertypes", s.onTypeHierarchySupertypes)
	req("typeHierarchy/subtypes", s.onTypeHierarchySubtypes)
	req("textDocument/prepareCallHierarchy", s.onPrepareCallHierarchy)
	req("callHierarchy/incomingCalls", s.onCallHierarchyIncoming)
	req("callHierarchy/outgoingCalls", s.onCallHierarchyOutgoing)
	req("shutdown", func(json.RawMessage) (any, *lsp.ResponseError) {
		s.shutdownReceived = true
		return nil, nil
	})
	// `exit` terminates the read loop; exit code 0 only after `shutdown`
	// (LSP spec), like vscode-languageserver's exit handler in the TS build.
	c.OnNotification("exit", func(json.RawMessage) {
		if s.shutdownReceived {
			c.Stop(nil)
		} else {
			c.Stop(ErrExitWithoutShutdown)
		}
	})
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
	s.initialized = true

	var roots []string
	for _, f := range p.WorkspaceFolders {
		roots = append(roots, f.URI)
	}
	if len(roots) == 0 && p.RootURI != "" {
		roots = []string{p.RootURI}
	}
	s.roots = roots
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
		TypeHierarchyProvider:  true,
		CallHierarchyProvider:  true,
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

// fileWatchers is the watch set requested from the client: source .java
// files, the loaded cappu.json (by name), and every configured classPath
// entry (a dir as a jar/class glob, a .jar as itself). Computed once from the
// startup config. Port of configWatchGlobs in src/workspace.ts.
// ponytail: a config edit that changes classPath keeps the old watch set until
// restart; re-register after reloadConfig if that ever matters. Absolute globs
// only fire inside the workspace folders, so classpath entries outside the
// root are not watched (the MCP server's per-call polling has no such limit).
func (s *Server) fileWatchers() []map[string]any {
	watchers := []map[string]any{{"globPattern": "**/*.java"}}
	if s.config == nil || s.config.ConfigPath == "" {
		return watchers
	}
	watchers = append(watchers, map[string]any{"globPattern": "**/" + filepath.Base(s.config.ConfigPath)})
	for _, p := range s.config.CompilerOptions.ClassPath {
		entry := filepath.ToSlash(s.config.ResolvePath(p))
		if strings.HasSuffix(entry, ".jar") {
			watchers = append(watchers, map[string]any{"globPattern": entry})
		} else {
			watchers = append(watchers, map[string]any{"globPattern": entry + "/**/*.{jar,class}"})
		}
	}
	return watchers
}

// rebuild replaces the program and checker from the current config. A
// classpath or config change cannot be patched incrementally (LoadClassPath
// never removes stubs for classes that disappeared), so the workspace is
// rebuilt from scratch exactly like at startup, then the open documents are
// re-injected (they overlay disk state and must survive) and re-validated.
func (s *Server) rebuild() {
	program := compiler.NewProgram()
	compiler.InstallJdkTypes(program, s.config)
	if s.config != nil {
		loadConfiguredSources(program, s.config)
	}
	for _, root := range s.roots {
		for _, f := range loadJavaFiles(uriToPath(compiler.URI(root))) {
			program.AddProjectFile(compiler.URI(f[0]), f[1])
		}
	}
	for uri, text := range s.docs {
		if isJavaURI(string(uri)) {
			// version 0 is safe: the fresh program has no cached parses.
			program.SetOpenDocument(uri, text, 0)
		}
	}
	s.program = program
	s.checker = compiler.NewChecker(program, nullnessConfig(s.config))
	for _, uri := range program.GetOpenUris() {
		s.validate(uri)
	}
}

// reloadConfig re-reads cappu.json and reports whether it applied. A
// malformed edit keeps the last good config (logged as a warning); the file
// is re-read on the next watched change. A config file appearing (server
// started without one) or disappearing is deliberately not handled.
func (s *Server) reloadConfig() bool {
	if s.config == nil || s.config.ConfigPath == "" {
		return false
	}
	next, err := config.Load(s.config.ConfigPath, s.config.BaseDir)
	if err != nil {
		_ = s.conn.Notify("window/logMessage", map[string]any{
			"type":    2, // Warning
			"message": fmt.Sprintf("%s (keeping previous config)", err),
		})
		return false
	}
	s.config = next
	s.packageSourceURL = next.PackageSources
	s.packageSources = nil
	s.latestCache = map[string]latestEntry{}
	s.inlayHints = services.DefaultInlayHints
	if h := next.LspOptions.InlayHints; h != nil {
		s.inlayHints = services.InlayHintsSettings{ParameterNames: *h.ParameterNames, VarTypes: *h.VarTypes}
	}
	return true
}

func (s *Server) onDidChangeWatchedFiles(params json.RawMessage) {
	p := decode[lsp.DidChangeWatchedFilesParams](params)
	needsRebuild := false
	for _, event := range p.Changes {
		uri := compiler.URI(event.URI)
		switch {
		case isJavaURI(event.URI):
			if event.Type == lsp.FileChangeDeleted {
				s.program.RemoveProjectFile(uri)
				continue
			}
			text, err := os.ReadFile(string(uriToPath(uri)))
			if err != nil {
				s.program.RemoveProjectFile(uri)
				continue
			}
			s.program.AddProjectFile(uri, string(text))
		case s.config != nil && string(uriToPath(uri)) == s.config.ConfigPath:
			// deletes are ignored: keep the last good config
			if event.Type != lsp.FileChangeDeleted && s.reloadConfig() {
				needsRebuild = true
			}
		case strings.HasSuffix(event.URI, ".jar") || strings.HasSuffix(event.URI, ".class"):
			needsRebuild = true
		}
		// anything else (e.g. a nested cappu.json in a subdirectory) is ignored
	}
	if needsRebuild {
		s.rebuild() // validates every open doc
		return
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
	case compiler.CategorySuggestion:
		return lsp.SeverityHint
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
		Code:     int(d.Code),
	}
	if d.Code == 1305 { // unused import: editors fade it out
		out.Tags = []int{lsp.DiagnosticTagUnnecessary}
	}
	if d.Code == 1306 { // use of @Deprecated: editors strike it out
		out.Tags = []int{lsp.DiagnosticTagDeprecated}
	}
	return out
}

func (s *Server) validate(uri compiler.URI) {
	sourceFile := s.program.GetSourceFile(uri)
	if sourceFile == nil {
		return
	}
	data := sourceFile.AsSourceFile()
	lineStarts := data.LineStarts()
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
	lineStarts := file.LineStarts()
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
	lineStarts := sourceFile.AsSourceFile().LineStarts()
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
