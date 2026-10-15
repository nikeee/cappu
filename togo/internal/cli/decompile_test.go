package cli

// Mirrors src/cli/decompile.test.ts: `cappu decompile` runs without a project
// config and reports unreadable input. The disassembly itself is covered by
// internal/compiler/disasm_test.go, the reconstruction by
// internal/compiler/decompile_test.go.
//
// The source baselines were written by the TS build, so comparing against them
// here is the TS/Go parity check for the decompiler. UPDATE_BASELINES=1
// rewrites them (and a missing one is always written).

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/baselines"
)

var arithmeticClass = filepath.Join(
	"..", "..", "..", "test-fixtures", "emitter", "emit-baselines", "Arithmetic.class",
)

// runDecompile captures what the command writes to stdout and stderr.
func runDecompile(t *testing.T, files ...string) (code int, stdout, stderr string) {
	return runDecompileMode(t, false, files...)
}

func runDecompileMode(t *testing.T, disasm bool, files ...string) (code int, stdout, stderr string) {
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
	code = RunDecompile(files, disasm)
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

func TestDecompilePrintsSource(t *testing.T) {
	code, stdout, _ := runDecompile(t, arithmeticClass)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, want := range []string{
		"class Arithmetic {", "int add(int arg0, int arg1) {", "return arg0 + arg1;",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestDecompilePrintsListingWithDisasm(t *testing.T) {
	code, stdout, _ := runDecompileMode(t, true, arithmeticClass)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, want := range []string{"class Arithmetic {", "0: iload_1", "iadd"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

// Every baseline the TS build wrote must come back byte for byte. The test set
// is driven by the emitted classes, not by the baseline directory, so a deleted
// baseline is regenerated rather than silently dropped.
func TestDecompileMatchesSourceBaselines(t *testing.T) {
	baselineDir := filepath.Join("..", "..", "..", "test-fixtures", "decompiler", "source-baselines")
	classDir := filepath.Join("..", "..", "..", "test-fixtures", "emitter", "emit-baselines")
	entries, err := os.ReadDir(classDir)
	if err != nil {
		t.Fatalf("read emit baselines: %v", err)
	}
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".class")
		if name == entry.Name() {
			continue
		}
		t.Run(name, func(t *testing.T) {
			classBytes, err := os.ReadFile(filepath.Join(classDir, entry.Name()))
			if err != nil {
				t.Fatalf("read class: %v", err)
			}
			// With the siblings beside the class, the way the CLI and the MCP
			// tools read it: a class whose shape depends on another - a sealed
			// parent, an anonymous body - is only written correctly there.
			classPath := filepath.Join(classDir, entry.Name())
			got, err := DecompileToSourceWith(classBytes, siblingsFor(classPath, classBytes, nil))
			if err != nil {
				t.Fatalf("decompile: %v", err)
			}
			path := filepath.Join(baselineDir, name+".java")
			wrote, err := baselines.Write(path, []byte(got))
			if err != nil {
				t.Fatalf("write baseline %s: %v", path, err)
			}
			if wrote {
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read baseline: %v", err)
			}
			if got != string(want) {
				t.Errorf("%s differs from the baseline:\n--- got ---\n%s\n--- want ---\n%s",
					name, got, want)
			}
		})
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

// A nested class named on the command line is written inside its outer
// class, so the run skips it - and the outer class has to read it from that
// file, wherever it is, or it is lost without a word.
func TestDecompileReadsANamedNestedClassFromWhereItIs(t *testing.T) {
	if _, err := exec.LookPath("javac"); err != nil {
		t.Skip("no javac")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "Foo.java")
	if err := os.WriteFile(source, []byte("package p;\npublic class Foo {\n  public static class In { public int v() { return 3; } }\n  public int w() { return new In().v(); }\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	classes := filepath.Join(dir, "c")
	if out, err := exec.Command("javac", "--release", "21", "-d", classes, source).CombinedOutput(); err != nil {
		t.Fatalf("javac: %v\n%s", err, out)
	}
	other := filepath.Join(dir, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(other, "Foo$In.class")
	if err := os.Rename(filepath.Join(classes, "p", "Foo$In.class"), nested); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runDecompile(t, filepath.Join(classes, "p", "Foo.class"), nested)
	if code != 0 || !strings.Contains(stdout, "class In") || !strings.Contains(stdout, "return 3;") {
		t.Errorf("expected the nested class inside its outer one (exit %d):\n%s\n%s", code, stdout, stderr)
	}
}

// Two builds of one class named in one run: each outer class reads the
// nested one beside it, not whichever file came last.
func TestDecompileKeepsTwoBuildsOfOneClassApart(t *testing.T) {
	if _, err := exec.LookPath("javac"); err != nil {
		t.Skip("no javac")
	}
	dir := t.TempDir()
	var files []string
	for _, v := range []string{"1", "2"} {
		source := filepath.Join(dir, "src"+v, "Foo.java")
		if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(source, []byte("package p;\npublic class Foo {\n  public static class In { public int v() { return "+v+"; } }\n  public int w() { return new In().v(); }\n}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		classes := filepath.Join(dir, "v"+v)
		if out, err := exec.Command("javac", "--release", "21", "-d", classes, source).CombinedOutput(); err != nil {
			t.Fatalf("javac: %v\n%s", err, out)
		}
		files = append(files, filepath.Join(classes, "p", "Foo.class"), filepath.Join(classes, "p", "Foo$In.class"))
	}
	code, stdout, stderr := runDecompile(t, files...)
	if code != 0 || !strings.Contains(stdout, "return 1;") || !strings.Contains(stdout, "return 2;") {
		t.Errorf("expected each build's own nested class (exit %d):\n%s\n%s", code, stdout, stderr)
	}
}
