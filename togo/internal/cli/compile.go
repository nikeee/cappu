package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/nikeee/cappu/internal/build"
	"github.com/nikeee/cappu/internal/compile"
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
	}
	validKinds := map[string]bool{"classes": true, "jar": true, "fat-jar": true}
	if outputFlag != "" && !validKinds[outputFlag] {
		fmt.Fprintf(os.Stderr, "cappu: invalid --output '%s' (classes, jar, fat-jar)\n", outputFlag)
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
	}
	for _, w := range result.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
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
		fmt.Fprintln(os.Stderr, "warning: experimentalCompiler.validate is not yet supported in the Go build; skipped")
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
	}
}
