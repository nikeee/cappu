package dapserver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nikeee/cappu/internal/config"
)

// driver speaks DAP to an in-process session over pipes, correlating responses
// by request_seq and letting the test await named events.
type driver struct {
	in  io.Writer
	all chan map[string]any
	buf []map[string]any
	seq int
}

func newDriver(in io.Writer, out io.Reader) *driver {
	d := &driver{in: in, all: make(chan map[string]any, 64)}
	go func() {
		br := bufio.NewReader(out)
		for {
			length := -1
			for {
				line, err := br.ReadString('\n')
				if err != nil {
					return
				}
				line = strings.TrimRight(line, "\r\n")
				if line == "" {
					break
				}
				if n, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(n), "Content-Length") {
					length, _ = strconv.Atoi(strings.TrimSpace(v))
				}
			}
			body := make([]byte, length)
			if _, err := io.ReadFull(br, body); err != nil {
				return
			}
			var m map[string]any
			if json.Unmarshal(body, &m) == nil {
				d.all <- m
			}
		}
	}()
	return d
}

func (d *driver) send(cmd string, args any) int {
	d.seq++
	msg := map[string]any{"seq": d.seq, "type": "request", "command": cmd}
	if args != nil {
		msg["arguments"] = args
	}
	body, _ := json.Marshal(msg)
	fmt.Fprintf(d.in, "Content-Length: %d\r\n\r\n%s", len(body), body)
	return d.seq
}

func (d *driver) take(t *testing.T, pred func(map[string]any) bool) map[string]any {
	t.Helper()
	for i, m := range d.buf {
		if pred(m) {
			d.buf = append(d.buf[:i], d.buf[i+1:]...)
			return m
		}
	}
	for {
		select {
		case m := <-d.all:
			if pred(m) {
				return m
			}
			d.buf = append(d.buf, m)
		case <-time.After(30 * time.Second):
			for _, m := range d.buf {
				t.Logf("buffered: %v", m)
			}
			t.Fatal("timeout waiting for DAP message")
		}
	}
}

func (d *driver) request(t *testing.T, cmd string, args any) map[string]any {
	t.Logf("-> request %s", cmd)
	seq := d.send(cmd, args)
	return d.take(t, func(m map[string]any) bool {
		return m["type"] == "response" && int(m["request_seq"].(float64)) == seq
	})
}

func (d *driver) event(t *testing.T, name string) map[string]any {
	t.Logf("-> waitEvent %s", name)
	return d.take(t, func(m map[string]any) bool { return m["type"] == "event" && m["event"] == name })
}

// TestDebugAppLive drives a full DAP session against a real JVM: stop on a
// breakpoint, inspect locals, step, continue. Self-skips without a JDK.
func TestDebugAppLive(t *testing.T) {
	if _, err := exec.LookPath("javac"); err != nil {
		t.Skip("javac not on PATH")
	}
	work := filepath.Join(t.TempDir(), "debug-app")
	src := filepath.Join("..", "..", "..", "examples", "debug-app")
	if err := os.CopyFS(work, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load("", work)
	if err != nil {
		t.Fatal(err)
	}
	appJava := filepath.Join(work, "src", "main", "java", "example", "App.java")

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go func() { _ = Run(cfg, inR, outW) }()
	defer func() { _ = inW.Close() }()
	d := newDriver(inW, outR)

	if d.request(t, "initialize", map[string]any{"adapterID": "cappu"})["success"] != true {
		t.Fatal("initialize failed")
	}
	d.event(t, "initialized")
	if d.request(t, "launch", map[string]any{})["success"] != true {
		t.Fatal("launch failed")
	}
	d.request(t, "setBreakpoints", map[string]any{
		"source":      map[string]any{"path": appJava},
		"breakpoints": []map[string]any{{"line": 8}},
	})
	d.request(t, "configurationDone", nil)

	stopped := d.event(t, "stopped")
	body := stopped["body"].(map[string]any)
	if body["reason"] != "breakpoint" {
		t.Fatalf("stop reason %v", body["reason"])
	}
	threadID := int(body["threadId"].(float64))

	stack := d.request(t, "stackTrace", map[string]any{"threadId": threadID})
	frames := stack["body"].(map[string]any)["stackFrames"].([]any)
	top := frames[0].(map[string]any)
	if int(top["line"].(float64)) != 8 {
		t.Fatalf("top frame line %v", top["line"])
	}
	frameID := int(top["id"].(float64))

	scopes := d.request(t, "scopes", map[string]any{"frameId": frameID})
	ref := int(scopes["body"].(map[string]any)["scopes"].([]any)[0].(map[string]any)["variablesReference"].(float64))
	vars := d.request(t, "variables", map[string]any{"variablesReference": ref})
	byName := map[string]string{}
	for _, v := range vars["body"].(map[string]any)["variables"].([]any) {
		vm := v.(map[string]any)
		byName[vm["name"].(string)] = vm["value"].(string)
	}
	if byName["i"] != "1" || byName["squared"] != "1" {
		t.Fatalf("locals: i=%q squared=%q", byName["i"], byName["squared"])
	}

	// Step over once: the step pipeline must produce another stop.
	d.request(t, "next", map[string]any{"threadId": threadID})
	stepped := d.event(t, "stopped")
	if stepped["body"].(map[string]any)["reason"] != "step" {
		t.Fatalf("expected step stop, got %v", stepped["body"])
	}

	d.request(t, "disconnect", nil)
}
