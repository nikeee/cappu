package mcp

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nikeee/cappu/internal/config"
)

// An in-process newline-delimited JSON-RPC round-trip over the MCP server, to
// verify transport + tool dispatch (the TS build has no mcpServer test; the tool
// logic is covered by tools_test/project_test).

type mcpTestClient struct {
	in  *io.PipeWriter
	out *bufio.Reader
	id  int
}

func startMcpTestServer(t *testing.T, cfg *config.Config) *mcpTestClient {
	t.Helper()
	cin, sin := io.Pipe()
	sout, cout := io.Pipe()
	server := NewServer(cfg)
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
	c := startMcpTestServer(t, nil)
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
	c := startMcpTestServer(t, nil)
	resp := c.request(t, "tools/list", nil)
	result := resp["result"].(map[string]any)
	tools := result["tools"].([]any)
	// Without a config the project tools are absent: the 13 semantic tools remain.
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
	c := startMcpTestServer(t, nil)
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

// describeSymbol calls the describe_symbol tool and returns its text content.
func describeSymbol(t *testing.T, c *mcpTestClient, ref string) string {
	t.Helper()
	resp := c.request(t, "tools/call", map[string]any{
		"name":      "describe_symbol",
		"arguments": map[string]any{"ref": ref},
	})
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", resp)
	}
	content := result["content"].([]any)
	return content[0].(map[string]any)["text"].(string)
}

func TestMcpClasspathTypesResolve(t *testing.T) {
	jar, err := filepath.Abs(filepath.Join("..", "compiler", "testdata", "classfiles", "util.jar"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{BaseDir: t.TempDir()}
	cfg.CompilerOptions.ClassPath = []string{jar}
	cfg.CompilerOptions.SourcePaths = []string{}
	c := startMcpTestServer(t, cfg)
	if text := describeSymbol(t, c, "lib.Util"); !strings.Contains(text, "classpath:///lib/Util.java") {
		t.Errorf("describe_symbol(lib.Util) = %s, want the classpath type to resolve", text)
	}
}

func TestMcpGeneratedSourcesLoaded(t *testing.T) {
	dir := t.TempDir()
	gen := filepath.Join(dir, ".cappu", "generated-sources", "sources", "pkg")
	if err := os.MkdirAll(gen, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gen, "Gen.java"), []byte("package pkg;\npublic class Gen {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{BaseDir: dir}
	cfg.CompilerOptions.ClassPath = []string{}
	cfg.CompilerOptions.SourcePaths = []string{}
	c := startMcpTestServer(t, cfg)
	if text := describeSymbol(t, c, "pkg.Gen"); !strings.Contains(text, "class Gen") {
		t.Errorf("describe_symbol(pkg.Gen) = %s, want the generated source to resolve", text)
	}
}

// copyFile copies src to dst (small test fixtures only).
func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func utilJarPath(t *testing.T) string {
	t.Helper()
	jar, err := filepath.Abs(filepath.Join("..", "compiler", "testdata", "classfiles", "util.jar"))
	if err != nil {
		t.Fatal(err)
	}
	return jar
}

const utilStubURI = "classpath:///lib/Util.java"

func TestMcpClasspathJarAppears(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{BaseDir: dir}
	cfg.CompilerOptions.ClassPath = []string{"lib"}
	cfg.CompilerOptions.SourcePaths = []string{}
	c := startMcpTestServer(t, cfg)
	if text := describeSymbol(t, c, "lib.Util"); strings.Contains(text, utilStubURI) {
		t.Fatalf("lib.Util resolved before the jar exists: %s", text)
	}
	copyFile(t, utilJarPath(t), filepath.Join(lib, "util.jar"))
	if text := describeSymbol(t, c, "lib.Util"); !strings.Contains(text, utilStubURI) {
		t.Errorf("describe_symbol(lib.Util) = %s, want the new jar to be picked up", text)
	}
}

func TestMcpClasspathJarRemoved(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	jar := filepath.Join(lib, "util.jar")
	copyFile(t, utilJarPath(t), jar)
	cfg := &config.Config{BaseDir: dir}
	cfg.CompilerOptions.ClassPath = []string{"lib"}
	cfg.CompilerOptions.SourcePaths = []string{}
	c := startMcpTestServer(t, cfg)
	if text := describeSymbol(t, c, "lib.Util"); !strings.Contains(text, utilStubURI) {
		t.Fatalf("lib.Util should resolve while the jar exists: %s", text)
	}
	if err := os.Remove(jar); err != nil {
		t.Fatal(err)
	}
	if text := describeSymbol(t, c, "lib.Util"); strings.Contains(text, utilStubURI) {
		t.Errorf("describe_symbol(lib.Util) = %s, want the stale stub gone after jar removal", text)
	}
}

// writeConfigFile writes a cappu.json with the given classPath entry and a
// distinct mtime, so the per-call config stat sees every rewrite.
func writeConfigFile(t *testing.T, dir, classPath string, mtime time.Time) string {
	t.Helper()
	path := filepath.Join(dir, "cappu.json")
	body := `{"compilerOptions":{"classPath":["` + classPath + `"],"sourcePaths":[]}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMcpConfigReloadChangesClasspath(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"libA", "libB"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	copyFile(t, utilJarPath(t), filepath.Join(dir, "libB", "util.jar"))
	base := time.Now().Add(-time.Hour)
	path := writeConfigFile(t, dir, "libA", base)
	cfg, err := config.Load(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	c := startMcpTestServer(t, cfg)
	if text := describeSymbol(t, c, "lib.Util"); strings.Contains(text, utilStubURI) {
		t.Fatalf("lib.Util resolved with libA on the classpath: %s", text)
	}
	writeConfigFile(t, dir, "libB", base.Add(time.Minute))
	if text := describeSymbol(t, c, "lib.Util"); !strings.Contains(text, utilStubURI) {
		t.Errorf("describe_symbol(lib.Util) = %s, want the rewritten cappu.json picked up", text)
	}
}

func TestMcpMalformedConfigKeepsState(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, utilJarPath(t), filepath.Join(dir, "lib", "util.jar"))
	base := time.Now().Add(-time.Hour)
	path := writeConfigFile(t, dir, "lib", base)
	cfg, err := config.Load(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	c := startMcpTestServer(t, cfg)
	if text := describeSymbol(t, c, "lib.Util"); !strings.Contains(text, utilStubURI) {
		t.Fatalf("lib.Util should resolve before the broken edit: %s", text)
	}
	if err := os.WriteFile(path, []byte("{ nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, base.Add(time.Minute), base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	for range 2 { // logged once, old state kept on every later call
		if text := describeSymbol(t, c, "lib.Util"); !strings.Contains(text, utilStubURI) {
			t.Errorf("describe_symbol(lib.Util) = %s, want the last good config kept", text)
		}
	}
}

func TestMcpUnknownTool(t *testing.T) {
	c := startMcpTestServer(t, nil)
	resp := c.request(t, "tools/call", map[string]any{"name": "nope", "arguments": map[string]any{}})
	if resp["error"] == nil {
		t.Errorf("unknown tool should error, got %v", resp)
	}
}
