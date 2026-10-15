package lsp

// A minimal JSON-RPC 2.0 connection over the LSP `Content-Length`-framed stream
// (stdio by default). Hand-rolled, as tsgo does, rather than pulling an external
// LSP library. Requests are dispatched one at a time on the read loop, except
// those registered with OnRequestAsync, which run on their own goroutine so the
// loop stays free for `$/cancelRequest` and further notifications.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/nikeee/cappu/internal/wire"
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

// AsyncRequestHandler handles a request on its own goroutine. ctx is cancelled
// by the client's `$/cancelRequest` for it, or when Run returns; the handler
// still writes the response (a cancelled result is the handler's to shape).
type AsyncRequestHandler func(ctx context.Context, params json.RawMessage) (any, *ResponseError)

// NotificationHandler handles a notification (no response).
type NotificationHandler func(params json.RawMessage)

// Conn is a JSON-RPC connection.
type Conn struct {
	framer   *wire.Framer
	requests map[string]RequestHandler
	notifs   map[string]NotificationHandler
	async    map[string]AsyncRequestHandler
	mu       sync.Mutex
	inFlight map[string]context.CancelFunc // async requests by raw id
	wg       sync.WaitGroup
	nextID   int
	stopped  bool
	stopErr  error
}

// Stop makes Run return err after the current message is dispatched. Handlers
// use it to terminate the read loop (e.g. the LSP `exit` notification).
func (c *Conn) Stop(err error) {
	c.stopped = true
	c.stopErr = err
}

// NewConn creates a connection reading from r and writing to w.
func NewConn(r io.Reader, w io.Writer) *Conn {
	return &Conn{
		framer:   wire.NewFramer(r, w),
		requests: map[string]RequestHandler{},
		notifs:   map[string]NotificationHandler{},
		async:    map[string]AsyncRequestHandler{},
		inFlight: map[string]context.CancelFunc{},
	}
}

// OnRequest registers a request handler for a method.
func (c *Conn) OnRequest(method string, h RequestHandler) { c.requests[method] = h }

// OnNotification registers a notification handler for a method.
func (c *Conn) OnNotification(method string, h NotificationHandler) { c.notifs[method] = h }

// OnRequestAsync registers a request handler that runs on its own goroutine.
func (c *Conn) OnRequestAsync(method string, h AsyncRequestHandler) { c.async[method] = h }

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
	return c.framer.Write(body)
}

// Run reads and dispatches messages until the stream closes (io.EOF) or a read
// error occurs. Requests with no registered handler get a method-not-found
// error; unknown notifications and client responses are ignored. Before it
// returns, it cancels the async requests still running and waits for them.
func (c *Conn) Run() error {
	err := c.loop()
	c.mu.Lock()
	for _, cancel := range c.inFlight {
		cancel()
	}
	c.mu.Unlock()
	c.wg.Wait()
	return err
}

func (c *Conn) loop() error {
	for {
		body, err := c.framer.Read()
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
		} else if msg.Method == "$/cancelRequest" {
			c.cancel(msg.Params)
		} else if h, ok := c.notifs[msg.Method]; ok {
			h(msg.Params)
		}
		if c.stopped {
			return c.stopErr
		}
	}
}

func (c *Conn) dispatchRequest(msg incoming) {
	if h, ok := c.async[msg.Method]; ok {
		ctx, cancel := context.WithCancel(context.Background())
		key := string(msg.ID)
		c.mu.Lock()
		c.inFlight[key] = cancel
		c.mu.Unlock()
		c.wg.Go(func() {
			result, rerr := h(ctx, msg.Params)
			c.mu.Lock()
			delete(c.inFlight, key)
			c.mu.Unlock()
			cancel()
			c.respond(msg.ID, result, rerr)
		})
		return
	}
	h, ok := c.requests[msg.Method]
	if !ok {
		_ = c.write(outgoingError{JSONRPC: "2.0", ID: msg.ID, Error: &ResponseError{Code: -32601, Message: "method not found: " + msg.Method}})
		return
	}
	result, rerr := h(msg.Params)
	c.respond(msg.ID, result, rerr)
}

func (c *Conn) respond(id json.RawMessage, result any, rerr *ResponseError) {
	if rerr != nil {
		_ = c.write(outgoingError{JSONRPC: "2.0", ID: id, Error: rerr})
		return
	}
	_ = c.write(outgoingResult{JSONRPC: "2.0", ID: id, Result: result})
}

// cancel handles `$/cancelRequest`: it cancels the async request with that id,
// if one is still running (sync requests have already been answered).
func (c *Conn) cancel(params json.RawMessage) {
	var p struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if cancel, ok := c.inFlight[string(p.ID)]; ok {
		cancel()
	}
}
