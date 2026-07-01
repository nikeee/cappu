package testing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/install"
)

func project(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cappu.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestFindTestSources(t *testing.T) {
	// A project with no src/test/java yields no test sources.
	if got := FindTestSources(project(t)); len(got) != 0 {
		t.Errorf("missing test dir should yield none, got %v", got)
	}

	cfg := project(t)
	src := filepath.Join(cfg.BaseDir, config.DefaultTestSourcePath, "com", "example")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "T.java"), []byte("class T {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := FindTestSources(cfg)
	if len(got) != 1 || !strings.HasSuffix(got[0], filepath.Join("com", "example", "T.java")) {
		t.Errorf("FindTestSources = %v", got)
	}
}

// CompileTests returns located diagnostics (not a flat error). When javac cannot
// run at all it collapses to one error diagnostic, matching compileTests in TS.
func TestCompileTestsJavacUnavailable(t *testing.T) {
	cfg := project(t)
	cfg.CompilerOptions.Javac = "cappu-no-such-javac"
	diags := CompileTests(cfg, []string{"X.java"})
	if len(diags) != 1 || diags[0].Severity != "error" || !strings.Contains(diags[0].Message, "compiling tests needs javac") {
		t.Errorf("diags = %+v", diags)
	}
}

func TestRunArgsStructure(t *testing.T) {
	cfg := project(t)
	args := TestRunArgs(cfg, "/path/launcher.jar", "")
	if args[0] != "-jar" || args[1] != "/path/launcher.jar" || args[2] != "execute" {
		t.Errorf("prefix = %v", args[:3])
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--class-path") {
		t.Errorf("missing class-path flag: %v", args)
	}
	// --scan-class-path must be the last argument (matches the TS run.at(-1)).
	if args[len(args)-1] != "--scan-class-path" {
		t.Errorf("last arg = %q, want --scan-class-path", args[len(args)-1])
	}
	// the compiled test classes must be the first runtime classpath entry
	if !strings.Contains(args[4], TestClassesDir(cfg)) {
		t.Errorf("test classes not on the runtime classpath: %q", args[4])
	}
	// default outputFormat is "text": no report is written
	if strings.Contains(strings.Join(args, " "), "--reports-dir") {
		t.Errorf("text format must not write reports: %v", args)
	}
}

func TestRunArgsJunitReports(t *testing.T) {
	cfg := project(t)
	cfg.TestOptions.OutputFormat = "junit"
	args := TestRunArgs(cfg, "/path/launcher.jar", "")
	i := indexOf(args, "--reports-dir")
	if i < 0 || args[i+1] != cfg.ResolvePath(config.DefaultTestReportsDir) {
		t.Errorf("--reports-dir = %v, want %q", args, cfg.ResolvePath(config.DefaultTestReportsDir))
	}
	// --scan-class-path stays last
	if args[len(args)-1] != "--scan-class-path" {
		t.Errorf("last arg = %q, want --scan-class-path", args[len(args)-1])
	}

	cfg.TestOptions.ReportsDir = "./build/reports"
	custom := TestRunArgs(cfg, "/path/launcher.jar", "")
	i = indexOf(custom, "--reports-dir")
	if i < 0 || custom[i+1] != cfg.ResolvePath("./build/reports") {
		t.Errorf("custom --reports-dir = %v", custom)
	}
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

func TestRuntimeClassPathOrder(t *testing.T) {
	cfg := project(t)
	cp := TestRuntimeClassPath(cfg)
	if cp[0] != TestClassesDir(cfg) {
		t.Errorf("runtime cp[0] = %q, want test-classes dir", cp[0])
	}
	if cp[1] != MainClassesDir(cfg) {
		t.Errorf("runtime cp[1] = %q, want main classes dir", cp[1])
	}
}

func TestRunArgsCoverageAgent(t *testing.T) {
	cfg := project(t)
	args := TestRunArgs(cfg, "/path/launcher.jar", "/store/jacocoagent.jar")
	execFile := filepath.Join(cfg.ResolvePath(cfg.TestOptions.ReportsDir), "jacoco.exec")
	want := "-javaagent:/store/jacocoagent.jar=destfile=" + execFile
	if args[0] != want {
		t.Errorf("args[0] = %q, want %q", args[0], want)
	}
	if args[1] != "-jar" || args[2] != "/path/launcher.jar" {
		t.Errorf("after agent: %v", args[1:3])
	}
	if args[len(args)-1] != "--scan-class-path" {
		t.Errorf("last arg = %q", args[len(args)-1])
	}
	if plain := TestRunArgs(cfg, "/path/launcher.jar", ""); plain[0] != "-jar" {
		t.Errorf("no agent should start at -jar, got %q", plain[0])
	}
}

func TestJacocoAgentCoordinates(t *testing.T) {
	if jacocoAgent.Classifier != "runtime" {
		t.Errorf("classifier = %q, want runtime", jacocoAgent.Classifier)
	}
	path, ok := install.StorePathFor(jacocoAgent)
	if !ok || !strings.HasSuffix(path, "-runtime.jar") {
		t.Errorf("store path = %q ok=%v", path, ok)
	}
}
