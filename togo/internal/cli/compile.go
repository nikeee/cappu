package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nikeee/cappu/internal/build"
	"github.com/nikeee/cappu/internal/compile"
	"github.com/nikeee/cappu/internal/compiler"
	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/publish"
)

// RunCompile handles `cappu compile`: run the print-free compile pipeline and
// render its result. With no files, this is a project build over the configured
// sourcePaths. Port of src/cli/compile.ts.
func RunCompile(files []string, outputFlag, artifact string, quiet bool, cfg *config.Config) int {
	inputs := files
	if len(inputs) == 0 {
		inputs = build.SourceJavaFiles(cfg)
	}
	if len(inputs) == 0 {
		fmt.Fprint(os.Stderr, "usage: cappu compile [-d <outdir>] <file.java> ...\n"+
			"(no files given and the configured sourcePaths contain no .java files)\n")
		return 2
	}
	for _, p := range compile.MissingConfiguredPaths(cfg) {
		fmt.Fprintf(os.Stderr, "warning: configured path not found (treated as empty): %s\n", p)
		emitAnnotation("warning", fmt.Sprintf("configured path not found (treated as empty): %s", p), AnnotationLocation{})
	}
	validKinds := map[string]bool{"classes": true, "jar": true, "fat-jar": true}
	if outputFlag != "" && !validKinds[outputFlag] {
		fmt.Fprintf(os.Stderr, "cappu: invalid --output '%s' (classes, jar, fat-jar)\n", outputFlag)
		emitAnnotation("error", fmt.Sprintf("invalid --output '%s' (classes, jar, fat-jar)", outputFlag), AnnotationLocation{})
		return 2
	}
	effectiveOutput := outputFlag
	if effectiveOutput == "" {
		effectiveOutput = cfg.CompilerOptions.Output
	}
	experimental := cfg.CompilerOptions.ExperimentalCompiler.Enabled
	validate := experimental && cfg.CompilerOptions.ExperimentalCompiler.Validate
	if validate && effectiveOutput != "classes" {
		fmt.Fprint(os.Stderr, "cappu: experimentalCompiler.validate needs \"output\": \"classes\" (javap reads class files)\n")
		emitAnnotation("error", "experimentalCompiler.validate needs \"output\": \"classes\" (javap reads class files)", AnnotationLocation{})
		return 2
	}

	result := compile.RunCompile(inputs, compile.Options{
		Output:       outputFlag,
		ArtifactName: strings.TrimSuffix(artifact, ".jar"),
		Config:       cfg,
	})
	if !quiet {
		for _, out := range result.Written {
			fmt.Fprintln(os.Stdout, out)
		}
	}
	for _, entry := range result.Degraded {
		fmt.Fprintf(os.Stderr, "warning: %s: unsupported construct, emitted a placeholder body\n", entry)
		emitAnnotation("warning", fmt.Sprintf("%s: unsupported construct, emitted a placeholder body", entry), AnnotationLocation{})
	}
	for _, w := range result.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
		emitAnnotation("warning", w, AnnotationLocation{})
	}
	if !result.Success {
		renderDiagnostics(result.Diagnostics)
		return 1
	}

	// A plain jar with full Maven coordinates is publishable: emit its POM beside it.
	if effectiveOutput == "jar" && len(publish.MissingCoordinates(cfg)) == 0 {
		for _, f := range result.Written {
			if strings.HasSuffix(f, ".jar") {
				pomPath := strings.TrimSuffix(f, ".jar") + ".pom"
				if pom, err := publish.GeneratePom(cfg); err == nil {
					if os.WriteFile(pomPath, []byte(pom), 0o644) == nil && !quiet {
						fmt.Fprintln(os.Stdout, pomPath)
					}
				}
				break
			}
		}
	}
	if validate {
		// `inputs`, not `files`: a project build validates the sourcePaths sources.
		v := compiler.ValidateAgainstJavac(inputs, result.Written, build.Javac(cfg))
		if !v.OK {
			if v.Error != "" {
				fmt.Fprintf(os.Stderr, "cappu: --validate: %s\n", v.Error)
				emitAnnotation("error", fmt.Sprintf("--validate: %s", v.Error), AnnotationLocation{})
			} else {
				for _, m := range v.Mismatches {
					fmt.Fprintf(os.Stderr, "error: %s: bytecode differs from javac: %s\n", m.ClassName, m.Detail)
					emitAnnotation("error", fmt.Sprintf("%s: bytecode differs from javac: %s", m.ClassName, m.Detail), AnnotationLocation{})
				}
			}
			return 1
		}
		if !quiet {
			fmt.Fprintf(os.Stderr, "--validate: %d class(es) match javac\n", v.Compared)
		}
	}

	// DX: after building a runnable application jar, show how to start it. Only
	// for applications - a library jar has no Main-Class, so result.MainClass is
	// empty and nothing is printed.
	if !quiet && (effectiveOutput == "jar" || effectiveOutput == "fat-jar") && result.MainClass != "" {
		for _, f := range result.Written {
			if strings.HasSuffix(f, ".jar") {
				rel := f
				if cwd, err := os.Getwd(); err == nil {
					if r, err := filepath.Rel(cwd, f); err == nil {
						rel = r
					}
				}
				fmt.Fprintf(os.Stdout, "\nRun it with:\n  java -jar %s\n", rel)
				break
			}
		}
	}
	return 0
}

// renderDiagnostics prints compile diagnostics to stderr as
// `file:line:col code: severity: message`.
func renderDiagnostics(diagnostics []compile.CompileDiagnostic) {
	for _, d := range diagnostics {
		location := ""
		if d.File != "" {
			location = d.File + ":" + strconv.Itoa(d.Line)
			if d.Column != 0 {
				location += ":" + strconv.Itoa(d.Column)
			}
			location += ": "
		}
		code := ""
		if d.Code != 0 {
			code = " " + strconv.Itoa(d.Code)
		}
		fmt.Fprintf(os.Stderr, "%s%s%s: %s\n", location, d.Severity, code, d.Message)
		emitAnnotation(d.Severity, d.Message, AnnotationLocation{File: d.File, Line: d.Line, Column: d.Column})
	}
}
