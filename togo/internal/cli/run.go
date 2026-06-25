package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nikeee/cappu/internal/build"
	"github.com/nikeee/cappu/internal/compile"
	"github.com/nikeee/cappu/internal/compiler"
	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/testing"
)

// A private build tree (gitignored under /.cappu/), like .cappu/test-build.
const runClassesDir = ".cappu/run-build/classes"

// SelectMainClass picks the class to run: the configured mainClass wins;
// otherwise the single detected entry point. A 0/ambiguous result returns an
// empty mainClass and a reason. Port of selectMainClass.
func SelectMainClass(detected []string, configured string) (mainClass, reason string) {
	if configured != "" {
		return configured, ""
	}
	switch len(detected) {
	case 1:
		return detected[0], ""
	case 0:
		return "", "no class declares a main(String[]) method; set compilerOptions.mainClass"
	default:
		return "", fmt.Sprintf(
			"several classes declare main(String[]) (%s); set compilerOptions.mainClass to pick one",
			strings.Join(detected, ", "))
	}
}

// detectMainClasses is the fully qualified names of the compiled .class files
// that declare a main method (com/app/Main.class -> com.app.Main).
func detectMainClasses(written []string, outDir string) []string {
	var mains []string
	for _, f := range written {
		if !strings.HasSuffix(f, ".class") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if !compiler.ClassDeclaresMain(b) {
			continue
		}
		rel, err := filepath.Rel(outDir, f)
		if err != nil {
			continue
		}
		fqn := strings.ReplaceAll(strings.TrimSuffix(rel, ".class"), string(filepath.Separator), ".")
		mains = append(mains, fqn)
	}
	return mains
}

// RunRun handles `cappu run`: build the project to a class tree and run it on
// the JVM, the way `cargo run` / `uv run` end the happy path inside the tool
// instead of dropping the user to a raw `java -jar dist/<name>.jar`. Compiles to
// a private .cappu/run-build/classes tree (no jar packaging), assembles the
// runtime classpath from it plus the configured dependency classPath, and execs
// the same java the test runner resolves. Port of src/cli/run.ts.
func RunRun(args []string, cfg *config.Config) int {
	sources := build.SourceJavaFiles(cfg)
	if len(sources) == 0 {
		fmt.Fprintln(os.Stderr, "cappu: no .java files under the configured sourcePaths")
		return 2
	}

	outDir := cfg.ResolvePath(runClassesDir)
	result := compile.RunCompile(sources, compile.Options{
		OutDir: outDir,
		Output: "classes",
		Config: cfg,
	})
	for _, w := range result.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
		emitAnnotation("warning", w, AnnotationLocation{})
	}
	if !result.Success {
		renderDiagnostics(result.Diagnostics)
		return 1
	}
	renderDiagnostics(result.Diagnostics)

	mainClass, reason := SelectMainClass(detectMainClasses(result.Written, outDir), cfg.CompilerOptions.MainClass)
	if reason != "" {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", reason)
		emitAnnotation("error", reason, AnnotationLocation{})
		return 2
	}

	classPath := append([]string{outDir}, build.ClassPath(cfg)...)
	cmdArgs := append([]string{"-cp", strings.Join(classPath, string(os.PathListSeparator)), mainClass}, args...)
	cmd := exec.Command(testing.ResolveJava(cfg), cmdArgs...)
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
