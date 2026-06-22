package wire

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	f := NewFramer(strings.NewReader(""), &buf)
	if err := f.Write([]byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "Content-Length: 7\r\n\r\n"+`{"a":1}` {
		t.Fatalf("framed output = %q", got)
	}

	// Read it back through a fresh framer.
	r := NewFramer(strings.NewReader(buf.String()), io.Discard)
	body, err := r.Read()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"a":1}` {
		t.Fatalf("read body = %q", body)
	}
}

func TestReadMissingContentLength(t *testing.T) {
	f := NewFramer(strings.NewReader("X-Other: 1\r\n\r\n"), io.Discard)
	if _, err := f.Read(); err == nil {
		t.Fatal("expected missing Content-Length error")
	}
}
