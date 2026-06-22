// Package dap implements the Debug Adapter Protocol wire transport (a
// Content-Length-framed JSON envelope) and the message types cappu's adapter
// exchanges with a DAP client.
package dap

// The Debug Adapter Protocol wire transport: the same Content-Length-framed
// stream LSP uses (internal/lsp/conn.go), but with DAP's envelope (a monotonic
// seq, a type of request/response/event) instead of JSON-RPC. Hand-rolled to
// match the LSP side so the implementations stay byte-comparable. Port of
// src/services/dap/transport.ts.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// Request is an incoming DAP request.
type Request struct {
	Seq       int             `json:"seq"`
	Type      string          `json:"type"`
	Command   string          `json:"command"`
	Arguments json.RawMessage `json:"arguments"`
}

// RequestHandler handles a request, returning a response body or an error
// (which becomes a success:false response carrying the error message).
type RequestHandler func(args json.RawMessage) (any, error)

// Conn is a DAP connection bound to one debug session.
type Conn struct {
	r        *bufio.Reader
	w        io.Writer
	wmu      sync.Mutex
	seq      int
	handlers map[string]RequestHandler
}

func NewConn(r io.Reader, w io.Writer) *Conn {
	return &Conn{r: bufio.NewReader(r), w: w, seq: 1, handlers: map[string]RequestHandler{}}
}

func (c *Conn) OnRequest(command string, h RequestHandler) { c.handlers[command] = h }

type response struct {
	Seq        int    `json:"seq"`
	Type       string `json:"type"`
	RequestSeq int    `json:"request_seq"`
	Success    bool   `json:"success"`
	Command    string `json:"command"`
	Message    string `json:"message,omitempty"`
	Body       any    `json:"body,omitempty"`
}

type event struct {
	Seq   int    `json:"seq"`
	Type  string `json:"type"`
	Event string `json:"event"`
	Body  any    `json:"body,omitempty"`
}

// SendEvent pushes a DAP event (stopped, output, terminated, ...) to the client.
func (c *Conn) SendEvent(name string, body any) {
	c.wmu.Lock()
	seq := c.seq
	c.seq++
	c.wmu.Unlock()
	_ = c.write(event{Seq: seq, Type: "event", Event: name, Body: body})
}

// Run reads and dispatches requests until the stream closes.
func (c *Conn) Run() error {
	for {
		body, err := c.readMessage()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		var req Request
		if err := json.Unmarshal(body, &req); err != nil {
			continue
		}
		if req.Type != "request" {
			continue
		}
		c.dispatch(req)
	}
}

func (c *Conn) dispatch(req Request) {
	h, ok := c.handlers[req.Command]
	if !ok {
		c.respond(req, false, nil, fmt.Sprintf("unsupported request '%s'", req.Command))
		return
	}
	body, err := h(req.Arguments)
	if err != nil {
		c.respond(req, false, nil, err.Error())
		return
	}
	c.respond(req, true, body, "")
}

func (c *Conn) respond(req Request, success bool, body any, message string) {
	c.wmu.Lock()
	seq := c.seq
	c.seq++
	c.wmu.Unlock()
	_ = c.write(response{
		Seq:        seq,
		Type:       "response",
		RequestSeq: req.Seq,
		Success:    success,
		Command:    req.Command,
		Message:    message,
		Body:       body,
	})
}

func (c *Conn) write(msg any) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = c.w.Write(body)
	return err
}

func (c *Conn) readMessage() ([]byte, error) {
	length := -1
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if name, value, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			length, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, fmt.Errorf("invalid Content-Length: %w", err)
			}
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(c.r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
