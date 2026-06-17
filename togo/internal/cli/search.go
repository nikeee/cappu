package cli

import (
	"fmt"
	"os"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/packages"
)

// configuredSources turns the config's packageSources into PackageSource
// instances (only Maven Central is searchable). Port of configuredSources in
// src/install.ts, search path only - the on-disk metadata cache arrives with
// the install command.
func configuredSources(cfg *config.Config) []packages.PackageSource {
	sources := make([]packages.PackageSource, 0, len(cfg.PackageSources))
	for _, url := range cfg.PackageSources {
		searchURL := ""
		if url == config.MavenCentral {
			searchURL = config.MavenCentralSearch
		}
		sources = append(sources, packages.NewMavenRepositorySource(url, searchURL))
	}
	return sources
}

// RunSearch handles `cappu search <query>`: free-text search across the
// configured package sources (deduplicated by group:artifact, source order
// wins). Port of src/cli/search.ts.
func RunSearch(query string, cfg *config.Config) int {
	hits, err := packages.SearchPackages(query, configuredSources(cfg))
	if err != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
		return 1
	}
	if len(hits) == 0 {
		fmt.Fprintf(os.Stderr, "no packages found for '%s'\n", query)
		return 1
	}
	for _, hit := range hits {
		fmt.Fprintf(os.Stdout, "%s:%s@%s\n", hit.GroupID, hit.ArtifactID, hit.Version)
	}
	return 0
}
