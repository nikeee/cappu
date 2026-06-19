package lsp

// A minimal JSON-RPC 2.0 connection over the LSP `Content-Length`-framed stream
// (stdio by default). Hand-rolled, as tsgo does, rather than pulling an external
// LSP library. Single-threaded request dispatch (the issue defers concurrency).

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

// ResponseError is a JSON-RPC error returned from a request handler.
type ResponseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *ResponseError) Error() string { return e.Message }

// LSP/JSON-RPC error codes used by the server.
const (
	ErrInvalidRequest = -32600
	ErrInvalidParams  = -32602
)

// RequestHandler handles a request and returns a result or an error.
type RequestHandler func(params json.RawMessage) (any, *ResponseError)

// NotificationHandler handles a notification (no response).
type NotificationHandler func(params json.RawMessage)

// Conn is a JSON-RPC connection.
type Conn struct {
	r        *bufio.Reader
	w        io.Writer
	wmu      sync.Mutex
	requests map[string]RequestHandler
	notifs   map[string]NotificationHandler
	nextID   int
}

// NewConn creates a connection reading from r and writing to w.
func NewConn(r io.Reader, w io.Writer) *Conn {
	return &Conn{
		r:        bufio.NewReader(r),
		w:        w,
		requests: map[string]RequestHandler{},
		notifs:   map[string]NotificationHandler{},
	}
}

// OnRequest registers a request handler for a method.
func (c *Conn) OnRequest(method string, h RequestHandler) { c.requests[method] = h }

// OnNotification registers a notification handler for a method.
func (c *Conn) OnNotification(method string, h NotificationHandler) { c.notifs[method] = h }

type incoming struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type outgoingResult struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
}

type outgoingError struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   *ResponseError  `json:"error"`
}

type outgoingNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type outgoingRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// Notify sends a server->client notification.
func (c *Conn) Notify(method string, params any) error {
	return c.write(outgoingNotification{JSONRPC: "2.0", Method: method, Params: params})
}

// Request sends a server->client request and does not wait for the response
// (the read loop discards client responses). Used for dynamic registration.
func (c *Conn) Request(method string, params any) error {
	c.nextID++
	return c.write(outgoingRequest{JSONRPC: "2.0", ID: c.nextID, Method: method, Params: params})
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

// readMessage reads one Content-Length-framed JSON message.
func (c *Conn) readMessage() ([]byte, error) {
	length := -1
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // end of headers
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

// Run reads and dispatches messages until the stream closes (io.EOF) or a read
// error occurs. Requests with no registered handler get a method-not-found
// error; unknown notifications and client responses are ignored.
func (c *Conn) Run() error {
	for {
		body, err := c.readMessage()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		var msg incoming
		if err := json.Unmarshal(body, &msg); err != nil {
			continue // malformed: skip
		}
		isRequest := len(msg.ID) > 0 && msg.ID[0] != 'n' // not null id
		if msg.Method == "" {
			continue // a response to one of our requests: ignore
		}
		if isRequest {
			c.dispatchRequest(msg)
		} else if h, ok := c.notifs[msg.Method]; ok {
			h(msg.Params)
		}
	}
}

func (c *Conn) dispatchRequest(msg incoming) {
	h, ok := c.requests[msg.Method]
	if !ok {
		_ = c.write(outgoingError{JSONRPC: "2.0", ID: msg.ID, Error: &ResponseError{Code: -32601, Message: "method not found: " + msg.Method}})
		return
	}
	result, rerr := h(msg.Params)
	if rerr != nil {
		_ = c.write(outgoingError{JSONRPC: "2.0", ID: msg.ID, Error: rerr})
		return
	}
	_ = c.write(outgoingResult{JSONRPC: "2.0", ID: msg.ID, Result: result})
}
