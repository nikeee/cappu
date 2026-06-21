// Package processors implements JSR-269 annotation processing (nikeee/cappu#7).
// Processors are arbitrary JVM bytecode, so cappu never executes them itself:
// generation is delegated to a real javac. The default compile (which IS javac)
// just adds -processorpath/-s to its single invocation; the experimental
// compiler runs a separate `-proc:only` generation pass first and then compiles
// original + generated sources itself. Port of src/processors/processors.ts.
// Nothing here prints; exec is injectable for tests.
package processors

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nikeee/cappu/internal/build"
	"github.com/nikeee/cappu/internal/compiler"
	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/javacdiag"
)

// generatedRoot is the derived output tree under .cappu/ (gitignored).
const generatedRootRel = ".cappu/generated-sources"

func GeneratedRoot(cfg *config.Config) string {
	return cfg.ResolvePath(generatedRootRel)
}

// GeneratedSourcesDir is the generated .java tree (an implicit extra source path).
func GeneratedSourcesDir(cfg *config.Config) string {
	return filepath.Join(GeneratedRoot(cfg), "sources")
}

// GeneratedClassesDir is the Filer CLASS_OUTPUT (merged into the build like resources).
func GeneratedClassesDir(cfg *config.Config) string {
	return filepath.Join(GeneratedRoot(cfg), "classes")
}

// ProcessorJars are the processor jars under .cappu/lib/processors, sorted.
func ProcessorJars(cfg *config.Config) []string {
	dir := cfg.ResolvePath(config.DefaultProcessorPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var jars []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jar") {
			jars = append(jars, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(jars)
	return jars
}

// DiscoverProcessors returns the processor implementation classes a set of jars
// declares via META-INF/services/javax.annotation.processing.Processor.
func DiscoverProcessors(jarPaths []string) []string {
	var processors []string
	for _, path := range jarPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		entries := compiler.ReadZipEntries(data)
		if entries == nil {
			continue
		}
		for _, e := range entries {
			if e.Name != "META-INF/services/javax.annotation.processing.Processor" {
				continue
			}
			for _, line := range strings.Split(string(e.Read()), "\n") {
				name := strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
				if name != "" {
					processors = append(processors, name)
				}
			}
		}
	}
	return processors
}

// ExecResult is the outcome of a javac invocation. Status nil means the process
// could not be run at all (a spawn failure).
type ExecResult struct {
	Status *int
	Stderr string
	Err    error
}

// Exec runs a command and returns its result; injectable for tests.
type Exec func(name string, args []string) ExecResult

// DefaultExec runs name with args, capturing stderr.
func DefaultExec(name string, args []string) ExecResult {
	cmd := exec.Command(name, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		zero := 0
		return ExecResult{Status: &zero, Stderr: stderr.String()}
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code := ee.ExitCode()
		return ExecResult{Status: &code, Stderr: stderr.String()}
	}
	return ExecResult{Status: nil, Stderr: stderr.String(), Err: err} // spawn failure
}

// ProcOnlyArgs builds the -proc:only generation arguments.
func ProcOnlyArgs(cfg *config.Config, files, jars []string, outSources, outClasses string) []string {
	classPath := build.ClassPath(cfg)
	var sourcePaths []string
	for _, p := range cfg.CompilerOptions.SourcePaths {
		resolved := cfg.ResolvePath(p)
		if _, err := os.Stat(resolved); err == nil {
			sourcePaths = append(sourcePaths, resolved)
		}
	}
	args := []string{
		"-proc:only",
		"-processorpath", strings.Join(jars, string(os.PathListSeparator)),
		"-s", outSources,
		"-d", outClasses,
		"-encoding", "UTF-8",
	}
	if cfg.CompilerOptions.Release != nil {
		args = append(args, "--release", itoa(*cfg.CompilerOptions.Release))
	}
	if len(classPath) > 0 {
		args = append(args, "-cp", strings.Join(classPath, string(os.PathListSeparator)))
	}
	if len(sourcePaths) > 0 {
		args = append(args, "-sourcepath", strings.Join(sourcePaths, string(os.PathListSeparator)))
	}
	args = append(args, files...)
	return args
}

// ProcessingResult is the outcome of runAnnotationProcessing.
type ProcessingResult struct {
	Ran            bool
	GeneratedFiles []string
	Diagnostics    []javacdiag.CompileDiagnostic
}

// RunAnnotationProcessing runs the -proc:only generation pass. The new generation
// replaces .cappu/generated-sources only when javac exits 0. Ran=false (no exec
// at all) when no processor jars are installed.
func RunAnnotationProcessing(cfg *config.Config, files []string, ex Exec) ProcessingResult {
	if ex == nil {
		ex = DefaultExec
	}
	jars := ProcessorJars(cfg)
	if len(jars) == 0 {
		return ProcessingResult{Ran: false}
	}
	javac := build.Javac(cfg)
	target := GeneratedRoot(cfg)
	_ = os.MkdirAll(filepath.Dir(target), 0o755)
	stage, err := os.MkdirTemp(filepath.Dir(target), filepath.Base(target)+".next-")
	if err != nil {
		return ProcessingResult{Ran: true, Diagnostics: []javacdiag.CompileDiagnostic{{Severity: "error", Message: err.Error()}}}
	}
	defer func() { _ = os.RemoveAll(stage) }()

	outSources := filepath.Join(stage, "sources")
	outClasses := filepath.Join(stage, "classes")
	_ = os.MkdirAll(outSources, 0o755)
	_ = os.MkdirAll(outClasses, 0o755)

	result := ex(javac, ProcOnlyArgs(cfg, files, jars, outSources, outClasses))
	if result.Err != nil || result.Status == nil {
		msg := "unknown error"
		if result.Err != nil {
			msg = result.Err.Error()
		}
		return ProcessingResult{Ran: true, Diagnostics: []javacdiag.CompileDiagnostic{{Severity: "error",
			Message: "annotation processing needs javac: '" + javac + "' could not run (" + msg +
				"); set compilerOptions.javac or configure \"jdk\""}}}
	}
	if *result.Status != 0 {
		diags := javacdiag.ParseJavacDiagnostics(result.Stderr)
		if len(diags) == 0 {
			detail := strings.TrimSpace(result.Stderr)
			if len(detail) > 400 {
				detail = detail[len(detail)-400:]
			}
			if detail == "" {
				detail = javac + " exited " + itoa(*result.Status)
			}
			diags = []javacdiag.CompileDiagnostic{{Severity: "error", Message: "annotation processing failed: " + detail}}
		}
		return ProcessingResult{Ran: true, Diagnostics: diags}
	}
	// Success: only LOCATED warnings survive (Messager "Note: ..." lines would
	// otherwise collapse into a bogus unlocated error).
	var warnings []javacdiag.CompileDiagnostic
	for _, d := range javacdiag.ParseJavacDiagnostics(result.Stderr) {
		if d.Severity == "warning" && d.File != "" {
			warnings = append(warnings, d)
		}
	}
	_ = os.RemoveAll(target)
	if err := os.Rename(stage, target); err != nil {
		return ProcessingResult{Ran: true, Diagnostics: []javacdiag.CompileDiagnostic{{Severity: "error", Message: err.Error()}}}
	}
	var generatedFiles []string
	_ = filepath.WalkDir(filepath.Join(target, "sources"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".java") {
			generatedFiles = append(generatedFiles, path)
		}
		return nil
	})
	return ProcessingResult{Ran: true, GeneratedFiles: generatedFiles, Diagnostics: warnings}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
