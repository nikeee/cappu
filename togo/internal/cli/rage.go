package cli

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/nikeee/cappu/internal/meta"
)

// browserOpener is the platform's "open this with whatever's registered"
// launcher for the issue tracker url.
func browserOpener() (string, []string) {
	switch runtime.GOOS {
	case "darwin":
		return "open", []string{meta.IssueTracker}
	case "windows":
		// `start` is a cmd builtin, not an executable; the empty "" is its title arg.
		return "cmd", []string{"/c", "start", "", meta.IssueTracker}
	default:
		return "xdg-open", []string{meta.IssueTracker}
	}
}

// RunRage opens the issue tracker in the default browser - for when a bug has
// worn you down enough to file it. Port of src/cli/rage.ts.
func RunRage() int {
	command, args := browserOpener()
	cmd := exec.Command(command, args...)
	// Detach: let the launcher outlive us; discard its streams.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "cappu: could not open a browser; file it at %s\n", meta.IssueTracker)
		return 1
	}
	_ = cmd.Process.Release()
	fmt.Fprintf(os.Stderr, "opening %s\n", meta.IssueTracker)
	return 0
}
