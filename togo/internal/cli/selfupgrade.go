package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nikeee/cappu/internal/selfupgrade"
)

// RunSelfUpgrade handles `cappu self-upgrade`: replace the running binary with
// the latest CD build. Refuses to run when the executable is not a cappu binary
// (e.g. `go run` / a test harness). Port of src/cli/selfUpgrade.ts.
func RunSelfUpgrade() int {
	errp := painter(os.Stderr)
	out := painter(os.Stdout)

	// CAPPU_UPGRADE_TARGET overrides the replaced path (tests / unusual installs).
	targetPath := os.Getenv("CAPPU_UPGRADE_TARGET")
	if targetPath == "" {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
			return 1
		}
		targetPath = exe
	}
	if !strings.HasPrefix(filepath.Base(targetPath), "cappu") {
		fmt.Fprintf(os.Stderr, "cappu: self-upgrade replaces the compiled cappu binary, but this is running via '%s'.\n"+
			"       Run the installed `cappu` binary, or set CAPPU_UPGRADE_TARGET to its path.\n", targetPath)
		return 2
	}

	token, ok := selfupgrade.ResolveToken()
	if !ok {
		fmt.Fprintln(os.Stderr, "cappu: self-upgrade needs a GitHub token to read CD build artifacts.\n"+
			"       Set GITHUB_TOKEN (or run `gh auth login`).")
		return 2
	}

	fmt.Fprint(os.Stderr, errp("bold", errp("cyan", "fetching the latest CD build...\n")))
	result, err := selfupgrade.SelfUpgrade(selfupgrade.Options{
		TargetPath: targetPath,
		Token:      token,
		GOOS:       runtime.GOOS,
		GOARCH:     runtime.GOARCH,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s self-upgrade failed: %s\n", errp("red", "error:"), err)
		return 1
	}
	sha := result.Artifact.RunSha
	if len(sha) > 7 {
		sha = sha[:7]
	}
	fmt.Fprintf(os.Stdout, "%s upgraded %s to %s (%s, built %s)\n",
		out("green", "✓"), result.TargetPath, out("bold", result.Target.Artifact), out("cyan", sha), result.Artifact.RunCreatedAt)
	return 0
}
