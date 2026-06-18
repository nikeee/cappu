package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/install"
	"github.com/nikeee/cappu/internal/sources"
)

var configurations = []string{"api", "implementation", "annotationProcessor", "testImplementation"}

// addCoordinate is a parsed "group:artifact[:version]" spec.
type addCoordinate struct {
	key     string // "group:artifact"
	version string // empty to use the newest published one
}

// parseAddCoordinate parses the Gradle/Maven "group:artifact[:version]" form
// (so a coordinate copied from a build file works as-is). ok is false otherwise.
func parseAddCoordinate(spec string) (addCoordinate, bool) {
	segments := strings.Split(spec, ":")
	for _, s := range segments {
		if s == "" {
			return addCoordinate{}, false
		}
	}
	switch len(segments) {
	case 2:
		return addCoordinate{key: spec}, true
	case 3:
		return addCoordinate{key: segments[0] + ":" + segments[1], version: segments[2]}, true
	default:
		return addCoordinate{}, false
	}
}

// looksExact reports whether a written spec is exact enough to skip the picker
// (two dots or a dash qualifier is a full Maven version; "2"/"2.10" are prefixes).
func looksExact(version string) bool {
	return version != "" && (strings.Count(version, ".") >= 2 || strings.Contains(version, "-"))
}

// RunAdd handles `cappu add <configuration> <group:artifact[:version]>...`:
// write the entries into cappu.json (comments preserved), then download them
// and their transitive dependencies like `cappu install`. Port of src/cli/add.ts.
func RunAdd(configurationArg string, specs []string, configPathArg string, cfg *config.Config) int {
	configuration := ""
	for _, c := range configurations {
		if c == configurationArg {
			configuration = c
		}
	}
	coords := make([]addCoordinate, 0, len(specs))
	var invalid []string
	for _, spec := range specs {
		if c, ok := parseAddCoordinate(spec); ok {
			coords = append(coords, c)
		} else {
			invalid = append(invalid, spec)
		}
	}
	if configuration == "" || len(coords) == 0 || len(invalid) > 0 {
		for _, spec := range invalid {
			fmt.Fprintf(os.Stderr, "cappu: not a coordinate: '%s'\n", spec)
		}
		fmt.Fprintf(os.Stderr, "usage: cappu add <%s> <group:artifact[:version]> [more...]\n"+
			"e.g.:  cappu add implementation com.google.code.gson:gson:2.14.0 org.slf4j:slf4j-api\n",
			strings.Join(configurations, "|"))
		return 2
	}
	if !cfg.FromFile {
		fmt.Fprintln(os.Stderr, "cappu: no cappu.json found - run `cappu init` first")
		return 1
	}

	configPath := filepath.Join(cfg.BaseDir, config.DefaultConfigName)
	if configPathArg != "" {
		if abs, err := filepath.Abs(configPathArg); err == nil {
			configPath = abs
		}
	}
	srcs := sources.Configured(cfg)

	// Pick every version FIRST against an in-memory config that accumulates the
	// earlier additions, writing nothing until all picks succeed - so a failure
	// mid-way leaves cappu.json untouched.
	working := cfg
	type pick struct{ key, version string }
	var picks []pick
	for _, coord := range coords {
		version := coord.version
		if !looksExact(version) {
			picked, ok, err := install.PickAddVersion(working, coord.key, version, srcs)
			if err != nil {
				fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
				return 1
			}
			if !ok {
				wanted := ""
				if version != "" {
					wanted = fmt.Sprintf(" matching '%s'", version)
				}
				fmt.Fprintf(os.Stderr, "cappu: no published version of %s%s found in any package source\n", coord.key, wanted)
				return 1
			}
			if !picked.Compatible {
				fmt.Fprintf(os.Stderr, "warning: every matching version of %s conflicts with the configured dependencies; using %s\n", coord.key, picked.Version)
			}
			version = picked.Version
		}
		picks = append(picks, pick{coord.key, version})
		working = withDependency(working, configuration, coord.key, version)
	}

	text, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
		return 1
	}
	for _, p := range picks {
		text, err = config.SetDependency(text, configuration, p.key, p.version)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
			return 1
		}
	}
	if err := os.WriteFile(configPath, text, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
		return 1
	}
	for _, p := range picks {
		fmt.Fprintf(os.Stderr, "added %s %s:%s\n", configuration, p.key, p.version)
	}

	reloaded, err := config.Load(configPath, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
		return 1
	}
	return runInstallWith(reloaded, false, true)
}

// withDependency returns a shallow copy of cfg with one dependency added to a
// configuration, for threading earlier picks into later ones.
func withDependency(cfg *config.Config, configuration, key, version string) *config.Config {
	clone := *cfg
	clone.Dependencies = cfg.Dependencies
	target := map[string]string{}
	switch configuration {
	case "api":
		copyInto(target, cfg.Dependencies.API)
		target[key] = version
		clone.Dependencies.API = target
	case "implementation":
		copyInto(target, cfg.Dependencies.Implementation)
		target[key] = version
		clone.Dependencies.Implementation = target
	case "annotationProcessor":
		copyInto(target, cfg.Dependencies.AnnotationProcessor)
		target[key] = version
		clone.Dependencies.AnnotationProcessor = target
	case "testImplementation":
		copyInto(target, cfg.Dependencies.TestImplementation)
		target[key] = version
		clone.Dependencies.TestImplementation = target
	}
	return &clone
}

func copyInto(dst, src map[string]string) {
	for k, v := range src {
		dst[k] = v
	}
}
