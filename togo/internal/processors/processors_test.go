package processors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/compiler"
	"github.com/nikeee/cappu/internal/config"
)

func loadCfg(t *testing.T, dir string) *config.Config {
	t.Helper()
	cfg, err := config.Load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func jarWithServices(t *testing.T, dir, name, services string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	z := compiler.WriteZip([]compiler.ZipEntryInput{
		{Name: "META-INF/services/javax.annotation.processing.Processor", Bytes: []byte(services)},
	})
	if err := os.WriteFile(path, z, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDiscoverProcessors(t *testing.T) {
	dir := t.TempDir()
	a := jarWithServices(t, dir, "a.jar", "# comment\ncom.example.AProcessor\n\ncom.example.BProcessor # trailing\n")
	plain := filepath.Join(dir, "plain.jar")
	if err := os.WriteFile(plain, compiler.WriteZip([]compiler.ZipEntryInput{{Name: "com/example/X.class", Bytes: []byte("x")}}), 0o644); err != nil {
		t.Fatal(err)
	}
	corrupt := filepath.Join(dir, "corrupt.jar")
	if err := os.WriteFile(corrupt, []byte("not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := DiscoverProcessors([]string{a, plain, corrupt})
	if !eq(got, []string{"com.example.AProcessor", "com.example.BProcessor"}) {
		t.Errorf("discovered = %v", got)
	}
}

func TestProcessorJarsSorted(t *testing.T) {
	project := t.TempDir()
	cfg := loadCfg(t, project)
	if got := ProcessorJars(cfg); len(got) != 0 {
		t.Errorf("absent processors dir should be empty, got %v", got)
	}
	procDir := filepath.Join(project, ".cappu", "lib", "processors")
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"b.jar", "a.jar", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(procDir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := ProcessorJars(cfg)
	want := []string{filepath.Join(procDir, "a.jar"), filepath.Join(procDir, "b.jar")}
	if !eq(got, want) {
		t.Errorf("jars = %v, want %v (sorted, .jar only)", got, want)
	}
}

func TestProcOnlyArgs(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".cappu", "lib", "classes"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := loadCfg(t, project)
	sep := string(os.PathListSeparator)
	args := ProcOnlyArgs(cfg, []string{"/p/A.java"}, []string{"/p/proc.jar", "/p/extra.jar"}, "/out/sources", "/out/classes")
	want := []string{
		"-proc:only",
		"-processorpath", "/p/proc.jar" + sep + "/p/extra.jar",
		"-s", "/out/sources",
		"-d", "/out/classes",
		"-encoding", "UTF-8",
		"-cp", filepath.Join(project, ".cappu", "lib", "classes"),
		// no -sourcepath: ./src/main/java does not exist in this project
		"/p/A.java",
	}
	if !eq(args, want) {
		t.Errorf("args =\n  %v\nwant\n  %v", args, want)
	}
}

func TestNoJarsNothingRuns(t *testing.T) {
	project := t.TempDir()
	cfg := loadCfg(t, project)
	r := RunAnnotationProcessing(cfg, []string{"/p/A.java"}, func(string, []string) ExecResult {
		t.Fatal("exec must not be called when no processor jars are installed")
		return ExecResult{}
	})
	if r.Ran || len(r.GeneratedFiles) != 0 || len(r.Diagnostics) != 0 {
		t.Errorf("result = %+v, want {Ran:false}", r)
	}
}

func statusResult(code int, stderr string) ExecResult {
	return ExecResult{Status: &code, Stderr: stderr}
}

func TestFailureModesAndSuccess(t *testing.T) {
	project := t.TempDir()
	procDir := filepath.Join(project, ".cappu", "lib", "processors")
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jarWithServices(t, procDir, "p.jar", "com.example.P\n")
	cfg := loadCfg(t, project)

	// located error from a failed run
	failed := RunAnnotationProcessing(cfg, []string{"/p/A.java"}, func(string, []string) ExecResult {
		return statusResult(1, "/p/A.java:3: error: cannot find symbol\n  symbol: class Missing\n1 error\n")
	})
	if len(failed.Diagnostics) != 1 || failed.Diagnostics[0].File != "/p/A.java" || failed.Diagnostics[0].Line != 3 || failed.Diagnostics[0].Message != "cannot find symbol" {
		t.Errorf("located failure = %+v", failed.Diagnostics)
	}

	// an uncaught processor exception has no located line: collapses to one error
	threw := RunAnnotationProcessing(cfg, []string{"/p/A.java"}, func(string, []string) ExecResult {
		return statusResult(3, "error: An annotation processor threw an uncaught exception.\n\tat com.example.P.process(P.java:10)\n")
	})
	if len(threw.Diagnostics) != 1 || threw.Diagnostics[0].Severity != "error" {
		t.Errorf("uncaught-exception = %+v", threw.Diagnostics)
	}

	// spawn failure (ENOENT-style)
	missing := RunAnnotationProcessing(cfg, []string{"/p/A.java"}, func(string, []string) ExecResult {
		return ExecResult{Status: nil, Err: os.ErrNotExist}
	})
	if len(missing.Diagnostics) == 0 || !strings.Contains(missing.Diagnostics[0].Message, "needs javac") || !strings.Contains(missing.Diagnostics[0].Message, `configure "jdk"`) {
		t.Errorf("spawn failure = %+v", missing.Diagnostics)
	}

	// success: Note: lines do not become errors; located warnings survive
	ok := RunAnnotationProcessing(cfg, []string{"/p/A.java"}, func(string, []string) ExecResult {
		return statusResult(0, "Note: com.example.P did things\n/p/A.java:1: warning: something odd\n")
	})
	if !ok.Ran || len(ok.Diagnostics) != 1 || ok.Diagnostics[0].Severity != "warning" || ok.Diagnostics[0].File != "/p/A.java" || ok.Diagnostics[0].Message != "something odd" {
		t.Errorf("success diagnostics = %+v", ok.Diagnostics)
	}
	if _, err := os.Stat(GeneratedSourcesDir(cfg)); err != nil {
		t.Errorf("generated sources dir should exist after a successful run: %v", err)
	}
}
