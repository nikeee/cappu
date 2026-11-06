package build

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nikeee/cappu/internal/config"
)

// The compile and the runtime classpath share the configured entries and differ
// in the cappu-managed compile-only / runtime-only jar dirs.
func TestCompileAndRuntimeClassPathDiffer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.DefaultConfigName), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	jars := map[string]string{
		config.DefaultClassPath:       "shared.jar",
		config.DefaultCompileOnlyPath: "servlet.jar",
		config.DefaultRuntimeOnlyPath: "driver.jar",
	}
	for d, jar := range jars {
		if err := os.MkdirAll(cfg.ResolvePath(d), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfg.ResolvePath(d), jar), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	has := func(cp []string, jar string) bool {
		return slices.ContainsFunc(cp, func(p string) bool { return filepath.Base(p) == jar })
	}
	compile, runtime := CompileClassPath(cfg), RuntimeClassPath(cfg)
	if !has(compile, "shared.jar") || !has(compile, "servlet.jar") || has(compile, "driver.jar") {
		t.Errorf("compile classpath = %v", compile)
	}
	if !has(runtime, "shared.jar") || has(runtime, "servlet.jar") || !has(runtime, "driver.jar") {
		t.Errorf("runtime classpath = %v", runtime)
	}
	if shared := ClassPath(cfg); has(shared, "servlet.jar") || has(shared, "driver.jar") {
		t.Errorf("shared classpath = %v", shared)
	}
}
