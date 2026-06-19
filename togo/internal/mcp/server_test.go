package mcp

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// An in-process newline-delimited JSON-RPC round-trip over the MCP server, to
// verify transport + tool dispatch (the TS build has no mcpServer test; the tool
// logic is covered by tools_test/project_test).

type mcpTestClient struct {
	in  *io.PipeWriter
	out *bufio.Reader
	id  int
}

func startMcpTestServer(t *testing.T) *mcpTestClient {
	t.Helper()
	cin, sin := io.Pipe()
	sout, cout := io.Pipe()
	server := NewServer(nil)
	go func() { _ = server.Run(cin, cout) }()
	t.Cleanup(func() { _ = sin.Close(); _ = sout.Close() })
	return &mcpTestClient{in: sin, out: bufio.NewReader(sout)}
}

func (c *mcpTestClient) request(t *testing.T, method string, params any) map[string]any {
	t.Helper()
	c.id++
	msg := map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	body, _ := json.Marshal(msg)
	if _, err := c.in.Write(append(body, '\n')); err != nil {
		t.Fatal(err)
	}
	line, err := c.out.ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

func TestMcpInitialize(t *testing.T) {
	c := startMcpTestServer(t)
	resp := c.request(t, "initialize", map[string]any{"protocolVersion": protocolVersion})
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", resp)
	}
	if result["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion = %v", result["protocolVersion"])
	}
	info, _ := result["serverInfo"].(map[string]any)
	if info["name"] != "cappu" {
		t.Errorf("serverInfo.name = %v", info["name"])
	}
	if !strings.Contains(result["instructions"].(string), "read-only") {
		t.Error("instructions should describe the read-only server")
	}
}

func TestMcpToolsList(t *testing.T) {
	c := startMcpTestServer(t)
	resp := c.request(t, "tools/list", nil)
	result := resp["result"].(map[string]any)
	tools := result["tools"].([]any)
	// Without a config the project tools are absent: the 12 semantic tools remain.
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"diagnostics", "outline", "search_symbols", "describe_symbol", "find_references", "rename_symbol", "type_hierarchy"} {
		if !names[want] {
			t.Errorf("tools/list missing %q", want)
		}
	}
	if names["audit"] {
		t.Error("project tools should be absent without a config")
	}
}

func TestMcpToolCall(t *testing.T) {
	c := startMcpTestServer(t)
	resp := c.request(t, "tools/call", map[string]any{
		"name":      "search_symbols",
		"arguments": map[string]any{"query": "String"},
	})
	result := resp["result"].(map[string]any)
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "java.lang.String") {
		t.Errorf("search_symbols result = %s, want it to contain java.lang.String", text)
	}
}

func TestMcpUnknownTool(t *testing.T) {
	c := startMcpTestServer(t)
	resp := c.request(t, "tools/call", map[string]any{"name": "nope", "arguments": map[string]any{}})
	if resp["error"] == nil {
		t.Errorf("unknown tool should error, got %v", resp)
	}
}
