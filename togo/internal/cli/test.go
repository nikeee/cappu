package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/nikeee/cappu/internal/build"
	"github.com/nikeee/cappu/internal/compile"
	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/testing"
)

// RunTest handles `cappu test`: build main + test classes, then stream a JUnit
// Platform Console Launcher run. Exits with the launcher's code. Port of
// src/cli/test.ts.
func RunTest(cfg *config.Config) int {
	testSources := testing.FindTestSources(cfg)
	if len(testSources) == 0 {
		fmt.Fprintln(os.Stderr, "cappu: no tests found under ./src/test/java")
		return 1
	}

	// 1. main classes (annotation processors and resources included), into the
	// derived .cappu/test-build/classes tree
	if mainSources := build.SourceJavaFiles(cfg); len(mainSources) > 0 {
		main := compile.RunCompile(mainSources, compile.Options{
			OutDir: testing.MainClassesDir(cfg),
			Output: "classes",
			Config: cfg,
		})
		for _, w := range main.Warnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", w)
			emitAnnotation("warning", w, AnnotationLocation{})
		}
		if !main.Success {
			renderDiagnostics(main.Diagnostics)
			return 1
		}
	}

	// 2. test classes against main + .cappu/lib/classes + .cappu/lib/test-classes
	if diagnostics := testing.CompileTests(cfg, testSources); len(diagnostics) > 0 {
		renderDiagnostics(diagnostics)
		for _, d := range diagnostics {
			if d.Severity == "error" {
				return 1
			}
		}
	}

	// 3. the JUnit run, streamed (the launcher's exit code is ours). With
	// coverage on, also fetch the JaCoCo agent and attach it (writes jacoco.exec
	// into reportsDir).
	launcher, err := testing.ConsoleLauncherJar(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
		return 1
	}
	agent := ""
	if cfg.TestOptions.Coverage {
		agent, err = testing.JacocoAgentJar(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
			return 1
		}
		// the JaCoCo agent opens destfile but does not create parent dirs
		if err := os.MkdirAll(cfg.ResolvePath(cfg.TestOptions.ReportsDir), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
			return 1
		}
	}
	cmd := exec.Command(testing.ResolveJava(cfg), testing.TestRunArgs(cfg, launcher, agent)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if code := exit.ExitCode(); code >= 0 {
				return code
			}
			return 1 // signal-killed: TS's `status ?? 1`, not Go's -1 (=255)
		}
		fmt.Fprintf(os.Stderr, "cappu: could not run java: %s\n", err)
		return 1
	}
	return 0
}
