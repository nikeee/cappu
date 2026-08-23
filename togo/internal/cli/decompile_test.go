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
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outWrite, errWrite
	code = RunDecompile(files)
	os.Stdout, os.Stderr = oldOut, oldErr
	_ = outWrite.Close()
	_ = errWrite.Close()
	out, _ := io.ReadAll(outRead)
	errs, _ := io.ReadAll(errRead)
	return code, string(out), string(errs)
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
