package compiler

import (
	"archive/zip"
	"bytes"
	"io"
)

// Minimal zip reading/writing for .jar classpath and jar output. Port of
// src/compiler/zipReader.ts + zipWriter.ts. The TS build hand-rolls stored-only
// entries (no node:zlib dependency for writing); Go has archive/zip in the
// standard library, so we use it - stored (uncompressed) entries with a fixed
// epoch keep the archive reproducible, and the reader transparently handles
// both stored and deflated entries. No zip64, no encryption (a jar that large
// is out of scope).

// ZipEntryInput is a single archive member to write.
type ZipEntryInput struct {
	// Name is the forward-slash path inside the archive, e.g. "com/app/Foo.class".
	Name  string
	Bytes []byte
}

// WriteZip packs entries into a zip archive, stored (uncompressed).
func WriteZip(entries []ZipEntryInput) []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		fw, err := w.CreateHeader(&zip.FileHeader{Name: e.Name, Method: zip.Store})
		if err != nil {
			continue
		}
		_, _ = fw.Write(e.Bytes)
	}
	_ = w.Close()
	return buf.Bytes()
}

// ZipEntry is one member of a read archive; Read lazily decompresses it.
type ZipEntry struct {
	Name string
	f    *zip.File
}

// Read returns the decompressed bytes of the entry (empty on failure).
func (e ZipEntry) Read() []byte {
	rc, err := e.f.Open()
	if err != nil {
		return nil
	}
	defer rc.Close()
	out, err := io.ReadAll(rc)
	if err != nil {
		return nil
	}
	return out
}

// ReadZipEntries returns the archive members, or nil when bytes are not a zip.
func ReadZipEntries(data []byte) []ZipEntry {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil
	}
	entries := make([]ZipEntry, 0, len(r.File))
	for _, f := range r.File {
		entries = append(entries, ZipEntry{Name: f.Name, f: f})
	}
	return entries
}
