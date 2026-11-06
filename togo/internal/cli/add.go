package cli

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/install"
	"github.com/nikeee/cappu/internal/packages"
	"github.com/nikeee/cappu/internal/sources"
)

var configurations = config.Configurations

// configurationAliases maps short aliases to the full configuration name.
var configurationAliases = map[string]string{
	"a":  "api",
	"i":  "implementation",
	"co": "compileOnly",
	"ro": "runtimeOnly",
	"ap": "annotationProcessor",
	"ti": "testImplementation",
	"p":  config.PlatformConfiguration,
}

// usageConfigurations is the <configuration> placeholder of add/remove.
var usageConfigurations = strings.Join(append(append([]string(nil), configurations...), config.PlatformConfiguration), "|")

const usageAliases = "aliases: a=api, i=implementation, co=compileOnly, ro=runtimeOnly, ap=annotationProcessor, ti=testImplementation, p=platform"

// resolveConfiguration resolves a configuration name or short alias to its
// canonical form, or "" if it matches neither. platform (the BOM list) is
// accepted next to the classpath configurations.
func resolveConfiguration(arg string) string {
	for _, c := range configurations {
		if c == arg {
			return c
		}
	}
	if arg == config.PlatformConfiguration {
		return arg
	}
	return configurationAliases[arg]
}

