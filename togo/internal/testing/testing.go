// Package testing implements `cappu test` (nikeee/cappu#16): compile
// src/test/java against the main classes plus the lib/test-classes, then run the
// JUnit Console Launcher over the result. Print-free; the CLI streams the JUnit
// run. Port of src/testing/.
package testing

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/nikeee/cappu/internal/build"
	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/install"
	"github.com/nikeee/cappu/internal/javacdiag"
	"github.com/nikeee/cappu/internal/jdks"
	"github.com/nikeee/cappu/internal/packages"
	"github.com/nikeee/cappu/internal/sources"
)

// testBuildRoot is derived build state under .cappu (gitignored via /.cappu/).
const testBuildRoot = ".cappu/test-build"

// MainClassesDir is where the project's main classes compile to for a test run.
func MainClassesDir(cfg *config.Config) string {
	return cfg.ResolvePath(filepath.Join(testBuildRoot, "classes"))
}

// TestClassesDir is where the compiled test classes go.
func TestClassesDir(cfg *config.Config) string {
	return cfg.ResolvePath(filepath.Join(testBuildRoot, "test-classes"))
}

// FindTestSources lists all .java files under src/test/java.
func FindTestSources(cfg *config.Config) []string {
	return build.JavaFilesIn(cfg.ResolvePath(config.DefaultTestSourcePath))
}

// dependencyClassPath is what test sources compile against: the main classes,
// the compile deps and the test deps (jar-expanded).
func dependencyClassPath(cfg *config.Config) []string {
	cp := []string{MainClassesDir(cfg)}
	cp = append(cp, build.ClassPath(cfg)...)
	cp = append(cp, build.ExpandJarDirs([]string{cfg.ResolvePath(config.DefaultTestClassPath)})...)
	return cp
}

// TestRuntimeClassPath is the classpath the JUnit launcher runs with.
func TestRuntimeClassPath(cfg *config.Config) []string {
	cp := []string{TestClassesDir(cfg)}
	cp = append(cp, dependencyClassPath(cfg)...)
	testResources := cfg.ResolvePath(config.DefaultTestResourcePath)
	if _, err := os.Stat(testResources); err == nil {
		cp = append(cp, testResources)
	}
	return cp
}

// compileTestsArgs builds the javac arguments for the test compile. Port of
// compileTestsArgs.
func compileTestsArgs(cfg *config.Config, sources []string) []string {
	args := []string{"-d", TestClassesDir(cfg), "-encoding", "UTF-8"}
	if cfg.CompilerOptions.Release != nil {
		args = append(args, "--release", strconv.Itoa(*cfg.CompilerOptions.Release))
	}
	args = append(args, "-cp", strings.Join(dependencyClassPath(cfg), string(os.PathListSeparator)))
	return append(args, sources...)
}

// CompileTests compiles src/test/java; the returned diagnostics are non-empty on
// failure (empty on success). The test-classes dir is wiped first so a
// since-deleted test cannot still be discovered by --scan-class-path. Port of
// compileTests.
func CompileTests(cfg *config.Config, testSources []string) []javacdiag.CompileDiagnostic {
	dir := TestClassesDir(cfg)
	_ = os.RemoveAll(dir)
	_ = os.MkdirAll(dir, 0o755)
	javac := build.Javac(cfg)
	cmd := exec.Command(javac, compileTestsArgs(cfg, testSources)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return nil
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return []javacdiag.CompileDiagnostic{{Severity: "error",
			Message: fmt.Sprintf("compiling tests needs javac: '%s' could not run (%s)", javac, err)}}
	}
	diagnostics := javacdiag.ParseJavacDiagnostics(stderr.String())
	if len(diagnostics) == 0 {
		s := strings.TrimSpace(stderr.String())
		if len(s) > 400 {
			s = s[len(s)-400:]
		}
		diagnostics = []javacdiag.CompileDiagnostic{{Severity: "error", Message: "test compilation failed: " + s}}
	}
	return diagnostics
}

// consoleLauncher is the pinned JUnit Platform Console Launcher: a TOOL, never a
// project dependency, so it never appears in cappu.json or the lockfile.
var consoleLauncher = packages.NewCoordinates("org.junit.platform", "junit-platform-console-standalone", "1.12.2")

// ConsoleLauncherJar returns the launcher jar's path in the global store,
// downloading it there on first use.
func ConsoleLauncherJar(cfg *config.Config) (string, error) {
	path, ok := install.StorePathFor(consoleLauncher)
	if !ok {
		return "", fmt.Errorf("unreachable: launcher coordinates are store-safe")
	}
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	for _, source := range sources.Configured(cfg) {
		bytes, err := source.GetArtifact(consoleLauncher)
		if err != nil {
			return "", err
		}
		if bytes == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, bytes, 0o644); err != nil {
			return "", err
		}
		return path, nil
	}
	return "", fmt.Errorf("could not download %s from any package source", consoleLauncher.String())
}

// TestRunArgs are the java arguments running the launcher over the tests.
func TestRunArgs(cfg *config.Config, launcherJar string) []string {
	return []string{
		"-jar", launcherJar, "execute",
		"--class-path", strings.Join(TestRuntimeClassPath(cfg), string(os.PathListSeparator)),
		"--scan-class-path",
	}
}

// ResolveJava is the java launcher tests run under: the provisioned JDK's, else
// the sibling of the resolved javac (so a PATH skew between javac and java
// cannot cause UnsupportedClassVersionError), else plain "java". A bare javac
// name is looked up on PATH and symlink-resolved first, matching the TS
// resolveJava, so e.g. a /usr/bin/javac that points at JDK 25 picks that JDK's
// java rather than a different default `java` on PATH.
func ResolveJava(cfg *config.Config) string {
	if java := jdks.ProvisionedJava(cfg); java != "" {
		return java
	}
	javac := build.Javac(cfg)
	path := javac
	if !strings.ContainsAny(javac, `/\`) {
		if p, err := exec.LookPath(javac); err == nil {
			path = p
		}
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	name := "java"
	if runtime.GOOS == "windows" {
		name = "java.exe"
	}
	sibling := filepath.Join(filepath.Dir(path), name)
	if _, err := os.Stat(sibling); err == nil {
		return sibling
	}
	return "java"
}
