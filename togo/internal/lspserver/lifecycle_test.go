package lspserver

import (
	"bufio"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/nikeee/cappu/internal/compiler"
)

// The vscode-languageserver lifecycle contract the TS build inherits:
// requests before initialize are rejected (-32002), exit after shutdown ends
// Run with nil (process exit 0), exit without shutdown ends it with
// ErrExitWithoutShutdown (process exit 1).

func startLifecycleServer(t *testing.T) (*testClient, chan error) {
	t.Helper()
	cin, sin := io.Pipe()
	sout, cout := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- NewServer(nil).Run(cin, cout) }()
	c := &testClient{toServer: sin, incoming: make(chan rpcMessage, 64)}
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
	return c, done
}

func waitDone(t *testing.T, done chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop after exit notification")
		return nil
	}
}

func TestExitAfterShutdownStopsCleanly(t *testing.T) {
	c, done := startLifecycleServer(t)
	c.request(t, "initialize", map[string]any{})
	c.request(t, "shutdown", nil)
	c.notify(t, "exit", nil)
	if err := waitDone(t, done); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
}

func TestExitWithoutShutdownReturnsError(t *testing.T) {
	c, done := startLifecycleServer(t)
	c.request(t, "initialize", map[string]any{})
	c.notify(t, "exit", nil)
	if err := waitDone(t, done); !errors.Is(err, ErrExitWithoutShutdown) {
		t.Fatalf("Run = %v, want ErrExitWithoutShutdown", err)
	}
}

func TestRequestsBeforeInitializeRejected(t *testing.T) {
	c, _ := startLifecycleServer(t)
	c.nextID++
	id := c.nextID
	c.send(t, map[string]any{"jsonrpc": "2.0", "id": id, "method": "shutdown"})
	for msg := range c.incoming {
		if msg.idEquals(id) {
			if msg.Error == nil || msg.Error.Code != -32002 {
				t.Fatalf("pre-initialize response = %+v, want code -32002", msg.Error)
			}
			return
		}
	}
	t.Fatal("connection closed before a response")
}

func TestUriRoundTripEncodesSpaces(t *testing.T) {
	p := FsPath("/home/u/My Project/src/A.java")
	uri := pathToURI(p)
	if string(uri) != "file:///home/u/My%20Project/src/A.java" {
		t.Errorf("pathToURI = %s, want percent-encoded space", uri)
	}
	if got := uriToPath(uri); got != p {
		t.Errorf("uriToPath = %s, want %s", got, p)
	}
	if got := uriToPath(compiler.URI("file:///plain/B.java")); got != FsPath("/plain/B.java") {
		t.Errorf("uriToPath(plain) = %s", got)
	}
}