// AddOptions are the `cappu add` flags that shape every entry written.
type AddOptions struct {
	Classifier string
	Exclude    []string
}

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
// and their transitive dependencies like `cappu install`. Without a version, a
// dependency a platform manages is written as "" (the platform's version);
// otherwise the newest conflict-free version is picked. Port of src/cli/add.ts.
func RunAdd(configurationArg string, specs []string, opts AddOptions, configPathArg string, cfg *config.Config) int {
	configuration := resolveConfiguration(configurationArg)
	platform := configuration == config.PlatformConfiguration
	coords := make([]addCoordinate, 0, len(specs))
	var invalid []string
	for _, spec := range specs {
		if c, ok := parseAddCoordinate(spec); ok {
			coords = append(coords, c)
		} else {
			invalid = append(invalid, spec)
		}
	}
	if platform && (opts.Classifier != "" || len(opts.Exclude) > 0) {
		fmt.Fprintln(os.Stderr, "cappu: a platform entry takes no --classifier or --exclude")
		emitAnnotation("error", "a platform entry takes no --classifier or --exclude", AnnotationLocation{})
		return 2
	}
	if opts.Classifier != "" && !config.MavenID.MatchString(opts.Classifier) {
		fmt.Fprintf(os.Stderr, "cappu: not a classifier: '%s'\n", opts.Classifier)
		emitAnnotation("error", fmt.Sprintf("not a classifier: '%s'", opts.Classifier), AnnotationLocation{})
		return 2
	}
	for _, pattern := range opts.Exclude {
		if !config.IsExcludePattern(pattern) {
			fmt.Fprintf(os.Stderr, "cappu: --exclude '%s' must be group:artifact (either side may be *)\n", pattern)
			emitAnnotation("error", fmt.Sprintf("--exclude '%s' must be group:artifact (either side may be *)", pattern), AnnotationLocation{})
			return 2
		}
	}
	if configuration == "" || len(coords) == 0 || len(invalid) > 0 {
		for _, spec := range invalid {
			fmt.Fprintf(os.Stderr, "cappu: not a coordinate: '%s'\n", spec)
			emitAnnotation("error", fmt.Sprintf("not a coordinate: '%s'", spec), AnnotationLocation{})
		}
		fmt.Fprintf(os.Stderr, "usage: cappu add <%s> <group:artifact[:version]> [more...] [--classifier <c>] [--exclude <group:artifact>]...\n"+
			"       %s\n"+
			"e.g.:  cappu add implementation com.google.code.gson:gson:2.14.0 org.slf4j:slf4j-api\n"+
			"       cappu add platform org.springframework.boot:spring-boot-dependencies:4.1.0\n",
			usageConfigurations, usageAliases)
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
	srcs := sources.Configured(cfg)
	managed, err := sources.ManagedVersions(cfg, srcs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
		emitAnnotation("error", err.Error(), AnnotationLocation{})
		return 1
	}

	// Pick every version FIRST against an in-memory config that accumulates the
	// earlier additions, writing nothing until all picks succeed - so a failure
	// mid-way leaves cappu.json untouched.
	working := cfg
	type pick struct {
		key string
		dep config.Dependency
	}
	var picks []pick
	for _, coord := range coords {
		version := coord.version
		// An entry already written keeps its classifier and exclusions unless
		// the flags replace them; a bump via `add` is then a version change only.
		dep := working.Dependencies.Configuration(configuration)[coord.key]
		if opts.Classifier != "" {
			dep.Classifier = opts.Classifier
		}
		if len(opts.Exclude) > 0 {
			dep.Exclude = opts.Exclude
		}
		groupID, artifactID, _ := strings.Cut(coord.key, ":")
		_, isManaged := managed[packages.NewCoordinates(groupID, artifactID, "").WithClassifier(dep.Classifier).Key()]
		switch {
		case version == "" && !platform && isManaged:
			// written as "": the platform's version, moving with the platform
		case !looksExact(version):
			picked, ok, err := install.PickAddVersion(working, coord.key, version, srcs, managed)
			if err != nil {
				fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
				emitAnnotation("error", err.Error(), AnnotationLocation{})
				return 1
			}
			if !ok {
				wanted := ""
				if version != "" {
					wanted = fmt.Sprintf(" matching '%s'", version)
				}
				fmt.Fprintf(os.Stderr, "cappu: no published version of %s%s found in any package source\n", coord.key, wanted)
				emitAnnotation("error", fmt.Sprintf("no published version of %s%s found in any package source", coord.key, wanted), AnnotationLocation{})
				return 1
			}
			if !picked.Compatible {
				fmt.Fprintf(os.Stderr, "warning: every matching version of %s conflicts with the configured dependencies; using %s\n", coord.key, picked.Version)
				emitAnnotation("warning", fmt.Sprintf("every matching version of %s conflicts with the configured dependencies; using %s", coord.key, picked.Version), AnnotationLocation{})
			}
			version = picked.Version
		}
		dep.Version = version
		picks = append(picks, pick{coord.key, dep})
		working = withDependency(working, configuration, coord.key, dep)
	}

	text, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
		return 1
	}
	for _, p := range picks {
		if platform {
			text, err = config.SetDependency(text, configuration, p.key, p.dep.Version)
		} else {
			text, err = config.SetDependencyEntry(text, configuration, p.key, p.dep)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
			return 1
		}
	}
	// Never write a cappu.json that will not load back.
	reloaded, err := config.Parse(text, configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
		emitAnnotation("error", err.Error(), AnnotationLocation{})
		return 1
	}
	if err := os.WriteFile(configPath, text, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
		return 1
	}
	for _, p := range picks {
		if p.dep.Managed() {
			fmt.Fprintf(os.Stderr, "added %s %s (version from platform)\n", configuration, p.key)
		} else {
			fmt.Fprintf(os.Stderr, "added %s %s:%s\n", configuration, p.key, p.dep.Version)
		}
	}
	return runInstallWith(reloaded, false, true)
}

// withDependency returns a shallow copy of cfg with one entry added to a
// configuration (or the platform list), for threading earlier picks into
// later ones.
func withDependency(cfg *config.Config, configuration, key string, dep config.Dependency) *config.Config {
	clone := *cfg
	if configuration == config.PlatformConfiguration {
		platform := maps.Clone(cfg.Dependencies.Platform)
		if _, exists := platform[key]; !exists {
			// a new platform is declared last: lowest precedence, like the
			// member SetDependency appends
			clone.Dependencies.PlatformOrder = append(slices.Clone(cfg.Dependencies.PlatformKeys()), key)
		}
		platform[key] = dep.Version
		clone.Dependencies.Platform = platform
		return &clone
	}
	target := maps.Clone(cfg.Dependencies.Configuration(configuration))
	target[key] = dep
	clone.Dependencies.SetConfiguration(configuration, target)
	return &clone
}
