package lspserver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/lsp"
)

// An in-process JSON-RPC client driving the server over two pipes, to verify the
// transport and handler wiring end to end (the TS build has no server test; the
// service logic is covered by the fourslash/service tests).

type testClient struct {
	toServer *io.PipeWriter
	incoming chan rpcMessage
	nextID   int
}

func startTestServer(t *testing.T) *testClient {
	return startTestServerWith(t, nil)
}

func startTestServerWith(t *testing.T, cfg *config.Config) *testClient {
	t.Helper()
	cin, sin := io.Pipe()   // client -> server
	sout, cout := io.Pipe() // server -> client
	server := NewServer(cfg)
	go func() { _ = server.Run(cin, cout) }()
	c := &testClient{toServer: sin, incoming: make(chan rpcMessage, 64)}
	// A background reader drains every server->client message (responses and
	// notifications) so the synchronous pipe never blocks the server's writes.
	go func() {
		r := bufio.NewReader(sout)
		for {
			msg, err := readMessage(r)
			if err != nil {
				close(c.incoming)
				return
			}
			c.incoming <- msg
		}
	}()
	t.Cleanup(func() { _ = sin.Close(); _ = sout.Close() })
	return c
}

func (c *testClient) request(t *testing.T, method string, params any) json.RawMessage {
	t.Helper()
	c.nextID++
	id := c.nextID
	c.send(t, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	for msg := range c.incoming {
		if msg.idEquals(id) {
			if msg.Error != nil {
				t.Fatalf("%s error: %s", method, msg.Error.Message)
			}
			return msg.Result
		}
	}
	t.Fatalf("%s: connection closed before a response", method)
	return nil
}

func (c *testClient) notify(t *testing.T, method string, params any) {
	t.Helper()
	c.send(t, map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *testClient) send(t *testing.T, msg any) {
	t.Helper()
	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(c.toServer, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
		t.Fatal(err)
	}
}

type rpcMessage struct {
	ID     json.RawMessage    `json:"id"`
	Result json.RawMessage    `json:"result"`
	Error  *lsp.ResponseError `json:"error"`
	Method string             `json:"method"`
	Params json.RawMessage    `json:"params"`
}

// awaitNotification reads server->client traffic until a message with the
// given method arrives, returning its params.
func (c *testClient) awaitNotification(t *testing.T, method string) json.RawMessage {
	t.Helper()
	for msg := range c.incoming {
		if msg.Method == method {
			return msg.Params
		}
	}
	t.Fatalf("connection closed before a %s notification", method)
	return nil
}

func (m rpcMessage) idEquals(id int) bool {
	if len(m.ID) == 0 {
		return false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(m.ID)))
	return err == nil && n == id
}

func readMessage(r *bufio.Reader) (rpcMessage, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return rpcMessage{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if name, value, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			length, _ = strconv.Atoi(strings.TrimSpace(value))
		}
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return rpcMessage{}, err
	}
	var msg rpcMessage
	if err := json.Unmarshal(buf, &msg); err != nil {
		return rpcMessage{}, err
	}
	return msg, nil
}

// posOf returns the LSP position of the first occurrence of needle in src.
func posOf(src, needle string) lsp.Position {
	idx := strings.Index(src, needle)
	line, char := 0, 0
	for i := 0; i < idx; i++ {
		if src[i] == '\n' {
			line++
			char = 0
		} else {
			char++
		}
	}
	return lsp.Position{Line: line, Character: char}
}

const serverSrc = "class C {\n  int count;\n  int twice(int x) { return x * 2; }\n  void m() { count = twice(count); }\n}\n"

func openDoc(t *testing.T, c *testClient, uri, text string) {
	c.notify(t, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "languageId": "java", "version": 1, "text": text},
	})
}

func TestServerInitialize(t *testing.T) {
	c := startTestServer(t)
	result := c.request(t, "initialize", lsp.InitializeParams{})
	var res lsp.InitializeResult
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatal(err)
	}
	caps := res.Capabilities
	if !caps.HoverProvider || !caps.DefinitionProvider || !caps.ReferencesProvider {
		t.Error("expected hover/definition/references capabilities")
	}
	if caps.CompletionProvider == nil || len(caps.CompletionProvider.TriggerCharacters) == 0 {
		t.Error("expected completion trigger characters")
	}
	if caps.SemanticTokensProvider == nil || !caps.SemanticTokensProvider.Full {
		t.Error("expected semantic tokens provider")
	}
}

