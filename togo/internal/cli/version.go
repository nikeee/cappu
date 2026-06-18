package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/semver"
)

// RunVersion handles `cappu version major|minor|patch`: bump the version in
// cappu.json (semver, comments preserved). When cappu.json sits at the root of
// a git repository, also commit the bump and create a `v<version>` tag - like
// `npm version`. Port of src/cli/version.ts.
func RunVersion(release, configPathArg string, cfg *config.Config) int {
	out := painter(os.Stdout)
	errp := painter(os.Stderr)

	if !semver.IsReleaseType(release) {
		var names []string
		for _, r := range semver.ReleaseTypes {
			names = append(names, string(r))
		}
		fmt.Fprintf(os.Stderr, "cappu: version needs one of: %s\n", strings.Join(names, ", "))
		return 2
	}
	if !cfg.FromFile {
		fmt.Fprintf(os.Stderr, "%s no cappu.json found - run `cappu init` first\n", errp("red", "error:"))
		return 1
	}
	if cfg.Version == "" {
		fmt.Fprintf(os.Stderr, "%s cappu.json has no \"version\" to bump\n", errp("red", "error:"))
		return 1
	}

	next, err := semver.Bump(cfg.Version, semver.ReleaseType(release))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %s\n", errp("red", "error:"), err)
		return 1
	}
	tag := "v" + next

	configPath := filepath.Join(cfg.BaseDir, config.DefaultConfigName)
	if configPathArg != "" {
		if abs, e := filepath.Abs(configPathArg); e == nil {
			configPath = abs
		}
	}
	text, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %s\n", errp("red", "error:"), err)
		return 1
	}
	updated, err := config.SetStringField(text, "version", next)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %s\n", errp("red", "error:"), err)
		return 1
	}
	if err := os.WriteFile(configPath, updated, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "%s %s\n", errp("red", "error:"), err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "%s\n", out("green", tag))

	// Commit + tag only when cappu.json is at the git repository root (npm-style).
	toplevel, ok := gitToplevel(cfg.BaseDir)
	if !ok {
		return 0 // not a git repo: bump only
	}
	if !sameDir(toplevel, cfg.BaseDir) {
		fmt.Fprint(os.Stderr, errp("dim", "not the git repository root - bumped cappu.json only\n"))
		return 0
	}
	// Path-limited commit: only the cappu.json change, never other working edits.
	if err := runGit(cfg.BaseDir, "commit", "-m", tag, "--", configPath); err != nil {
		fmt.Fprintf(os.Stderr, "%s could not commit/tag %s: %v\n", errp("yellow", "warning:"), tag, err)
		return 0
	}
	if err := runGit(cfg.BaseDir, "tag", tag); err != nil {
		fmt.Fprintf(os.Stderr, "%s could not commit/tag %s: %v\n", errp("yellow", "warning:"), tag, err)
		return 0
	}
	fmt.Fprint(os.Stderr, errp("dim", fmt.Sprintf("committed and tagged %s\n", tag)))
	return 0
}

// gitToplevel returns the git repository root containing cwd, or ok=false when
// not in a repo.
func gitToplevel(cwd string) (string, bool) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

func runGit(cwd string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	return cmd.Run()
}

func sameDir(a, b string) bool {
	ra, errA := filepath.Abs(a)
	rb, errB := filepath.Abs(b)
	return errA == nil && errB == nil && ra == rb
}
