package dapserver

// Resolving and building the program cappu launches under the debugger. v1
// debugs a configured (or launch-supplied) main class. Sources are compiled
// with javac -g into a dedicated debug-build tree so the bytecode carries the
// LocalVariableTable that variable inspection needs. Port of
// src/services/dap/debuggee.ts.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/nikeee/cappu/internal/build"
	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/dap"
	"github.com/nikeee/cappu/internal/javacdiag"
	"github.com/nikeee/cappu/internal/jdks"
)

const debugBuildClasses = ".cappu/debug-build/classes"

func DebugClassesDir(cfg *config.Config) string { return cfg.ResolvePath(debugBuildClasses) }

// CompileForDebug compiles src/main/java with javac -g; non-empty on failure.
func CompileForDebug(cfg *config.Config) []javacdiag.CompileDiagnostic {
	sources := build.SourceJavaFiles(cfg)
	if len(sources) == 0 {
		return []javacdiag.CompileDiagnostic{{Severity: "error", Message: "no sources under src/main/java to debug"}}
	}
	dir := DebugClassesDir(cfg)
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return []javacdiag.CompileDiagnostic{{Severity: "error", Message: err.Error()}}
	}
	javac := jdks.ProvisionedJavac(cfg)
	if javac == "" {
		javac = cfg.CompilerOptions.Javac
	}
	cp := build.ClassPath(cfg)
	args := []string{"-g", "-d", dir, "-encoding", "UTF-8"}
	if cfg.CompilerOptions.Release != nil {
		args = append(args, "--release", strconv.Itoa(*cfg.CompilerOptions.Release))
	}
	if len(cp) > 0 {
		args = append(args, "-cp", strings.Join(cp, string(os.PathListSeparator)))
	}
	args = append(args, sources...)
	cmd := exec.Command(javac, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		diagnostics := javacdiag.ParseJavacDiagnostics(stderr.String())
		if len(diagnostics) == 0 {
			diagnostics = []javacdiag.CompileDiagnostic{{Severity: "error", Message: fmt.Sprintf("javac failed: %s", err)}}
		}
		return diagnostics
	}
	return nil
}

// DebuggeeClassPath is the runtime classpath: debug classes + dependency jars.
func DebuggeeClassPath(cfg *config.Config, extra []string) string {
	cp := append([]string{DebugClassesDir(cfg)}, build.ClassPath(cfg)...)
	cp = append(cp, extra...)
	return strings.Join(cp, string(os.PathListSeparator))
}

func ResolveMainClass(cfg *config.Config, args dap.LaunchArguments) (string, error) {
	mc := args.MainClass
	if mc == "" {
		mc = cfg.CompilerOptions.MainClass
	}
	if mc == "" {
		return "", errors.New("no main class: set compilerOptions.mainClass in cappu.json or pass mainClass in the launch request")
	}
	return mc, nil
}
