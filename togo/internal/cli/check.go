package cli

import (
	"fmt"
	"os"

	"github.com/nikeee/cappu/internal/build"
	"github.com/nikeee/cappu/internal/compile"
	"github.com/nikeee/cappu/internal/config"
)

// RunCheck handles `cappu check`: type-check the project with cappu's own
// checker (the LSP's diagnostics, #30) and report them - no class files are
// written. With no files, check everything under the configured sourcePaths.
// Port of src/cli/check.ts.
func RunCheck(files []string, cfg *config.Config) int {
	inputs := files
	if len(inputs) == 0 {
		inputs = build.SourceJavaFiles(cfg)
	}
	if len(inputs) == 0 {
		fmt.Fprint(os.Stderr, "usage: cappu check <file.java> ...\n"+
			"(no files given and the configured sourcePaths contain no .java files)\n")
		return 2
	}
	diagnostics := compile.RunCheck(inputs, cfg)
	renderDiagnostics(diagnostics)
	for _, d := range diagnostics {
		if d.Severity == "error" {
			return 1
		}
	}
	return 0
}
