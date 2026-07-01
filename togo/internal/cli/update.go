package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/install"
	"github.com/nikeee/cappu/internal/sources"
)

// RunUpdate handles `cappu update`: bump every declared dependency to the
// newest stable version that keeps its configuration's transitive graph
// conflict-free, rewrite cappu.json (comments preserved), then refresh the lock
// via install. Port of src/cli/update.ts.
func RunUpdate(configPathArg string, cfg *config.Config) int {
	if !cfg.FromFile {
		fmt.Fprintln(os.Stderr, "cappu: no cappu.json found - run `cappu init` first")
		emitAnnotation("error", "no cappu.json found - run `cappu init` first", AnnotationLocation{})
		return 1
	}

	errp := painter(os.Stderr)
	fmt.Fprint(os.Stderr, errp("cyan", "checking for updates...\n"))

	bumps, err := install.PlanUpdates(cfg, sources.Configured(cfg))
	if err != nil {
		fmt.Fprintf(os.Stderr, "cappu: update failed: %s\n", err)
		emitAnnotation("error", fmt.Sprintf("update failed: %s", err), AnnotationLocation{})
		return 2
	}
	if len(bumps) == 0 {
		fmt.Fprintln(os.Stdout, "all dependencies are up to date")
		return 0
	}

	configPath := filepath.Join(cfg.BaseDir, config.DefaultConfigName)
	if configPathArg != "" {
		if abs, e := filepath.Abs(configPathArg); e == nil {
			configPath = abs
		}
	}
	text, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
		return 1
	}
	for _, b := range bumps {
		text, err = config.SetDependency(text, b.Configuration, b.Key, b.To)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
			return 1
		}
	}
	if err := os.WriteFile(configPath, text, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
		return 1
	}
	for _, b := range bumps {
		fmt.Fprintf(os.Stderr, "updated %s: %s -> %s\n", b.Key, b.From, b.To)
	}

	reloaded, err := config.Load(configPath, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
		return 1
	}
	return runInstallWith(reloaded, false, true)
}
