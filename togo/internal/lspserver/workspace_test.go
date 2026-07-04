package lspserver

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/nikeee/cappu/internal/compiler"
	"github.com/nikeee/cappu/internal/config"
)

// Port of src/workspace.test.ts (the findJavaFiles helper).

func TestFindJavaFilesMissingDir(t *testing.T) {
	if files := findJavaFiles(FsPath("/definitely/not/here")); len(files) != 0 {
		t.Errorf("missing dir should yield no files, got %v", files)
	}
}

func TestFindJavaFilesSkipsBuildDirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(p string) {
		if err := os.WriteFile(p, []byte("class X {}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "A.java"))
	write(filepath.Join(dir, "src", "B.java"))
	write(filepath.Join(dir, "node_modules", "x", "C.java"))

	got := findJavaFiles(FsPath(dir))
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	want := []FsPath{FsPath(filepath.Join(dir, "A.java")), FsPath(filepath.Join(dir, "src", "B.java"))}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("findJavaFiles = %v, want %v", got, want)
	}
}

func TestLoadConfiguredSourcesLoadsClassPath(t *testing.T) {
	jar, err := filepath.Abs(filepath.Join("..", "compiler", "testdata", "classfiles", "util.jar"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{BaseDir: t.TempDir()}
	cfg.CompilerOptions.ClassPath = []string{jar}
	cfg.CompilerOptions.SourcePaths = []string{}
	program := compiler.NewProgram()
	compiler.InstallJdkTypes(program, cfg)
	loadConfiguredSources(program, cfg)
	if program.GetGlobalIndex().GetType("lib.Util") == nil {
		t.Error("lib.Util should resolve after loading the configured classPath")
	}
}
