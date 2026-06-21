package compiler

import (
	"os"
	"path/filepath"
	"testing"
)

// Mirrors src/compiler/validateJavac.test.ts "an unavailable javac yields an
// error result, not a throw": a missing javac binary must surface as an Error
// result rather than panicking.
func TestValidateAgainstJavacUnavailableBinary(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "X.java")
	if err := os.WriteFile(src, []byte("class X { }"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := ValidateAgainstJavac([]string{src}, nil, "cappu-no-such-javac")
	if result.OK {
		t.Error("expected OK=false for an unavailable javac")
	}
	if result.Error == "" {
		t.Error("expected a non-empty Error for an unavailable javac")
	}
}
