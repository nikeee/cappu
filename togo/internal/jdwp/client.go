package jdwp

// A JDWP client: connects to a JVM debug port, performs the handshake, and
// multiplexes synchronous command/reply exchanges with asynchronous events over
// one connection. A read goroutine frames packets by length, resolves the
// pending reply keyed by packet id, and routes Event.Composite packets to the
// event callback. Port of src/jdwp/jdwpClient.ts.

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
)

// Error is a non-zero JDWP reply error code.
type Error struct{ Code uint16 }

func (e *Error) Error() string { return fmt.Sprintf("JDWP error %d", e.Code) }

type reply struct {
	data []byte
	err  error
}

// Client is a connected JDWP debugger client.
type Client struct {
	conn     net.Conn
	mu       sync.Mutex
	nextID   uint32
	pending  map[uint32]chan reply
	onEvent  func([]byte)
	closed   bool
	closeErr error

	IDSizes IDSizes
}

// Connect dials a JVM debug port, handshakes, and negotiates ID sizes.
func Connect(host string, port int) (*Client, error) {
	conn, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	return Attach(conn)
}

// Attach drives the handshake + ID-size negotiation over an open connection.
func Attach(conn net.Conn) (*Client, error) {
	c := &Client{conn: conn, nextID: 1, pending: map[uint32]chan reply{}, IDSizes: DefaultIDSizes}
	if _, err := conn.Write([]byte(Handshake)); err != nil {
		return nil, err
	}
	buf := make([]byte, len(Handshake))
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, err
	}
	if string(buf) != Handshake {
		return nil, fmt.Errorf("bad JDWP handshake: %q", string(buf))
	}
	go c.readLoop()
	if err := c.negotiateIDSizes(); err != nil {
		return nil, err
	}
	return c, nil
}

// OnEvent registers the callback for Event.Composite packet bodies. Guarded by
// the mutex because the read goroutine reads onEvent concurrently in handle.
func (c *Client) OnEvent(fn func([]byte)) {
	c.mu.Lock()
	c.onEvent = fn
	c.mu.Unlock()
}

// Send issues a command and returns the reply body (an *Error on a non-zero code).
func (c *Client) Send(set, cmd byte, data []byte) ([]byte, error) {
	c.mu.Lock()
	if c.closed {
		err := c.closeErr
		c.mu.Unlock()
		return nil, err
	}
	id := c.nextID
	c.nextID++
	ch := make(chan reply, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	if _, err := c.conn.Write(EncodeCommandPacket(id, set, cmd, data)); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	r := <-ch
	return r.data, r.err
}

// Close ends the connection.
func (c *Client) Close() { _ = c.conn.Close() }

func (c *Client) negotiateIDSizes() error {
	data, err := c.Send(CSVirtualMachine, VMIDSizes, nil)
	if err != nil {
		return err
	}
	r := NewReader(data)
	c.IDSizes = IDSizes{
		FieldID:         int(r.U4()),
		MethodID:        int(r.U4()),
		ObjectID:        int(r.U4()),
		ReferenceTypeID: int(r.U4()),
		FrameID:         int(r.U4()),
	}
	return nil
}

func (c *Client) readLoop() {
	var buf []byte
	tmp := make([]byte, 8192)
	for {
		n, err := c.conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			for {
				p, rest, ok := TryReadPacket(buf)
				if !ok {
					break
				}
				buf = rest
				c.handle(p)
			}
		}
		if err != nil {
			c.fail(err)
			return
		}
	}
}

func (c *Client) handle(p Packet) {
	if p.IsReply {
		c.mu.Lock()
		ch, ok := c.pending[p.ID]
		delete(c.pending, p.ID)
		c.mu.Unlock()
		if !ok {
			return
		}
		if p.ErrorCode != 0 {
			ch <- reply{err: &Error{Code: p.ErrorCode}}
		} else {
			ch <- reply{data: p.Data}
		}
		return
	}
	if p.CommandSet == CSEvent && p.Command == EVComposite {
		c.mu.Lock()
		fn := c.onEvent
		c.mu.Unlock()
		if fn != nil {
			fn(p.Data)
		}
	}
}

func (c *Client) fail(err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.closeErr = err
	pending := c.pending
	c.pending = map[uint32]chan reply{}
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- reply{err: err}
	}
}
