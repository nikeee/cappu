package bspserver

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/wire"
)

type msg struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
}

type client struct {
	t      *testing.T
	framer *wire.Framer
	nextID int
	done   chan error
}

func start(t *testing.T, dir string) *client {
	t.Helper()
	cfg, err := config.Load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	cin, sin := io.Pipe()
	sout, cout := io.Pipe()
	c := &client{t: t, framer: wire.NewFramer(sout, sin), done: make(chan error, 1)}
	go func() { c.done <- Run(cfg, cin, cout); _ = cout.Close() }()
	t.Cleanup(func() { _ = sin.Close(); _ = sout.Close() })
	return c
}

// call sends a request and returns its result plus the notifications that
// arrived before it.
func (c *client) call(method string, params any) (json.RawMessage, []msg) {
	c.t.Helper()
	c.nextID++
	c.send(map[string]any{"jsonrpc": "2.0", "id": c.nextID, "method": method, "params": params})
	var notes []msg
	for {
		body, err := c.framer.Read()
		if err != nil {
			c.t.Fatalf("%s: %v", method, err)
		}
		var m msg
		_ = json.Unmarshal(body, &m)
		if m.Method != "" {
			notes = append(notes, m)
			continue
		}
		return m.Result, notes
	}
}

func (c *client) send(v any) {
	body, _ := json.Marshal(v)
	if err := c.framer.Write(body); err != nil {
		c.t.Fatal(err)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleAndTargets(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "src/main/java/app/M.java"), "package app; public class M {}")
	write(t, filepath.Join(dir, "src/test/java/app/MTest.java"), "package app; class MTest {}")
	c := start(t, dir)

	var init struct {
		DisplayName string `json:"displayName"`
		BspVersion  string `json:"bspVersion"`
	}
	res, _ := c.call("build/initialize", map[string]any{})
	_ = json.Unmarshal(res, &init)
	if init.DisplayName != "cappu" || init.BspVersion != BSPVersion {
		t.Fatalf("initialize: %s", res)
	}

	var targets struct {
		Targets []struct {
			ID           targetID   `json:"id"`
			Dependencies []targetID `json:"dependencies"`
		} `json:"targets"`
	}
	res, _ = c.call("workspace/buildTargets", nil)
	_ = json.Unmarshal(res, &targets)
	if len(targets.Targets) != 2 || targets.Targets[1].Dependencies[0] != targets.Targets[0].ID {
		t.Fatalf("buildTargets: %s", res)
	}

	var sources struct {
		Items []struct {
			Sources []struct {
				URI string `json:"uri"`
			} `json:"sources"`
		} `json:"items"`
	}
	res, _ = c.call("buildTarget/sources", map[string]any{"targets": []targetID{targets.Targets[1].ID}})
	_ = json.Unmarshal(res, &sources)
	if want := pathToURI(filepath.Join(dir, "src/test/java")) + "/"; len(sources.Items) != 1 || sources.Items[0].Sources[0].URI != want {
		t.Fatalf("sources: %s, want %s", res, want)
	}

	c.call("build/shutdown", nil)
	c.send(map[string]any{"jsonrpc": "2.0", "method": "build/exit"})
	if err := <-c.done; err != nil {
		t.Fatalf("exit after shutdown: %v", err)
	}
}

func TestExitWithoutShutdown(t *testing.T) {
	c := start(t, t.TempDir())
	c.send(map[string]any{"jsonrpc": "2.0", "method": "build/exit"})
	if err := <-c.done; !errors.Is(err, ErrExitWithoutShutdown) {
		t.Fatalf("got %v", err)
	}
}

// buildTarget/javacOptions reports compilerOptions.javacOptions for both targets.
func TestJavacOptionsReportsConfiguredFlags(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "cappu.json"), `{ "compilerOptions": { "javacOptions": ["-parameters"] } }`)
	c := start(t, dir)
	res, _ := c.call("buildTarget/javacOptions", map[string]any{"targets": []targetID{
		{URI: pathToURI(dir) + "?id=main"}, {URI: pathToURI(dir) + "?id=test"},
	}})
	var r struct {
		Items []struct {
			Options []string `json:"options"`
		} `json:"items"`
	}
	_ = json.Unmarshal(res, &r)
	if len(r.Items) != 2 || !slices.Equal(r.Items[0].Options, []string{"-parameters"}) || !slices.Equal(r.Items[1].Options, []string{"-parameters"}) {
		t.Fatalf("javacOptions: %s", res)
	}
}

