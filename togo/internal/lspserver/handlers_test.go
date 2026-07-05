package lspserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/compiler"
	"github.com/nikeee/cappu/internal/lsp"
)

// End-to-end transport coverage for the remaining LSP handlers (the TS build has
// no server test; the service logic is covered by the service-level tests, so
// these verify only that each request is wired and round-trips correctly).

const handlerSrc = "interface Shape { double area(); }\n" +
	"class Circle implements Shape {\n" +
	"  int radius;\n" +
	"  public double area() { return radius; }\n" +
	"  void m() { var c = new Circle(); area(); }\n" +
	"}\n"

func openedServer(t *testing.T) *testClient {
	t.Helper()
	c := startTestServer(t)
	c.request(t, "initialize", lsp.InitializeParams{})
	openDoc(t, c, "file:///S.java", handlerSrc)
	return c
}

func tdParams(needle string) lsp.TextDocumentPositionParams {
	return lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file:///S.java"},
		Position:     posOf(handlerSrc, needle),
	}
}

// posOfN returns the position of the nth (1-based) occurrence of needle.
func posOfN(src, needle string, n int) lsp.Position {
	idx := -1
	for i := 0; i < n; i++ {
		next := strings.Index(src[idx+1:], needle)
		idx = idx + 1 + next
	}
	line, char := 0, 0
	for i := 0; i < idx; i++ {
		if src[i] == '\n' {
			line, char = line+1, 0
		} else {
			char++
		}
	}
	return lsp.Position{Line: line, Character: char}
}

func TestServerDefinition(t *testing.T) {
	c := openedServer(t)
	// The 2nd `area(` is the bare call site inside Circle.m(); it resolves to
	// Circle's own override (line 3), not the interface declaration (line 0).
	result := c.request(t, "textDocument/definition", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file:///S.java"},
		Position:     posOfN(handlerSrc, "area(", 3), // decl(0), override decl(1), call(2) -> 1-based 3
	})
	var loc lsp.Location
	if err := json.Unmarshal(result, &loc); err != nil {
		t.Fatal(err)
	}
	if loc.URI != "file:///S.java" || loc.Range.Start.Line != 3 {
		t.Errorf("definition = %+v, want the area() method on line 3", loc)
	}
}

func TestServerTypeDefinition(t *testing.T) {
	c := openedServer(t)
	// `c` is a Circle; go-to-type-definition lands on the Circle declaration.
	result := c.request(t, "textDocument/typeDefinition", tdParams("c = new"))
	var loc lsp.Location
	if err := json.Unmarshal(result, &loc); err != nil {
		t.Fatal(err)
	}
	if loc.Range.Start.Line != 1 {
		t.Errorf("typeDefinition line = %d, want 1 (class Circle)", loc.Range.Start.Line)
	}
}

func TestServerImplementation(t *testing.T) {
	c := openedServer(t)
	result := c.request(t, "textDocument/implementation", tdParams("Shape { double"))
	var locs []lsp.Location
	if err := json.Unmarshal(result, &locs); err != nil {
		t.Fatal(err)
	}
	if len(locs) != 1 {
		t.Errorf("implementations = %d, want 1 (Circle)", len(locs))
	}
}

func TestServerDocumentHighlight(t *testing.T) {
	c := openedServer(t)
	result := c.request(t, "textDocument/documentHighlight", tdParams("radius;"))
	var hs []lsp.DocumentHighlight
	if err := json.Unmarshal(result, &hs); err != nil {
		t.Fatal(err)
	}
	// declaration + the use in area()
	if len(hs) != 2 {
		t.Errorf("highlights = %d, want 2", len(hs))
	}
}

func TestServerFoldingRange(t *testing.T) {
	c := openedServer(t)
	result := c.request(t, "textDocument/foldingRange", lsp.DocumentSymbolParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file:///S.java"},
	})
	var ranges []lsp.FoldingRange
	if err := json.Unmarshal(result, &ranges); err != nil {
		t.Fatal(err)
	}
	if len(ranges) == 0 {
		t.Error("expected at least one folding range (the Circle body)")
	}
}

func TestServerSignatureHelp(t *testing.T) {
	c := startTestServer(t)
	c.request(t, "initialize", lsp.InitializeParams{})
	src := "class C { int add(int a, int b) { return 0; } void m() { add(1, ); } }"
	openDoc(t, c, "file:///C.java", src)
	result := c.request(t, "textDocument/signatureHelp", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file:///C.java"},
		Position:     posOf(src, "1, )"), // inside the argument list
	})
	var help lsp.SignatureHelp
	if err := json.Unmarshal(result, &help); err != nil {
		t.Fatal(err)
	}
	if len(help.Signatures) != 1 || help.Signatures[0].Label != "int add(int a, int b)" {
		t.Errorf("signature help = %+v", help)
	}
}

func TestServerCodeAction(t *testing.T) {
	c := startTestServer(t)
	c.request(t, "initialize", lsp.InitializeParams{})
	src := "import java.util.List;\nclass C { int x; }\n"
	openDoc(t, c, "file:///C.java", src)
	result := c.request(t, "textDocument/codeAction", lsp.CodeActionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file:///C.java"},
		Range:        lsp.Range{Start: lsp.Position{Line: 0, Character: 0}, End: lsp.Position{Line: 0, Character: 20}},
	})
	var actions []lsp.CodeAction
	if err := json.Unmarshal(result, &actions); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range actions {
		if strings.Contains(a.Title, "Remove unused import") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a 'Remove unused import' action, got %+v", actions)
	}
}

