package lsp

import "encoding/json"

// LSP protocol request/response/notification payloads the server exchanges,
// hand-rolled to match the vscode-languageserver shapes the TypeScript build
// uses. Port of the types from src/services/server.ts's use of
// vscode-languageserver.

// --- text documents ----------------------------------------------------------

type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

type VersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// --- lifecycle / sync ---------------------------------------------------------

type WorkspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

type InitializeParams struct {
	RootURI               string            `json:"rootUri"`
	WorkspaceFolders      []WorkspaceFolder `json:"workspaceFolders"`
	InitializationOptions json.RawMessage   `json:"initializationOptions"`
}

type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
}

type CompletionOptions struct {
	TriggerCharacters []string `json:"triggerCharacters,omitempty"`
}

type SignatureHelpOptions struct {
	TriggerCharacters []string `json:"triggerCharacters,omitempty"`
}

type RenameOptions struct {
	PrepareProvider bool `json:"prepareProvider"`
}

type SemanticTokensLegend struct {
	TokenTypes     []string `json:"tokenTypes"`
	TokenModifiers []string `json:"tokenModifiers"`
}

type SemanticTokensOptions struct {
	Legend SemanticTokensLegend `json:"legend"`
	Full   bool                 `json:"full"`
}

type CodeLensOptions struct {
	ResolveProvider bool `json:"resolveProvider"`
}

type ServerCapabilities struct {
	TextDocumentSync          int                    `json:"textDocumentSync"`
	DocumentSymbolProvider    bool                   `json:"documentSymbolProvider"`
	DefinitionProvider        bool                   `json:"definitionProvider"`
	ReferencesProvider        bool                   `json:"referencesProvider"`
	HoverProvider             bool                   `json:"hoverProvider"`
	CompletionProvider        *CompletionOptions     `json:"completionProvider,omitempty"`
	SignatureHelpProvider     *SignatureHelpOptions  `json:"signatureHelpProvider,omitempty"`
	RenameProvider            *RenameOptions         `json:"renameProvider,omitempty"`
	CodeActionProvider        bool                   `json:"codeActionProvider"`
	WorkspaceSymbolProvider   bool                   `json:"workspaceSymbolProvider"`
	InlayHintProvider         bool                   `json:"inlayHintProvider"`
	DocumentHighlightProvider bool                   `json:"documentHighlightProvider"`
	FoldingRangeProvider      bool                   `json:"foldingRangeProvider"`
	TypeDefinitionProvider    bool                   `json:"typeDefinitionProvider"`
	SemanticTokensProvider    *SemanticTokensOptions `json:"semanticTokensProvider,omitempty"`
	CodeLensProvider          *CodeLensOptions       `json:"codeLensProvider,omitempty"`
	ImplementationProvider    bool                   `json:"implementationProvider"`
	TypeHierarchyProvider     bool                   `json:"typeHierarchyProvider"`
	CallHierarchyProvider     bool                   `json:"callHierarchyProvider"`
}

const TextDocumentSyncIncremental = 2

type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

type TextDocumentContentChangeEvent struct {
	Range *Range `json:"range,omitempty"`
	Text  string `json:"text"`
}

type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

const (
	FileChangeCreated = 1
	FileChangeChanged = 2
	FileChangeDeleted = 3
)

type FileEvent struct {
	URI  string `json:"uri"`
	Type int    `json:"type"`
}

type DidChangeWatchedFilesParams struct {
	Changes []FileEvent `json:"changes"`
}

type DidChangeConfigurationParams struct {
	Settings json.RawMessage `json:"settings"`
}

// --- diagnostics --------------------------------------------------------------

const (
	SeverityError       = 1
	SeverityWarning     = 2
	SeverityInformation = 3
	SeverityHint        = 4
)

const (
	DiagnosticTagUnnecessary = 1
	DiagnosticTagDeprecated  = 2
)

const CompletionItemTagDeprecated = 1

type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"`
	Code     int    `json:"code"`
	Source   string `json:"source"`
	Message  string `json:"message"`
	Tags     []int  `json:"tags,omitempty"`
}

type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// --- features -----------------------------------------------------------------

type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

const MarkupKindMarkdown = "markdown"

type Hover struct {
	Contents MarkupContent `json:"contents"`
}

type CompletionItem struct {
	Label string `json:"label"`
	Kind  int    `json:"kind"`
	Tags  []int  `json:"tags,omitempty"`
}

type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

type WorkspaceEdit struct {
	Changes map[string][]TextEdit `json:"changes"`
}

type CodeActionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
}

type CodeAction struct {
	Title string        `json:"title"`
	Kind  string        `json:"kind"`
	Edit  WorkspaceEdit `json:"edit"`
}

type Command struct {
	Title     string `json:"title"`
	Command   string `json:"command"`
	Arguments []any  `json:"arguments,omitempty"`
}

type CodeLens struct {
	Range   Range    `json:"range"`
	Command *Command `json:"command,omitempty"`
}

type ParameterInformation struct {
	Label string `json:"label"`
}

type SignatureInformation struct {
	Label         string                 `json:"label"`
	Parameters    []ParameterInformation `json:"parameters"`
	Documentation string                 `json:"documentation,omitempty"`
}

type SignatureHelp struct {
	Signatures      []SignatureInformation `json:"signatures"`
	ActiveSignature int                    `json:"activeSignature"`
	ActiveParameter int                    `json:"activeParameter"`
}

const (
	DocumentHighlightRead  = 1
	DocumentHighlightWrite = 3
)

type DocumentHighlight struct {
	Range Range `json:"range"`
	Kind  int   `json:"kind"`
}

type FoldingRange struct {
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Kind      string `json:"kind,omitempty"`
}

const (
	InlayHintKindType      = 1
	InlayHintKindParameter = 2
)

type InlayHint struct {
	Position     Position `json:"position"`
	Label        string   `json:"label"`
	Kind         int      `json:"kind"`
	PaddingRight bool     `json:"paddingRight,omitempty"`
}

type InlayHintParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
}

type SemanticTokens struct {
	Data []uint `json:"data"`
}

type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type WorkspaceSymbolParams struct {
	Query string `json:"query"`
}

type SymbolInformation struct {
	Name          string   `json:"name"`
	Kind          int      `json:"kind"`
	Location      Location `json:"location"`
	ContainerName string   `json:"containerName,omitempty"`
	Tags          []int    `json:"tags,omitempty"`
}

type ReferenceParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

type RenameParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	NewName      string                 `json:"newName"`
}
