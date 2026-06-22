package dap

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
)

func frame(msg any) string {
	body, _ := json.Marshal(msg)
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
}

// readFrame reads one Content-Length-framed message off r.
func readFrame(r *bufio.Reader) (map[string]any, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if n, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(n), "Content-Length") {
			length, _ = strconv.Atoi(strings.TrimSpace(v))
		}
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	var m map[string]any
	return m, json.Unmarshal(buf, &m)
}

// pipePair wires a Conn so the test can write requests and read its output.
func pipePair(t *testing.T) (in io.Writer, out *bufio.Reader, conn *Conn) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	conn = NewConn(inR, outW)
	go func() { _ = conn.Run() }()
	return inW, bufio.NewReader(outR), conn
}

func TestRequestDispatchedAndAnswered(t *testing.T) {
	in, out, conn := pipePair(t)
	conn.OnRequest("initialize", func(json.RawMessage) (any, error) {
		return Capabilities{SupportsConfigurationDoneRequest: true}, nil
	})
	go fmt.Fprint(in, frame(map[string]any{"seq": 7, "type": "request", "command": "initialize"}))

	resp, err := readFrame(out)
	if err != nil {
		t.Fatal(err)
	}
	if resp["type"] != "response" || resp["command"] != "initialize" || resp["request_seq"].(float64) != 7 || resp["success"] != true {
		t.Fatalf("resp %+v", resp)
	}
}

func TestUnknownRequest(t *testing.T) {
	in, out, _ := pipePair(t)
	go fmt.Fprint(in, frame(map[string]any{"seq": 1, "type": "request", "command": "nope"}))
	resp, err := readFrame(out)
	if err != nil {
		t.Fatal(err)
	}
	if resp["success"] != false || !strings.Contains(resp["message"].(string), "unsupported request 'nope'") {
		t.Fatalf("resp %+v", resp)
	}
}

func TestThrowingHandler(t *testing.T) {
	in, out, conn := pipePair(t)
	conn.OnRequest("launch", func(json.RawMessage) (any, error) {
		return nil, errors.New("no main class")
	})
	go fmt.Fprint(in, frame(map[string]any{"seq": 2, "type": "request", "command": "launch"}))
	resp, err := readFrame(out)
	if err != nil {
		t.Fatal(err)
	}
	if resp["success"] != false || resp["message"] != "no main class" {
		t.Fatalf("resp %+v", resp)
	}
}

func TestSendEventMonotonicSeq(t *testing.T) {
	_, out, conn := pipePair(t)
	// io.Pipe writes block until read, so emit from a goroutine.
	go func() {
		conn.SendEvent("initialized", nil)
		conn.SendEvent("stopped", StoppedEventBody{Reason: "pause", ThreadID: 1})
	}()
	a, err := readFrame(out)
	if err != nil {
		t.Fatal(err)
	}
	b, err := readFrame(out)
	if err != nil {
		t.Fatal(err)
	}
	if a["type"] != "event" || a["event"] != "initialized" {
		t.Fatalf("a %+v", a)
	}
	if b["seq"].(float64) <= a["seq"].(float64) {
		t.Fatalf("seq not increasing: %v then %v", a["seq"], b["seq"])
	}
}