func TestServerSemanticTokens(t *testing.T) {
	c := openedServer(t)
	result := c.request(t, "textDocument/semanticTokens/full", lsp.DocumentSymbolParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file:///S.java"},
	})
	var tokens lsp.SemanticTokens
	if err := json.Unmarshal(result, &tokens); err != nil {
		t.Fatal(err)
	}
	// every entry is 5 uints; a non-empty, multiple-of-5 data array
	if len(tokens.Data) == 0 || len(tokens.Data)%5 != 0 {
		t.Errorf("semantic tokens data length = %d, want a non-zero multiple of 5", len(tokens.Data))
	}
}

func TestServerInlayHint(t *testing.T) {
	c := startTestServer(t)
	c.request(t, "initialize", lsp.InitializeParams{})
	src := "class C { static int twice(int value) { return value; } void m() { var n = twice(21); } }"
	openDoc(t, c, "file:///C.java", src)
	result := c.request(t, "textDocument/inlayHint", lsp.InlayHintParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file:///C.java"},
		Range:        lsp.Range{Start: lsp.Position{Line: 0, Character: 0}, End: lsp.Position{Line: 0, Character: 200}},
	})
	var hints []lsp.InlayHint
	if err := json.Unmarshal(result, &hints); err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, h := range hints {
		labels = append(labels, h.Label)
	}
	if len(labels) < 2 { // ": int" var-type and "value:" parameter
		t.Errorf("inlay hints = %v, want a var-type and a parameter hint", labels)
	}
}

func TestServerCodeLens(t *testing.T) {
	c := openedServer(t)
	result := c.request(t, "textDocument/codeLens", lsp.DocumentSymbolParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file:///S.java"},
	})
	var lenses []lsp.CodeLens
	if err := json.Unmarshal(result, &lenses); err != nil {
		t.Fatal(err)
	}
	if len(lenses) == 0 {
		t.Error("expected reference/implementation code lenses")
	}
}

func TestServerWorkspaceSymbol(t *testing.T) {
	c := openedServer(t)
	result := c.request(t, "workspace/symbol", lsp.WorkspaceSymbolParams{Query: "circle"})
	var syms []lsp.SymbolInformation
	if err := json.Unmarshal(result, &syms); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range syms {
		if s.Name == "Circle" {
			found = true
		}
	}
	if !found {
		t.Errorf("workspace/symbol 'circle' should find Circle, got %+v", syms)
	}
}

func TestServerPrepareRename(t *testing.T) {
	c := openedServer(t)
	result := c.request(t, "textDocument/prepareRename", tdParams("radius;"))
	var rng lsp.Range
	if err := json.Unmarshal(result, &rng); err != nil {
		t.Fatal(err)
	}
	if rng.Start.Line != 2 {
		t.Errorf("prepareRename range line = %d, want 2 (the radius field)", rng.Start.Line)
	}
}

func TestServerIncrementalDidChange(t *testing.T) {
	c := startTestServer(t)
	c.request(t, "initialize", lsp.InitializeParams{})
	openDoc(t, c, "file:///C.java", "class C { int foo; }\n")
	// Replace "foo" (chars 14..17 on line 0) with "renamed".
	c.notify(t, "textDocument/didChange", lsp.DidChangeTextDocumentParams{
		TextDocument: lsp.VersionedTextDocumentIdentifier{URI: "file:///C.java", Version: 2},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{{
			Range: &lsp.Range{Start: lsp.Position{Line: 0, Character: 14}, End: lsp.Position{Line: 0, Character: 17}},
			Text:  "renamed",
		}},
	})
	// The field is now "renamed": a documentSymbol confirms the incremental edit applied.
	result := c.request(t, "textDocument/documentSymbol", lsp.DocumentSymbolParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file:///C.java"},
	})
	var syms []lsp.DocumentSymbol
	if err := json.Unmarshal(result, &syms); err != nil {
		t.Fatal(err)
	}
	if len(syms) != 1 || len(syms[0].Children) != 1 || syms[0].Children[0].Name != "renamed" {
		t.Errorf("after incremental change, field = %+v, want 'renamed'", syms)
	}
}

func TestServerMakeFieldFinalCodeAction(t *testing.T) {
	c := startTestServer(t)
	c.request(t, "initialize", lsp.InitializeParams{})
	src := "class C {\n  private int x = 1;\n}\n"
	openDoc(t, c, "file:///C.java", src)
	result := c.request(t, "textDocument/codeAction", lsp.CodeActionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file:///C.java"},
		Range:        lsp.Range{Start: lsp.Position{Line: 1, Character: 14}, End: lsp.Position{Line: 1, Character: 14}},
	})
	var actions []lsp.CodeAction
	if err := json.Unmarshal(result, &actions); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range actions {
		if a.Title == "Add 'final' modifier" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an \"Add 'final' modifier\" action, got %+v", actions)
	}
}

func TestToSeveritySuggestionIsHint(t *testing.T) {
	if got := toSeverity(compiler.CategorySuggestion); got != lsp.SeverityHint {
		t.Errorf("toSeverity(CategorySuggestion) = %d, want %d", got, lsp.SeverityHint)
	}
}
