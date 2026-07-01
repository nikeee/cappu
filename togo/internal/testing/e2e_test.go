package testing

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/config"
)

// End-to-end: with testOptions.outputFormat "junit", run the real JUnit Console
// Launcher over a compiled test and assert a valid junit-XML report file is
// emitted into the resolved reportsDir. Mirrors src/testing/testing.e2e.test.ts.
// Gated on javac; the launcher (which bundles the jupiter API+engine) is fetched
// from the configured package sources, so the test skips if that network step
// fails.
func TestJunitReportEmitted(t *testing.T) {
	if exec.Command("javac", "-version").Run() != nil {
		t.Skip("javac not on PATH")
	}
	t.Setenv("CAPPU_PACKAGE_STORE", filepath.Join(t.TempDir(), "store"))

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cappu.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.TestOptions.OutputFormat = "junit"
	cfg.TestOptions.ReportsDir = "./dist/test-results"

	// the one network step: skip if the launcher cannot be fetched (offline).
	launcher, err := ConsoleLauncherJar(cfg)
	if err != nil {
		t.Skipf("could not fetch console launcher: %v", err)
	}

	classes := TestClassesDir(cfg)
	if err := os.MkdirAll(classes, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "SampleTest.java")
	sample := `
import org.junit.jupiter.api.Test;
import static org.junit.jupiter.api.Assertions.assertEquals;

class SampleTest {
  @Test void addition() { assertEquals(2, 1 + 1); }
}
`
	if err := os.WriteFile(src, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("javac", "-cp", launcher, "-d", classes, src).CombinedOutput(); err != nil {
		t.Fatalf("javac failed: %v\n%s", err, out)
	}

	run := exec.Command(ResolveJava(cfg), TestRunArgs(cfg, launcher)...)
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("launcher run failed: %v\n%s", err, out)
	}

	reportsDir := cfg.ResolvePath("./dist/test-results")
	entries, err := os.ReadDir(reportsDir)
	if err != nil {
		t.Fatalf("reports dir not created: %v", err)
	}
	var xml string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".xml") {
			b, err := os.ReadFile(filepath.Join(reportsDir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			xml = string(b)
			break
		}
	}
	if xml == "" {
		t.Fatalf("no junit-XML report emitted in %s", reportsDir)
	}
	if !strings.HasPrefix(strings.TrimSpace(xml), "<?xml") ||
		!strings.Contains(xml, "<testsuite") ||
		!strings.Contains(xml, `tests="1"`) ||
		!strings.Contains(xml, `failures="0"`) ||
		!strings.Contains(xml, "addition") {
		t.Errorf("report is not a valid single-test junit-XML:\n%s", xml)
	}
}
