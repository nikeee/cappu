package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nikeee/cappu/internal/config"
)

// RunRemove handles `cappu remove <configuration> <group:artifact>...`: drop the
// entries from cappu.json (comments preserved), then re-resolve and rewrite the
// lock + .cappu/lib like `cappu add` in reverse. The version segment of a
// coordinate is ignored - a dependency is removed by its group:artifact key.
// Port of src/cli/remove.ts.
func RunRemove(configurationArg string, specs []string, configPathArg string, cfg *config.Config) int {
	configuration := resolveConfiguration(configurationArg)
	keys := make([]string, 0, len(specs))
	var invalid []string
	for _, spec := range specs {
		if c, ok := parseAddCoordinate(spec); ok {
			keys = append(keys, c.key)
		} else {
			invalid = append(invalid, spec)
		}
	}
	if configuration == "" || len(keys) == 0 || len(invalid) > 0 {
		for _, spec := range invalid {
			fmt.Fprintf(os.Stderr, "cappu: not a coordinate: '%s'\n", spec)
			emitAnnotation("error", fmt.Sprintf("not a coordinate: '%s'", spec), AnnotationLocation{})
		}
		fmt.Fprintf(os.Stderr, "usage: cappu remove <%s> <group:artifact> [more...]\n"+
			"       aliases: a=api, i=implementation, ap=annotationProcessor, ti=testImplementation\n",
			strings.Join(configurations, "|"))
		return 2
	}
	if !cfg.FromFile {
		fmt.Fprintln(os.Stderr, "cappu: no cappu.json found - run `cappu init` first")
		emitAnnotation("error", "no cappu.json found - run `cappu init` first", AnnotationLocation{})
		return 1
	}

	configPath := filepath.Join(cfg.BaseDir, config.DefaultConfigName)
	if configPathArg != "" {
		if abs, err := filepath.Abs(configPathArg); err == nil {
			configPath = abs
		}
	}

	text, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
		return 1
	}
	any := false
	for _, key := range keys {
		next, removed, err := config.RemoveDependency(text, configuration, key)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
			return 1
		}
		if removed {
			text = next
			any = true
			fmt.Fprintf(os.Stderr, "removed %s %s\n", configuration, key)
		} else {
			fmt.Fprintf(os.Stderr, "warning: %s is not a %s dependency\n", key, configuration)
			emitAnnotation("warning", fmt.Sprintf("%s is not a %s dependency", key, configuration), AnnotationLocation{})
		}
	}
	if !any {
		return 1
	}
	if err := os.WriteFile(configPath, text, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
		return 1
	}

	reloaded, err := config.Load(configPath, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
		return 1
	}
	return runInstallWith(reloaded, false, true)
}
