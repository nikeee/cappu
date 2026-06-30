// Package sources bridges the project config and the package layer: it turns
// cappu.json's packageSources into PackageSource instances and its dependency
// maps into resolver roots. Port of the configuredSources / *Roots helpers in
// src/install.ts.
package sources

import (
	"sort"
	"strings"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/packages"
)

// Configured turns the config's packageSources into PackageSource instances
// (only Maven Central carries a search index), each wrapped in the on-disk
// metadata cache. Port of configuredSources in src/install.ts.
func Configured(cfg *config.Config) []packages.PackageSource {
	return configured(cfg, true)
}

// ConfiguredUncached is Configured without the on-disk metadata cache, for a
// fresh resolve that ignores everything cached (e.g. `cappu audit --no-cache`).
func ConfiguredUncached(cfg *config.Config) []packages.PackageSource {
	return configured(cfg, false)
}

func configured(cfg *config.Config, useCache bool) []packages.PackageSource {
	result := make([]packages.PackageSource, 0, len(cfg.PackageSources))
	for _, url := range cfg.PackageSources {
		searchURL := ""
		if url == config.MavenCentral {
			searchURL = config.MavenCentralSearch
		}
		src := packages.PackageSource(packages.NewMavenRepositorySource(url, searchURL))
		if useCache {
			src = WithMetadataCache(src)
		}
		result = append(result, src)
	}
	return result
}

// RootsOf turns a "group:artifact" -> version map into coordinates. Go map
// iteration is unordered, so the keys are sorted for a deterministic resolution
// order (the Node build relies on cappu.json's object-key order, which JSON
// decoding into a map cannot preserve).
func RootsOf(entries map[string]string) []packages.Coordinates {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	roots := make([]packages.Coordinates, 0, len(entries))
	for _, key := range keys {
		groupID, artifactID, _ := strings.Cut(key, ":")
		roots = append(roots, packages.NewCoordinates(groupID, artifactID, entries[key]))
	}
	return roots
}

// CompileRoots are every COMPILE dependency (api + implementation alike). The
// annotationProcessor configuration deliberately stays out - processor
// classpaths must not version-mediate against the app classpath.
func CompileRoots(cfg *config.Config) []packages.Coordinates {
	return append(RootsOf(cfg.Dependencies.API), RootsOf(cfg.Dependencies.Implementation)...)
}

// ProcessorRoots are the annotationProcessor configuration's roots.
func ProcessorRoots(cfg *config.Config) []packages.Coordinates {
	return RootsOf(cfg.Dependencies.AnnotationProcessor)
}

// TestRoots are the testImplementation configuration's roots.
func TestRoots(cfg *config.Config) []packages.Coordinates {
	return RootsOf(cfg.Dependencies.TestImplementation)
}
