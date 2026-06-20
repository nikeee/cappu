package compile

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nikeee/cappu/internal/config"
)

// End-to-end orchestrator test: RunCompile under the experimental compiler emits
// the same class bytes the emitter baseline records, written to the output tree.
func TestRunCompileExperimentalClasses(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Hello.java")
	if err := os.WriteFile(src, []byte(`public class Hello { public static void main(String[] args) { System.out.println("Hello, world"); } }`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cappu.json"),
		[]byte(`{"compilerOptions":{"output":"classes","experimentalCompiler":{"enabled":true}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "dist")
	result := RunCompile([]string{src}, Options{OutDir: outDir, Config: cfg})
	if !result.Success {
		t.Fatalf("compile failed: %+v", result.Diagnostics)
	}
	got, err := os.ReadFile(filepath.Join(outDir, "Hello.class"))
	if err != nil {
		t.Fatalf("Hello.class not written: %v", err)
	}
	want, err := os.ReadFile(filepath.Join("..", "..", "..", "test-fixtures", "emitter", "emit-baselines", "Hello.class"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("written Hello.class (%d bytes) differs from the emitter baseline (%d bytes)", len(got), len(want))
	}
}

// The experimental compiler reports semantic errors and writes nothing.
func TestRunCompileReportsErrors(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Bad.java")
	if err := os.WriteFile(src, []byte("class Bad { void f(String s) {} void m() { f(1); } }"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cappu.json"),
		[]byte(`{"compilerOptions":{"output":"classes","experimentalCompiler":{"enabled":true}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load("", dir)
	result := RunCompile([]string{src}, Options{OutDir: filepath.Join(dir, "dist"), Config: cfg})
	if result.Success {
		t.Error("expected a failing compile for an undefined symbol")
	}
	if len(result.Written) != 0 {
		t.Errorf("a failing compile must write nothing, wrote %v", result.Written)
	}
	if !hasError(result.Diagnostics) {
		t.Errorf("expected an error diagnostic, got %+v", result.Diagnostics)
	}
}

func TestParseJavacDiagnostics(t *testing.T) {
	out := ParseJavacDiagnostics("A.java:3: error: cannot find symbol\n  int x = y;\n          ^\n1 error\n")
	if len(out) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(out), out)
	}
	if out[0].File != "A.java" || out[0].Line != 3 || out[0].Severity != "error" || out[0].Message != "cannot find symbol" {
		t.Errorf("parsed = %+v", out[0])
	}
}