func TestServerHover(t *testing.T) {
	c := startTestServer(t)
	c.request(t, "initialize", lsp.InitializeParams{})
	openDoc(t, c, "file:///C.java", serverSrc)

	result := c.request(t, "textDocument/hover", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file:///C.java"},
		Position:     posOf(serverSrc, "twice(int"),
	})
	var hover lsp.Hover
	if err := json.Unmarshal(result, &hover); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(hover.Contents.Value, "int twice(int x)") {
		t.Errorf("hover = %q, want it to contain the signature", hover.Contents.Value)
	}
}

func TestServerCompletion(t *testing.T) {
	c := startTestServer(t)
	c.request(t, "initialize", lsp.InitializeParams{})
	openDoc(t, c, "file:///C.java", serverSrc)

	result := c.request(t, "textDocument/completion", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file:///C.java"},
		Position:     posOf(serverSrc, "count = twice"), // scope completion
	})
	var items []lsp.CompletionItem
	if err := json.Unmarshal(result, &items); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range items {
		if it.Label == "twice" {
			found = true
		}
	}
	if !found {
		t.Errorf("completion should offer 'twice', got %d items", len(items))
	}
}

func TestServerDocumentSymbol(t *testing.T) {
	c := startTestServer(t)
	c.request(t, "initialize", lsp.InitializeParams{})
	openDoc(t, c, "file:///C.java", serverSrc)

	result := c.request(t, "textDocument/documentSymbol", lsp.DocumentSymbolParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file:///C.java"},
	})
	var syms []lsp.DocumentSymbol
	if err := json.Unmarshal(result, &syms); err != nil {
		t.Fatal(err)
	}
	if len(syms) != 1 || syms[0].Name != "C" {
		t.Fatalf("symbols = %+v, want one class C", syms)
	}
	names := map[string]bool{}
	for _, child := range syms[0].Children {
		names[child.Name] = true
	}
	if !names["count"] || !names["twice"] || !names["m"] {
		t.Errorf("class children = %v, want count/twice/m", names)
	}
}

func TestServerReferencesAndRename(t *testing.T) {
	c := startTestServer(t)
	c.request(t, "initialize", lsp.InitializeParams{})
	openDoc(t, c, "file:///C.java", serverSrc)

	result := c.request(t, "textDocument/references", lsp.ReferenceParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file:///C.java"},
		Position:     posOf(serverSrc, "count;"),
	})
	var locs []lsp.Location
	if err := json.Unmarshal(result, &locs); err != nil {
		t.Fatal(err)
	}
	// declaration + `count = ...` (write) + `twice(count)` (read) = 3
	if len(locs) != 3 {
		t.Errorf("references = %d, want 3", len(locs))
	}

	renameResult := c.request(t, "textDocument/rename", lsp.RenameParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file:///C.java"},
		Position:     posOf(serverSrc, "count;"),
		NewName:      "total",
	})
	var edit lsp.WorkspaceEdit
	if err := json.Unmarshal(renameResult, &edit); err != nil {
		t.Fatal(err)
	}
	if len(edit.Changes["file:///C.java"]) != 3 {
		t.Errorf("rename edits = %d, want 3", len(edit.Changes["file:///C.java"]))
	}
}

// --- config/classpath live reload ---------------------------------------------

