package httpx

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

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
