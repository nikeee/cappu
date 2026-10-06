package cli

import (
	_ "embed"
	"fmt"
	"os"
)

// The scripts are checked in and embedded, edited by hand alongside the
// commands; TestCompletionScriptsListEveryCommand fails when one of them
// stops offering a command the CLI has.
var (
	//go:embed completion/cappu.bash
	bashCompletion string
	//go:embed completion/cappu.zsh
	zshCompletion string
)

// RunCompletion prints the shell completion script for bash or zsh to stdout.
// Port of src/cli/completion.ts.
func RunCompletion(shell string) int {
	switch shell {
	case "bash":
		fmt.Fprint(os.Stdout, bashCompletion)
		return 0
	case "zsh":
		fmt.Fprint(os.Stdout, zshCompletion)
		return 0
	case "":
		fmt.Fprint(os.Stderr, "cappu: completion needs a shell: bash or zsh\n")
	default:
		fmt.Fprintf(os.Stderr, "cappu: unknown shell '%s' (expected: bash, zsh)\n", shell)
	}
	return 2
}
