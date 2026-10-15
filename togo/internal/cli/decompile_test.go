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
	if code != 0 || !strings.Contains(stdout, "return 1;") || !strings.Contains(stdout, "return 2;") ||
		strings.Count(stdout, "int v()") != 2 {
		t.Errorf("expected each build's own nested class, once (exit %d):\n%s\n%s", code, stdout, stderr)
	}
}

// A nested class named on the command line that its outer class does not
// read for itself - not beside it, or another build of it - is written on
// its own rather than dropped; one named twice is one file.
func TestDecompileWritesEveryNamedNestedClass(t *testing.T) {
	if _, err := exec.LookPath("javac"); err != nil {
		t.Skip("no javac")
	}
	dir := t.TempDir()
	nested := map[string]string{}
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
		moved := filepath.Join(dir, "only"+v, "Foo$In.class")
		if err := os.MkdirAll(filepath.Dir(moved), 0o755); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(classes, "p", "Foo$In.class"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(moved, b, 0o644); err != nil {
			t.Fatal(err)
		}
		nested[v] = moved
	}
	if err := os.Symlink(filepath.Join(dir, "only1"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	outer := filepath.Join(dir, "v1", "p", "Foo.class")
	if err := os.Remove(filepath.Join(dir, "v1", "p", "Foo$In.class")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name  string
		files []string
		want  []string
	}{
		{"two copies, neither beside", []string{outer, nested["1"], nested["2"]}, []string{"return 1;", "return 2;"}},
		{"one file named twice", []string{outer, nested["1"], nested["1"]}, []string{"return 1;"}},
		{"one file named by a link too", []string{outer, nested["1"], filepath.Join(dir, "link", "Foo$In.class")}, []string{"return 1;"}},
		{"another build than the one beside", []string{filepath.Join(dir, "v2", "p", "Foo.class"), nested["1"]}, []string{"return 1;", "return 2;"}},
	} {
		code, stdout, stderr := runDecompile(t, c.files...)
		for _, want := range c.want {
			if code != 0 || !strings.Contains(stdout, want) {
				t.Errorf("%s: expected %q (exit %d):\n%s\n%s", c.name, want, code, stdout, stderr)
			}
		}
		if strings.Count(stdout, "int v()") != len(c.want) {
			t.Errorf("%s: expected %d In:\n%s", c.name, len(c.want), stdout)
		}
	}
}

// Classes nested in nested classes are written by the class at the top,
// through its own lookup: a named copy of one that the top reads elsewhere
// is written on its own, and said so. So is one nested deeper than a file
// follows the classes it declares, which no class writes inside.
func TestDecompileWritesDeeplyNestedClassesOnce(t *testing.T) {
	if _, err := exec.LookPath("javac"); err != nil {
		t.Skip("no javac")
	}
	dir := t.TempDir()
	compile := func(name, source string) string {
		path := filepath.Join(dir, name, "Foo.java")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		classes := filepath.Join(dir, name, "c")
		if out, err := exec.Command("javac", "--release", "21", "-d", classes, path).CombinedOutput(); err != nil {
			t.Fatalf("javac: %v\n%s", err, out)
		}
		return filepath.Join(classes, "p")
	}
	two := func(v string) string {
		return "package p;\npublic class Foo { public static class A { public static class B { public int v() { return " + v + "; } } } }\n"
	}
	v1, v2 := compile("v1", two("1")), compile("v2", two("2"))
	// v1's A elsewhere, with v2's B beside it: Foo still writes A and B,
	// both read beside Foo, and A's neighbour is not what it writes.
	other := filepath.Join(dir, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	for from, to := range map[string]string{filepath.Join(v1, "Foo$A.class"): "Foo$A.class", filepath.Join(v2, "Foo$A$B.class"): "Foo$A$B.class"} {
		b, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(other, to), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, stdout, stderr := runDecompile(t, filepath.Join(v1, "Foo.class"), filepath.Join(other, "Foo$A.class"), filepath.Join(other, "Foo$A$B.class"))
	if code != 0 || strings.Count(stdout, "int v()") != 2 || !strings.Contains(stdout, "return 2;") ||
		!strings.Contains(stderr, "Foo$A$B.class: written on its own") {
		t.Errorf("expected v1's B inside Foo and v2's on its own, said so (exit %d):\n%s\n%s", code, stdout, stderr)
	}
	deep := "package p;\npublic class Foo {"
	for _, name := range []string{"A", "B", "C", "D", "E", "F", "G", "H", "I"} {
		deep += " public static class " + name + " {"
	}
	deep += " public int v() { return 42; } " + strings.Repeat("}", 10) + "\n"
	classes := compile("deep", deep)
	all, err := filepath.Glob(filepath.Join(classes, "*.class"))
	if err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runDecompile(t, all...)
	if code != 0 || strings.Count(stdout, "return 42;") != 1 || !strings.Contains(stderr, "Foo$A$B$C$D$E$F$G$H$I.class: written on its own") {
		t.Errorf("expected the class nested past the depth a file follows, once and said so (exit %d):\n%s\n%s", code, stdout, stderr)
	}
	// A chain through a class the run does not name: Foo writes A, which it
	// reads for itself, and A writes B - once.
	v1Files := []string{filepath.Join(v1, "Foo.class"), filepath.Join(v1, "Foo$A$B.class")}
	code, stdout, stderr = runDecompile(t, v1Files...)
	if code != 0 || strings.Count(stdout, "int v()") != 1 || stderr != "" {
		t.Errorf("expected B once, inside A inside Foo (exit %d):\n%s\n%s", code, stdout, stderr)
	}
}

// A top-level class whose name merely begins with a nested class's, `$` and
// all, is no class the other declares: written as it is, without a note.
func TestDecompileSaysNothingOfAClassNobodyDeclares(t *testing.T) {
	if _, err := exec.LookPath("javac"); err != nil {
		t.Skip("no javac")
	}
	dir := t.TempDir()
	for name, source := range map[string]string{
		"Foo.java":     "package p;\npublic class Foo { public static class Ab { } }\n",
		"Foo$Abc.java": "package p;\npublic class Foo$Abc { public int v() { return 7; } }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	classes := filepath.Join(dir, "c")
	if out, err := exec.Command("javac", "--release", "21", "-d", classes, filepath.Join(dir, "Foo.java"), filepath.Join(dir, "Foo$Abc.java")).CombinedOutput(); err != nil {
		t.Fatalf("javac: %v\n%s", err, out)
	}
	code, stdout, stderr := runDecompile(t, filepath.Join(classes, "p", "Foo.class"), filepath.Join(classes, "p", "Foo$Abc.class"))
	if code != 0 || !strings.Contains(stdout, "return 7;") || stderr != "" {
		t.Errorf("expected Foo$Abc written plainly, without a note (exit %d):\n%s\n%s", code, stdout, stderr)
	}
}
