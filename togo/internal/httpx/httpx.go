// Package httpx centralizes outbound HTTP for the CLI: one shared client with
// connection-level timeouts (so a stalled server never hangs the process
// forever) and a size-capped body reader (so a hostile or runaway response
// cannot exhaust memory). Whole-request timeouts are deliberately omitted -
// large JDK/release-artifact downloads stream for a long time legitimately, so
// only the connect / TLS-handshake / response-header stages are bounded.
//
// ponytail: transport-level timeouts, not a whole-request Timeout. A single
// Client.Timeout would kill multi-hundred-MB JDK downloads. Add full
// context-cancellation threading from the command entrypoints if Ctrl-C
// responsiveness during a download ever matters.
package httpx

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// Client is the shared outbound HTTP client. Transport timeouts bound the hang
// scenarios (dial, TLS handshake, waiting for response headers) without capping
// total transfer time, so streaming a large download still works.
var Client = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     true,
	},
}

// MaxBodyBytes caps in-memory response reads (ReadAllCapped): generous enough
// for any jar or release artifact we pull fully into memory, small enough that
// a hostile or runaway response cannot exhaust RAM.
const MaxBodyBytes = 256 << 20 // 256 MiB

// ReadAllCapped reads r fully but errors past MaxBodyBytes instead of growing
// without bound. Use for bodies read into memory; streaming-to-file paths read
// the body directly (they are bounded by disk, not RAM).
func ReadAllCapped(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > MaxBodyBytes {
		return nil, fmt.Errorf("response exceeds %d-byte limit", MaxBodyBytes)
	}
	return b, nil
}

// ProgressReader wraps R and reports cumulative bytes read via OnProgress.
// Shared by the streaming download paths (JDK provisioning, self-upgrade).
type ProgressReader struct {
	R          io.Reader
	Total      int64 // -1 when the server sends no Content-Length
	OnProgress func(received, total int64)
	received   int64
}

func (p *ProgressReader) Read(b []byte) (int, error) {
	n, err := p.R.Read(b)
	p.received += int64(n)
	if p.OnProgress != nil {
		p.OnProgress(p.received, p.Total)
	}
	return n, err
}
