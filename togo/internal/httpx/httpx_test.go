package httpx

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Regression for nikeee/cappu#22: a 429 (Central rate-limiting a burst of POM
// fetches) must be retried, not silently treated as a 404-style miss that the
// resolver then reports as "not found in any package source".

func TestGetRetriesTransientThenSucceeds(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls < 2 {
			w.WriteHeader(http.StatusTooManyRequests) // 429 once
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	var slept []time.Duration
	body, found, err := get(server.Client(), server.URL, func(d time.Duration) { slept = append(slept, d) })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found || string(body) != "ok" {
		t.Fatalf("found=%v body=%q, want true \"ok\"", found, body)
	}
	if calls != 2 {
		t.Errorf("server calls = %d, want 2 (one retry)", calls)
	}
	if len(slept) != 1 || slept[0] != baseBackoff {
		t.Errorf("backoff sleeps = %v, want one %v", slept, baseBackoff)
	}
}

func TestGetPersistentTransientIsErrorNotMiss(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable) // 503 forever
	}))
	defer server.Close()

	body, found, err := get(server.Client(), server.URL, func(time.Duration) {})
	if err == nil {
		t.Fatal("want an error after exhausting retries, got nil (would be reported as a miss)")
	}
	if found || body != nil {
		t.Errorf("found=%v body=%q, want false/nil", found, body)
	}
	if calls != maxFetchAttempts {
		t.Errorf("server calls = %d, want %d", calls, maxFetchAttempts)
	}
}

func TestGetGenuine404IsMiss(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	body, found, err := get(server.Client(), server.URL, func(time.Duration) { t.Fatal("404 must not retry") })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found || body != nil {
		t.Errorf("found=%v body=%q, want false/nil", found, body)
	}
	if calls != 1 {
		t.Errorf("server calls = %d, want 1 (no retry on 404)", calls)
	}
}

func TestGetHonorsRetryAfter(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls < 2 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	var slept []time.Duration
	if _, _, err := get(server.Client(), server.URL, func(d time.Duration) { slept = append(slept, d) }); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 1 || slept[0] != 2*time.Second {
		t.Errorf("sleeps = %v, want one 2s (Retry-After)", slept)
	}
}

func TestReadAllCappedUnderLimit(t *testing.T) {
	got, err := ReadAllCapped(strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestReadAllCappedOverLimit(t *testing.T) {
	// A reader that yields MaxBodyBytes+1 bytes must error, not allocate forever.
	r := io.LimitReader(neverEnding{}, MaxBodyBytes+1)
	if _, err := ReadAllCapped(r); err == nil {
		t.Fatal("expected over-limit error, got nil")
	}
}

func TestProgressReaderReportsCumulative(t *testing.T) {
	var last int64
	pr := &ProgressReader{R: bytes.NewReader(make([]byte, 10)), Total: 10, OnProgress: func(received, _ int64) { last = received }}
	if _, err := io.ReadAll(pr); err != nil {
		t.Fatal(err)
	}
	if last != 10 {
		t.Fatalf("progress = %d, want 10", last)
	}
}

type neverEnding struct{}

func (neverEnding) Read(b []byte) (int, error) {
	for i := range b {
		b[i] = 'x'
	}
	return len(b), nil
}
