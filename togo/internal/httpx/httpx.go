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
	"context"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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

// retryStatuses are the transient HTTP statuses worth retrying: a 429 rate
// limit (Maven Central throttles per-IP after a burst of POM fetches) and the
// 502/503/504 gateway failures. A genuine 404/410 - or any other non-2xx - is
// a real "not here", returned as a miss so the caller tries the next source.
var retryStatuses = map[int]bool{
	http.StatusTooManyRequests:    true, // 429
	http.StatusBadGateway:         true, // 502
	http.StatusServiceUnavailable: true, // 503
	http.StatusGatewayTimeout:     true, // 504
}

const (
	maxFetchAttempts = 6
	baseBackoff      = 500 * time.Millisecond
	maxBackoff       = 20 * time.Second
)

// Get fetches u, retrying transient failures (429/502/503/504) with exponential
// backoff that honors a numeric Retry-After. found is false for a genuine miss
// (404 etc.); an error is returned only when a transient failure persists past
// maxFetchAttempts - so a rate limit surfaces as a real failure instead of a
// false "not found". Used by the package fetchers (one POM at a time).
func Get(client *http.Client, u string) (body []byte, found bool, err error) {
	return get(client, u, time.Sleep, rand.Float64)
}

// get is Get with an injectable sleep and rng so tests retry deterministically
// without real delays.
func get(client *http.Client, u string, sleep func(time.Duration), rng func() float64) ([]byte, bool, error) {
	backoff := baseBackoff
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
		if err != nil {
			return nil, false, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, false, err
		}
		status := resp.StatusCode
		if status >= 200 && status < 300 {
			data, err := ReadAllCapped(resp.Body)
			resp.Body.Close()
			if err != nil {
				return nil, false, err
			}
			return data, true, nil
		}
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		resp.Body.Close()
		if !retryStatuses[status] {
			return nil, false, nil // a genuine miss (404/410/...): not retryable
		}
		if attempt >= maxFetchAttempts {
			return nil, false, fmt.Errorf("%s: HTTP %d after %d attempts (rate limited or server error); try again shortly", hostOf(u), status, maxFetchAttempts)
		}
		// Honor an explicit Retry-After exactly; otherwise full jitter over the
		// capped exponential backoff so the many concurrent retries desync
		// instead of hammering the registry in lockstep (nikeee/cappu#31).
		wait := retryAfter
		if wait == 0 {
			capped := backoff
			if capped > maxBackoff {
				capped = maxBackoff
			}
			wait = time.Duration(rng() * float64(capped))
		}
		sleep(wait)
		backoff *= 2
	}
}

// parseRetryAfter reads the delta-seconds form of a Retry-After header.
//
// ponytail: only the integer-seconds form (what Maven Central sends); an
// HTTP-date Retry-After falls back to exponential backoff. Parse the date form
// if a repository ever uses it.
func parseRetryAfter(v string) time.Duration {
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}

// hostOf is the host of u for error messages, or u itself if it will not parse.
func hostOf(u string) string {
	if parsed, err := url.Parse(u); err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return u
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
