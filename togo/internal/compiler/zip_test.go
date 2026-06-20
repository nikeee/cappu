package compiler

import (
	"bytes"
	"testing"
)

// Port of src/compiler/zipWriter.test.ts.

func TestZipRoundTrip(t *testing.T) {
	entries := []ZipEntryInput{
		{Name: "META-INF/MANIFEST.MF", Bytes: []byte("Manifest-Version: 1.0\r\n\r\n")},
		{Name: "com/app/Foo.class", Bytes: []byte{0xca, 0xfe, 0xba, 0xbe, 1, 2, 3}},
		{Name: "empty.txt", Bytes: []byte{}},
	}
	zipped := WriteZip(entries)
	read := ReadZipEntries(zipped)
	if read == nil {
		t.Fatal("ReadZipEntries returned nil for a written archive")
	}
	if len(read) != len(entries) {
		t.Fatalf("read %d entries, want %d", len(read), len(entries))
	}
	for i, e := range read {
		if e.Name != entries[i].Name {
			t.Errorf("entry %d name = %q, want %q", i, e.Name, entries[i].Name)
		}
	}
	if got := read[1].Read(); !bytes.Equal(got, entries[1].Bytes) {
		t.Errorf("entry 1 bytes = %v, want %v", got, entries[1].Bytes)
	}
	if got := read[2].Read(); len(got) != 0 {
		t.Errorf("entry 2 should be empty, got %d bytes", len(got))
	}
}

func TestReadZipEntriesRejectsNonZip(t *testing.T) {
	if ReadZipEntries([]byte("not a zip at all")) != nil {
		t.Error("non-zip bytes should read back as nil")
	}
}
