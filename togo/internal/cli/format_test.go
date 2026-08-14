package cli

// Mirrors src/cli/format.test.ts: the whole `cappu format` path, from
// cappu.json to the bytes on disk. The unit tests cover the grouping itself;
// what only this level can catch is the command dropping
// formatterOptions.importOrder on the way in.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nikeee/cappu/internal/config"
)

const unformattedImports = `package app;

import org.junit.Test;
import java.util.List;

class T {
  List<String> xs;
  Test t;
}
`

// setupFormatProject writes a project whose one source file is grouped the way
// the configured importOrder does NOT want, and chdirs into it.
func setupFormatProject(t *testing.T, configJSON string) (dir, javaFile string, cfg *config.Config) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cappu.json"), []byte(configJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(dir, "src", "main", "java", "app")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	javaFile = filepath.Join(pkg, "T.java")
	if err := os.WriteFile(javaFile, []byte(unformattedImports), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(dir, "cappu.json"), "")
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	t.Chdir(dir)
	return dir, javaFile, cfg
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRunFormatAppliesConfiguredImportOrder(t *testing.T) {
	_, javaFile, cfg := setupFormatProject(t,
		`{ "formatterOptions": { "importOrder": ["org.*", "", "java.*"] } }`)

	// Check mode reports the file and changes nothing: a different grouping is
	// exactly what makes a file unformatted.
	if code := RunFormat(nil, false, cfg); code != 1 {
		t.Errorf("check exit code = %d, want 1", code)
	}
	if got := readFile(t, javaFile); got != unformattedImports {
		t.Errorf("check mode rewrote the file:\n%s", got)
	}

	if code := RunFormat(nil, true, cfg); code != 0 {
		t.Errorf("write exit code = %d, want 0", code)
	}
	want := "package app;\n\nimport org.junit.Test;\n\nimport java.util.List;\n\nclass T {\n  List<String> xs;\n  Test t;\n}\n"
	if got := readFile(t, javaFile); got != want {
		t.Errorf("after --write:\n%q\nwant:\n%q", got, want)
	}

	// The written file is formatted, so a second check passes.
	if code := RunFormat(nil, false, cfg); code != 0 {
		t.Errorf("check exit code after --write = %d, want 0", code)
	}
}

// Without importOrder the command keeps google-java-format's single block, so
// the config's absence is as meaningful as its presence.
func TestRunFormatWithoutImportOrderKeepsOneBlock(t *testing.T) {
	_, javaFile, cfg := setupFormatProject(t, `{}`)
	if code := RunFormat(nil, true, cfg); code != 0 {
		t.Errorf("write exit code = %d, want 0", code)
	}
	want := "package app;\n\nimport java.util.List;\nimport org.junit.Test;\n\nclass T {\n  List<String> xs;\n  Test t;\n}\n"
	if got := readFile(t, javaFile); got != want {
		t.Errorf("after --write:\n%q\nwant:\n%q", got, want)
	}
}