// writeLspConfig writes a cappu.json with the given classPath entries and
// loads it, so the server has a real ConfigPath to watch.
func writeLspConfig(t *testing.T, dir string, classPath []string) *config.Config {
	t.Helper()
	entries, err := json.Marshal(classPath)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "cappu.json")
	body := `{"compilerOptions":{"classPath":` + string(entries) + `,"sourcePaths":[]}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func copyJarFixture(t *testing.T, dst string) {
	t.Helper()
	src, err := filepath.Abs(filepath.Join("..", "compiler", "testdata", "classfiles", "util.jar"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestServerWatcherRegistration(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := writeLspConfig(t, dir, []string{"lib", "direct.jar"})
	c := startTestServerWith(t, cfg)
	c.request(t, "initialize", lsp.InitializeParams{})
	c.notify(t, "initialized", map[string]any{})

	params := c.awaitNotification(t, "client/registerCapability")
	var globs []string
	var reg struct {
		Registrations []struct {
			RegisterOptions struct {
				Watchers []struct {
					GlobPattern string `json:"globPattern"`
				} `json:"watchers"`
			} `json:"registerOptions"`
		} `json:"registrations"`
	}
	if err := json.Unmarshal(params, &reg); err != nil {
		t.Fatal(err)
	}
	for _, r := range reg.Registrations {
		for _, w := range r.RegisterOptions.Watchers {
			globs = append(globs, w.GlobPattern)
		}
	}
	want := []string{
		"**/*.java",
		"**/cappu.json",
		filepath.ToSlash(filepath.Join(dir, "lib")) + "/**/*.{jar,class}",
		filepath.ToSlash(filepath.Join(dir, "direct.jar")),
	}
	for _, w := range want {
		found := false
		for _, g := range globs {
			if g == w {
				found = true
			}
		}
		if !found {
			t.Errorf("watchers %v missing %q", globs, w)
		}
	}
}

// utilRefSrc references the jar fixture's lib.Util; definitionOnUtil reports
// whether "Util" in an open buffer resolves to the classpath stub - the
// observable that flips when the jar's stubs (re)load. The checker degrades
// unresolvable types without a diagnostic, so definition is the signal.
const utilRefSrc = "import lib.Util;\nclass App { int x = Util.triple(2); }\n"

func definitionOnUtil(t *testing.T, c *testClient, uri string) bool {
	t.Helper()
	result := c.request(t, "textDocument/definition", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     posOf(utilRefSrc, "Util.triple"),
	})
	return strings.Contains(string(result), "classpath:///lib/Util.java")
}

func TestServerConfigChangeReloadsAndKeepsOpenBuffer(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	copyJarFixture(t, filepath.Join(dir, "lib", "util.jar"))
	cfg := writeLspConfig(t, dir, []string{}) // jar present but not on the classpath yet
	c := startTestServerWith(t, cfg)
	c.request(t, "initialize", lsp.InitializeParams{})
	// An open buffer whose content exists nowhere on disk: queries against it
	// keep working after the rebuild only if open documents are re-injected
	// into the new program.
	openDoc(t, c, "file:///Unsaved.java", utilRefSrc)
	if definitionOnUtil(t, c, "file:///Unsaved.java") {
		t.Fatal("lib.Util should not resolve with an empty classPath")
	}

	writeLspConfig(t, dir, []string{"lib"})
	c.notify(t, "workspace/didChangeWatchedFiles", lsp.DidChangeWatchedFilesParams{
		Changes: []lsp.FileEvent{{URI: "file://" + filepath.ToSlash(cfg.ConfigPath), Type: lsp.FileChangeChanged}},
	})
	if !definitionOnUtil(t, c, "file:///Unsaved.java") {
		t.Error("after the cappu.json rewrite, lib.Util should resolve from the open buffer")
	}
}

func TestServerClasspathEventRebuild(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := writeLspConfig(t, dir, []string{"lib"})
	c := startTestServerWith(t, cfg)
	c.request(t, "initialize", lsp.InitializeParams{})
	openDoc(t, c, "file:///App.java", utilRefSrc)
	if definitionOnUtil(t, c, "file:///App.java") {
		t.Fatal("lib.Util should not resolve before the jar exists")
	}

	jar := filepath.Join(lib, "util.jar")
	copyJarFixture(t, jar)
	c.notify(t, "workspace/didChangeWatchedFiles", lsp.DidChangeWatchedFilesParams{
		Changes: []lsp.FileEvent{{URI: "file://" + filepath.ToSlash(jar), Type: lsp.FileChangeCreated}},
	})
	if !definitionOnUtil(t, c, "file:///App.java") {
		t.Error("after the jar appeared, lib.Util should resolve to the classpath stub")
	}
}
