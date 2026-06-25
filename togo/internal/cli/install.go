package cli

import (
	"fmt"
	"math"
	"os"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/install"
	"github.com/nikeee/cappu/internal/jdks"
	"github.com/nikeee/cappu/internal/packages"
)

// RunInstall handles `cappu install`: resolve + download the cappu.json
// dependencies (pinned in cappu-lock.json), then render the result - jars
// written, version conflicts (warnings), unresolvable packages (errors). Port
// of src/cli/install.ts.
//
// JDK provisioning (the config "jdk" entry) is NOT yet ported; install prints a
// notice and otherwise proceeds. See internal/jdks in the Node build.
func RunInstall(cfg *config.Config, verbose, locked bool) int {
	// --locked (CI): fail before downloading anything if the lock is stale or
	// missing relative to cappu.json.
	if locked {
		if ok, reason := install.CheckLocked(cfg); !ok {
			fmt.Fprintf(os.Stderr, "%s %s\n", painter(os.Stderr)("red", "error:"), reason)
			emitAnnotation("error", reason, AnnotationLocation{})
			return 1
		}
	}
	return runInstallWith(cfg, verbose, false)
}

// runInstallWith is the shared install renderer; updateLock re-resolves and
// rewrites the lock (used after `cappu add`/`update` change the dependencies).
func runInstallWith(cfg *config.Config, verbose, updateLock bool) int {
	out := painter(os.Stdout)
	errp := painter(os.Stderr)
	showProgress := ColorEnabled(isTTY(os.Stderr), os.Getenv)

	resolving := 0
	// Resolving (no lockfile) fetches a POM per package with no known total, so
	// it gets a count-up line; once downloads start it is wiped and replaced by
	// the bar. Port of the onResolve/onProgress dance in install.ts.
	var bar *downloadBar
	result, err := install.Dependencies(cfg, nil, install.Options{
		UpdateLock: updateLock,
		OnResolve: func(current packages.CoordinateString) {
			if showProgress {
				resolving++
				fmt.Fprintf(os.Stderr, "\r\x1b[2Kresolving %d %s", resolving, current)
			}
		},
		OnProgress: func(done, total int, current packages.CoordinateString) {
			if !showProgress {
				return
			}
			if resolving > 0 {
				fmt.Fprint(os.Stderr, "\r\x1b[2K") // wipe the resolving line before the bar
				resolving = 0
			}
			if bar == nil {
				bar = newDownloadBar(os.Stderr, "")
				bar.start(total)
			}
			bar.update(done, string(current))
		},
	})
	if resolving > 0 {
		fmt.Fprint(os.Stderr, "\r\x1b[2K") // nothing to download after resolve
	}
	bar.stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
		emitAnnotation("error", err.Error(), AnnotationLocation{})
		return 1
	}

	jdkFailed := provisionJDK(cfg, errp)
	if result.FromLock {
		fmt.Fprint(os.Stderr, errp("dim", "using cappu-lock.json\n"))
	}
	if len(result.FromStore) > 0 {
		fmt.Fprint(os.Stderr, errp("dim", fmt.Sprintf("%d package(s) from the local store\n", len(result.FromStore))))
	}
	if result.LockStale {
		fmt.Fprintf(os.Stderr, "%s cappu.json's dependencies changed since cappu-lock.json was written;\n"+
			"         the locked set was installed anyway. Use `cappu add` (or delete the\n"+
			"         lock file) to re-resolve.\n", errp("yellow", "warning:"))
		emitAnnotation("warning", "cappu.json's dependencies changed since cappu-lock.json was written; the locked set was installed anyway. Use `cappu add` (or delete the lock file) to re-resolve.", AnnotationLocation{})
	}

	if verbose {
		for _, file := range result.Installed {
			fmt.Fprintln(os.Stdout, file)
		}
	} else {
		fmt.Fprintf(os.Stdout, "%s %s installed\n", out("green", "✓"), summary(out, result.InstalledByCategory))
	}

	WarnUnmappedLicenses(result.Resolution.Packages)
	for _, c := range result.Resolution.Conflicts {
		fmt.Fprintf(os.Stderr, "%s %s: version %s (via %s) loses to %s\n",
			errp("yellow", "warning:"), c.Key, c.Rejected, c.RejectedBy.ArtifactID, c.Selected)
		emitAnnotation("warning", fmt.Sprintf("%s: version %s (via %s) loses to %s", c.Key, c.Rejected, c.RejectedBy.ArtifactID, c.Selected), AnnotationLocation{})
	}

	failed := false
	for _, m := range result.Resolution.Missing {
		via := ""
		if m.RequestedBy.ArtifactID != "" {
			via = fmt.Sprintf(" (required by %s)", m.RequestedBy.ArtifactID)
		}
		fmt.Fprintf(os.Stderr, "%s %s: not found in any package source%s\n", errp("red", "error:"), m.Coordinates.String(), via)
		emitAnnotation("error", fmt.Sprintf("%s: not found in any package source%s", m.Coordinates.String(), via), AnnotationLocation{})
		failed = true
	}
	for _, c := range result.NoArtifact {
		fmt.Fprintf(os.Stderr, "%s %s: source provided no jar\n", errp("red", "error:"), c)
		emitAnnotation("error", fmt.Sprintf("%s: source provided no jar", c), AnnotationLocation{})
		failed = true
	}
	for _, c := range result.IntegrityFailures {
		fmt.Fprintf(os.Stderr, "%s %s: downloaded jar does not match the SHA-256 in cappu-lock.json\n", errp("red", "error:"), c)
		emitAnnotation("error", fmt.Sprintf("%s: downloaded jar does not match the SHA-256 in cappu-lock.json", c), AnnotationLocation{})
		failed = true
	}
	if failed || jdkFailed {
		return 1
	}
	return 0
}

