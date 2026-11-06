// Package sources bridges the project config and the package layer: it turns
// cappu.json's packageSources into PackageSource instances and its dependency
// maps into resolver roots. Port of the configuredSources / *Roots helpers in
// src/install.ts.
package sources

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/httpx"
	"github.com/nikeee/cappu/internal/packages"
)

// Configured turns the config's packageSources into PackageSource instances
// (only Maven Central carries a search index), each remote one wrapped in the
// on-disk metadata cache. Port of configuredSources in src/install.ts.
func Configured(cfg *config.Config) []packages.PackageSource {
	return FromEntries(cfg, cfg.PackageSources, true, os.Getenv)
}

// ConfiguredUncached is Configured without the on-disk metadata cache, for a
// fresh resolve that ignores everything cached (e.g. `cappu audit --no-cache`).
func ConfiguredUncached(cfg *config.Config) []packages.PackageSource {
	return FromEntries(cfg, cfg.PackageSources, false, os.Getenv)
}

// FromEntries builds the sources for explicit packageSources entries. A local
// entry reads the directory (never cached: it changes under cappu). An entry
// with auth sends the credentials CAPPU_SOURCE_<auth>_USERNAME + _PASSWORD or
// _TOKEN from getenv; when they are missing the source is still listed but
// every fetch from it fails naming the variables, so a dependency only it
// serves fails loudly instead of being reported as not found.
func FromEntries(cfg *config.Config, entries []config.PackageSource, useCache bool, getenv func(string) string) []packages.PackageSource {
	result := make([]packages.PackageSource, 0, len(entries))
	for _, entry := range entries {
		if entry.IsLocal() {
			// named by the entry as written, so the lock's `source` stays the
			// same on every machine ("~/.m2/repository", not /home/<user>/...)
			result = append(result, packages.NewFileRepositorySource(entry.LocalDir(cfg), entry.URL))
			continue
		}
		searchURL := ""
		if entry.URL == config.MavenCentral {
			searchURL = config.MavenCentralSearch
		}
		var src packages.PackageSource
		if entry.Auth == "" {
			src = packages.NewMavenRepositorySource(entry.URL, searchURL)
		} else {
			prefix := "CAPPU_SOURCE_" + entry.Auth + "_"
			auth, ok := httpx.ResolveAuth(getenv(prefix+"USERNAME"), getenv(prefix+"PASSWORD"), getenv(prefix+"TOKEN"))
			if ok {
				src = packages.NewAuthorizedMavenRepositorySource(entry.URL, searchURL, auth.Header())
			} else {
				src = packages.NewUnavailableSource(entry.URL, fmt.Errorf("%s: no credentials: set %sUSERNAME + %sPASSWORD, or %sTOKEN", entry.URL, prefix, prefix, prefix))
			}
		}
		if useCache {
			src = WithMetadataCache(src)
		}
		result = append(result, src)
	}
	return result
}

// RootsOf turns a "group:artifact" -> Dependency map into resolver roots. Go
// map iteration is unordered, so the keys are sorted for a deterministic
// resolution order (the Node build relies on cappu.json's object-key order,
// which JSON decoding into a map cannot preserve). A managed entry keeps its
// empty version for the resolver to fill from the platform.
func RootsOf(entries map[string]config.Dependency) []packages.DependencyDeclaration {
	keys := slices.Sorted(maps.Keys(entries))
	roots := make([]packages.DependencyDeclaration, 0, len(entries))
	for _, key := range keys {
		dep := entries[key]
		groupID, artifactID, _ := strings.Cut(key, ":")
		roots = append(roots, packages.DependencyDeclaration{
			Coordinates: packages.NewCoordinates(groupID, artifactID, dep.Version).WithClassifier(dep.Classifier),
			Exclusions:  dep.Exclude,
		})
	}
	return roots
}

// CompileRoots are every COMPILE dependency (api + implementation alike). The
// annotationProcessor configuration deliberately stays out - processor
// classpaths must not version-mediate against the app classpath.
func CompileRoots(cfg *config.Config) []packages.DependencyDeclaration {
	return append(RootsOf(cfg.Dependencies.API), RootsOf(cfg.Dependencies.Implementation)...)
}

// CompileOnlyRoots are the compileOnly configuration's roots.
func CompileOnlyRoots(cfg *config.Config) []packages.DependencyDeclaration {
	return RootsOf(cfg.Dependencies.CompileOnly)
}

// RuntimeOnlyRoots are the runtimeOnly configuration's roots.
func RuntimeOnlyRoots(cfg *config.Config) []packages.DependencyDeclaration {
	return RootsOf(cfg.Dependencies.RuntimeOnly)
}

// MainRoots are the roots of the one graph the main classpath resolves as:
// api, implementation, compileOnly and runtimeOnly (Maven's compile, provided
// and runtime scopes share a tree). Install partitions the result by scope.
func MainRoots(cfg *config.Config) []packages.DependencyDeclaration {
	roots := CompileRoots(cfg)
	roots = append(roots, CompileOnlyRoots(cfg)...)
	return append(roots, RuntimeOnlyRoots(cfg)...)
}

// AllRoots are every declared root: main, processors and tests, for the
// commands that look at the whole project (audit, licenses, the MCP tools).
func AllRoots(cfg *config.Config) []packages.DependencyDeclaration {
	roots := MainRoots(cfg)
	roots = append(roots, ProcessorRoots(cfg)...)
	return append(roots, TestRoots(cfg)...)
}

// ProcessorRoots are the annotationProcessor configuration's roots.
func ProcessorRoots(cfg *config.Config) []packages.DependencyDeclaration {
	return RootsOf(cfg.Dependencies.AnnotationProcessor)
}

// TestRoots are the testImplementation configuration's roots.
func TestRoots(cfg *config.Config) []packages.DependencyDeclaration {
	return RootsOf(cfg.Dependencies.TestImplementation)
}

// ManagedVersions is what the config's platform entries manage: each BOM's
// effective <dependencyManagement>, from the first source that knows it. When
// two platforms manage the same artifact the first declared in cappu.json wins
// (Maven: the first import wins).
func ManagedVersions(cfg *config.Config, srcs []packages.PackageSource) (map[packages.PackageKey]string, error) {
	return ManagedVersionsOf(cfg.Dependencies, srcs)
}

// ManagedVersionsOf is ManagedVersions over an explicit dependencies section
// (only its Platform and PlatformOrder are read).
func ManagedVersionsOf(deps config.Dependencies, srcs []packages.PackageSource) (map[packages.PackageKey]string, error) {
	platform := deps.Platform
	managed := map[packages.PackageKey]string{}
	for _, key := range deps.PlatformKeys() {
		groupID, artifactID, _ := strings.Cut(key, ":")
		bom := packages.NewCoordinates(groupID, artifactID, platform[key])
		var entries map[packages.PackageKey]string
		for _, source := range srcs {
			found, err := source.GetManaged(bom)
			if err != nil {
				return nil, err
			}
			if found != nil {
				entries = found
				break
			}
		}
		if entries == nil {
			return nil, fmt.Errorf("platform %s: not found in any package source", bom.String())
		}
		for k, v := range entries {
			if _, ok := managed[k]; !ok {
				managed[k] = v
			}
		}
	}
	return managed, nil
}
