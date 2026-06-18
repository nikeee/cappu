package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/nikeee/cappu/internal/build"
	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/testing"
)

// RunTest handles `cappu test`: build main + test classes, then stream a JUnit
// Platform Console Launcher run. Exits with the launcher's code. Port of
// src/cli/test.ts.
func RunTest(cfg *config.Config) int {
	errp := painter(os.Stderr)

	testSources := testing.FindTestSources(cfg)
	if len(testSources) == 0 {
		fmt.Fprintln(os.Stderr, "cappu: no tests found under ./src/test/java")
		return 1
	}

	// 1. main classes into .cappu/test-build/classes
	if mainSources := build.SourceJavaFiles(cfg); len(mainSources) > 0 {
		if err := build.Compile(cfg, mainSources, testing.MainClassesDir(cfg), build.ClassPath(cfg)); err != nil {
			fmt.Fprintf(os.Stderr, "%s %s\n", errp("red", "error:"), err)
			return 1
		}
	}

	// 2. test classes against main + lib + test-classes
	if err := testing.CompileTests(cfg, testSources); err != nil {
		fmt.Fprintf(os.Stderr, "%s %s\n", errp("red", "error:"), err)
		return 1
	}

	// 3. the JUnit run, streamed (the launcher's exit code is ours)
	launcher, err := testing.ConsoleLauncherJar(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
		return 1
	}
	cmd := exec.Command(testing.ResolveJava(cfg), testing.TestRunArgs(cfg, launcher)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "cappu: could not run java: %s\n", err)
		return 1
	}
	return 0
}
