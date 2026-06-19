package lspserver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"

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
	t.Helper()
	cin, sin := io.Pipe()   // client -> server
	sout, cout := io.Pipe() // server -> client
	server := NewServer(nil)
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