// provisionJDK provisions cfg's "jdk" entry (nikeee/cappu#8) into .cappu/jdks,
// rendering progress and the outcome. Returns true on failure. A no-jdk config
// is a no-op.
func provisionJDK(cfg *config.Config, errp func(format, text string) string) bool {
	if cfg.JDK == "" {
		return false
	}
	var bar *downloadBar
	result, err := jdks.Provision(cfg, cfg.JDK, func(received, total int64) {
		if total <= 0 {
			return
		}
		if bar == nil {
			bar = newDownloadBar(os.Stderr, "MiB")
			bar.start(int(math.Round(float64(total) / 1024 / 1024)))
		}
		bar.update(int(math.Round(float64(received)/1024/1024)), cfg.JDK)
	})
	bar.stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s jdk %s: %s\n", errp("red", "error:"), cfg.JDK, err)
		emitAnnotation("error", fmt.Sprintf("jdk %s: %s", cfg.JDK, err), AnnotationLocation{})
		return true
	}
	switch {
	case result.AlreadyProvisioned:
		fmt.Fprint(os.Stderr, errp("dim", fmt.Sprintf("jdk %s: already provisioned\n", cfg.JDK)))
	default:
		if result.FromCache {
			fmt.Fprint(os.Stderr, errp("dim", fmt.Sprintf("jdk %s: archive from the local cache\n", cfg.JDK)))
		}
		fmt.Fprintf(os.Stdout, "%s\n", result.JdkDir)
	}
	return false
}

// summary is the colourful per-category count (e.g. "3 compile dependencies, 1
// test dependency"), or "no packages".
func summary(out func(format, text string) string, cats install.Categories) string {
	type cat struct {
		n         int
		one, many string
	}
	var parts []string
	for _, c := range []cat{
		{len(cats.Compile), "compile dependency", "compile dependencies"},
		{len(cats.Processor), "annotation processor", "annotation processors"},
		{len(cats.Test), "test dependency", "test dependencies"},
	} {
		if c.n == 0 {
			continue
		}
		noun := c.many
		if c.n == 1 {
			noun = c.one
		}
		parts = append(parts, fmt.Sprintf("%s %s", out("bold", out("cyan", fmt.Sprintf("%d", c.n))), noun))
	}
	if len(parts) == 0 {
		return out("dim", "no packages")
	}
	joined := parts[0]
	for _, p := range parts[1:] {
		joined += ", " + p
	}
	return joined
}
