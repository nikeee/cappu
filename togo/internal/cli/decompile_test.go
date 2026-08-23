package cli

// Mirrors src/cli/decompile.test.ts: `cappu decompile` runs without a project
// config and reports unreadable input. The disassembly itself is covered by
// internal/compiler/disasm_test.go.

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var arithmeticClass = filepath.Join(
	"..", "..", "..", "test-fixtures", "emitter", "emit-baselines", "Arithmetic.class",
)

// runDecompile captures what the command writes to stdout and stderr.
func runDecompile(t *testing.T, files ...string) (code int, stdout, stderr string) {
	t.Helper()
	outRead, outWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errRead, errWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// Drain both pipes while the command runs: a listing larger than the pipe
	// buffer (~64 KiB) would otherwise block the writer forever.
	outDone := readAll(outRead)
	errDone := readAll(errRead)
	oldOut, oldErr := os.Stdout, os.Stderr
	defer func() {
		os.Stdout, os.Stderr = oldOut, oldErr
	}()
	os.Stdout, os.Stderr = outWrite, errWrite
	code = RunDecompile(files)
	os.Stdout, os.Stderr = oldOut, oldErr
	_ = outWrite.Close()
	_ = errWrite.Close()
	return code, <-outDone, <-errDone
}

// readAll drains a pipe in the background and yields its contents at EOF.
func readAll(r *os.File) <-chan string {
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	return done
}

func TestDecompilePrintsListing(t *testing.T) {
	code, stdout, _ := runDecompile(t, arithmeticClass)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, want := range []string{"class Arithmetic {", "0: iload_1", "iadd"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestDecompileWithoutFiles(t *testing.T) {
	code, _, stderr := runDecompile(t)
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "usage: cappu decompile") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestDecompileRejectsOtherFiles(t *testing.T) {
	notAClass := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(notAClass, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runDecompile(t, notAClass)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "not a class file") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestDecompileReportsDirectory(t *testing.T) {
	dir := t.TempDir()
	code, _, stderr := runDecompile(t, dir)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	// Same wording as the TS build (src/cli/decompile.test.ts).
	if !strings.Contains(stderr, "is a directory") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestDecompileContinuesAfterBadFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.class")
	code, stdout, stderr := runDecompile(t, missing, arithmeticClass)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "missing.class") {
		t.Errorf("stderr = %q", stderr)
	}
	if !strings.Contains(stdout, "class Arithmetic {") {
		t.Errorf("stdout = %q", stdout)
	}
}
