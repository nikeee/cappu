package cli

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/nikeee/cappu/internal/meta"
)

// rageReport is the environment block the user can paste into a bug report.
func rageReport() string {
	return fmt.Sprintf(
		"cappu %s\nruntime: go %s\nplatform: %s %s\n\nfile an issue at %s\n",
		meta.Version, runtime.Version(), nodePlatform(runtime.GOOS), nodeArch(runtime.GOARCH), meta.IssueTracker,
	)
}

// nodePlatform/nodeArch map Go's GOOS/GOARCH to Node's process.platform /
// process.arch tokens, so the platform line matches the TS build.
func nodePlatform(goos string) string {
	if goos == "windows" {
		return "win32"
	}
	return goos
}

func nodeArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	default:
		return goarch
	}
}

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

// RunRage prints version/environment info plus the issue tracker URL - for when
// a bug has worn you down enough to file it. With open, it also opens the
// tracker in the default browser. Port of src/cli/rage.ts.
func RunRage(open bool) int {
	fmt.Fprint(os.Stdout, rageReport())

	if !open {
		return 0
	}

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