// A compile error is published for its file and fails the build; once fixed,
// the file gets an empty reset and the build succeeds.
func TestCompilePublishesAndClearsDiagnostics(t *testing.T) {
	if _, err := exec.LookPath("javac"); err != nil {
		t.Skip("javac not available")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src/main/java/app/M.java")
	write(t, src, `package app; public class M { int x = "s"; }`)
	c := start(t, dir)
	c.call("build/initialize", map[string]any{})
	main := map[string]any{"targets": []targetID{{URI: pathToURI(dir) + "?id=main"}}, "originId": "o1"}

	type diagNote struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Diagnostics []json.RawMessage `json:"diagnostics"`
	}
	compileOnce := func() (int, map[string]int) {
		res, notes := c.call("buildTarget/compile", main)
		var r struct {
			StatusCode int `json:"statusCode"`
		}
		_ = json.Unmarshal(res, &r)
		published := map[string]int{}
		for _, n := range notes {
			if n.Method == "build/publishDiagnostics" {
				var d diagNote
				_ = json.Unmarshal(n.Params, &d)
				published[d.TextDocument.URI] = len(d.Diagnostics)
			}
		}
		return r.StatusCode, published
	}

	status, published := compileOnce()
	if status != statusError || published[pathToURI(src)] == 0 {
		t.Fatalf("broken build: status %d, published %v", status, published)
	}
	write(t, src, `package app; public class M { int x = 1; }`)
	status, published = compileOnce()
	if n, ok := published[pathToURI(src)]; status != statusOK || !ok || n != 0 {
		t.Fatalf("fixed build: status %d, published %v", status, published)
	}
}

// run compiles main, runs its single main class with the request's arguments
// and streams stdout / stderr as run/print* notifications.
func TestRunStreamsOutput(t *testing.T) {
	if _, err := exec.LookPath("javac"); err != nil {
		t.Skip("javac not available")
	}
	dir := t.TempDir()
	write(t, filepath.Join(dir, "src/main/java/app/M.java"), `package app; public class M {
  public static void main(String[] a) { System.out.println("hi " + a[0]); System.err.print("oops"); System.exit(3); } }`)
	c := start(t, dir)
	c.call("build/initialize", map[string]any{})
	res, notes := c.call("buildTarget/run", map[string]any{
		"target": targetID{URI: pathToURI(dir) + "?id=main"}, "originId": "r", "arguments": []string{"there"},
	})
	var r struct {
		StatusCode int `json:"statusCode"`
	}
	_ = json.Unmarshal(res, &r)
	printed := map[string]string{}
	for _, n := range notes {
		var p struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(n.Params, &p)
		printed[n.Method] += p.Message
	}
	if r.StatusCode != statusError || printed["run/printStdout"] != "hi there\n" || printed["run/printStderr"] != "oops\n" {
		t.Fatalf("status %d, printed %q", r.StatusCode, printed)
	}
}

// read returns the next message from the server.
func (c *client) read() msg {
	c.t.Helper()
	body, err := c.framer.Read()
	if err != nil {
		c.t.Fatal(err)
	}
	var m msg
	_ = json.Unmarshal(body, &m)
	return m
}

// A running program reads run/readStdin, and cancelling the request kills it:
// the program echoes one line, then blocks on stdin until cancelled.
func TestRunStdinAndCancel(t *testing.T) {
	if _, err := exec.LookPath("javac"); err != nil {
		t.Skip("javac not available")
	}
	dir := t.TempDir()
	write(t, filepath.Join(dir, "src/main/java/app/M.java"), `package app; public class M {
  public static void main(String[] a) throws Exception {
    var in = new java.io.BufferedReader(new java.io.InputStreamReader(System.in));
    System.out.println("ready");
    System.out.println("got " + in.readLine());
    in.readLine(); } }`)
	c := start(t, dir)
	c.call("build/initialize", map[string]any{})
	c.send(map[string]any{"jsonrpc": "2.0", "id": 99, "method": "buildTarget/run", "params": map[string]any{
		"target": targetID{URI: pathToURI(dir) + "?id=main"}, "originId": "r",
	}})
	printed := func() string {
		for {
			m := c.read()
			if m.Method == "run/printStdout" {
				var p struct {
					Message string `json:"message"`
				}
				_ = json.Unmarshal(m.Params, &p)
				return p.Message
			}
			if m.Method == "" {
				t.Fatalf("run ended early: %s", m.Result)
			}
		}
	}
	if got := printed(); got != "ready\n" {
		t.Fatalf("got %q", got)
	}
	c.send(map[string]any{"jsonrpc": "2.0", "method": "run/readStdin", "params": map[string]any{"originId": "r", "value": "line\n"}})
	if got := printed(); got != "got line\n" {
		t.Fatalf("got %q", got)
	}
	c.send(map[string]any{"jsonrpc": "2.0", "method": "$/cancelRequest", "params": map[string]any{"id": 99}})
	for {
		if m := c.read(); m.Method == "" {
			var r struct {
				StatusCode int `json:"statusCode"`
			}
			_ = json.Unmarshal(m.Result, &r)
			if string(m.ID) != "99" || r.StatusCode != statusCancelled {
				t.Fatalf("response %s: %s", m.ID, m.Result)
			}
			return
		}
	}
}

func TestLineWriter(t *testing.T) {
	var got []string
	w := newLineWriter(func(l string) { got = append(got, l) })
	_, _ = w.Write([]byte("a\r\nb"))
	_, _ = w.Write([]byte("c\n\nd"))
	w.flush()
	if want := []string{"a", "bc", "", "d"}; !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
