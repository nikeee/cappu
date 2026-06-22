// Package wire implements the Content-Length-framed JSON transport shared by
// the LSP (internal/lsp) and DAP (internal/dap) connections: read one framed
// payload, write one framed payload. The protocol-specific envelopes (JSON-RPC
// vs DAP's seq/type) live in those packages; only the framing is shared here,
// so the two transports are byte-identical on the wire by construction rather
// than by hand. Port of the framing in src/lsp + src/services/dap/transport.ts.
package wire

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// Framer reads and writes Content-Length-framed payloads over a stream. Writes
// are serialized so concurrent senders never interleave a frame.
type Framer struct {
	r   *bufio.Reader
	w   io.Writer
	wmu sync.Mutex
}

func NewFramer(r io.Reader, w io.Writer) *Framer {
	return &Framer{r: bufio.NewReader(r), w: w}
}

// Read returns the body bytes of the next framed message.
func (f *Framer) Read() ([]byte, error) {
	length := -1
	for {
		line, err := f.r.ReadString('\n')
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
	if _, err := io.ReadFull(f.r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// Write frames and writes body. Safe for concurrent callers.
func (f *Framer) Write(body []byte) error {
	f.wmu.Lock()
	defer f.wmu.Unlock()
	if _, err := fmt.Fprintf(f.w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err := f.w.Write(body)
	return err
}
