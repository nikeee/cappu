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

	run := exec.Command(ResolveJava(cfg), TestRunArgs(cfg, launcher, "")...)
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

// End-to-end: with testOptions.coverage, run the compiled test under the JaCoCo
// agent and assert a valid jacoco.exec lands in reportsDir. Mirrors the coverage
// e2e in src/testing/testing.e2e.test.ts. Gated on javac; the launcher and agent
// are fetched from package sources, so it skips if either network step fails.
func TestCoverageExecEmitted(t *testing.T) {
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
	cfg.TestOptions.Coverage = true
	cfg.TestOptions.ReportsDir = "./dist/test-results"

	// two network steps: the launcher and the JaCoCo agent. Skip if either fails.
	launcher, err := ConsoleLauncherJar(cfg)
	if err != nil {
		t.Skipf("could not fetch console launcher: %v", err)
	}
	agent, err := JacocoAgentJar(cfg)
	if err != nil {
		t.Skipf("could not fetch JaCoCo agent: %v", err)
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

	// the CLI creates reportsDir before running; mirror that here
	reportsDir := cfg.ResolvePath("./dist/test-results")
	if err := os.MkdirAll(reportsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := exec.Command(ResolveJava(cfg), TestRunArgs(cfg, launcher, agent)...)
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("launcher run failed: %v\n%s", err, out)
	}

	// jacoco.exec exists and begins with JaCoCo's header: block id 0x01 then the
	// 0xC0 0xC0 magic number
	b, err := os.ReadFile(filepath.Join(reportsDir, "jacoco.exec"))
	if err != nil {
		t.Fatalf("jacoco.exec not emitted: %v", err)
	}
	if len(b) < 3 || b[0] != 0x01 || b[1] != 0xc0 || b[2] != 0xc0 {
		t.Errorf("jacoco.exec header = % x, want 01 c0 c0", b[:min(3, len(b))])
	}
}
