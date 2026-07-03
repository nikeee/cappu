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

func TestReadResyncsPastHeaderWithoutContentLength(t *testing.T) {
	// A bad header block must not kill the session; the next valid frame is
	// still delivered (matches the TS transport's resync).
	f := NewFramer(strings.NewReader("X-Other: 1\r\n\r\nContent-Length: 2\r\n\r\nok"), io.Discard)
	body, err := f.Read()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ok" {
		t.Fatalf("read body = %q, want %q", body, "ok")
	}

	// A stream that ends with only bad blocks reports EOF.
	f = NewFramer(strings.NewReader("X-Other: 1\r\n\r\n"), io.Discard)
	if _, err := f.Read(); err == nil {
		t.Fatal("expected EOF after only bad header blocks")
	}
}
