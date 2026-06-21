package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/packages"
	"github.com/nikeee/cappu/internal/sources"
)

// searchHitJSON is one match in --json output. Port of the shape in search.ts.
type searchHitJSON struct {
	GroupID    string `json:"groupId"`
	ArtifactID string `json:"artifactId"`
	Version    string `json:"version"`
}

// RunSearch handles `cappu search <query>`: free-text search across the
// configured package sources (deduplicated by group:artifact, source order
// wins). With jsonOut, the matches are emitted machine-readable. Port of
// src/cli/search.ts.
func RunSearch(query string, cfg *config.Config, jsonOut bool) int {
	hits, err := packages.SearchPackages(query, sources.Configured(cfg))
	if err != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
		return 1
	}
	if jsonOut {
		out := make([]searchHitJSON, 0, len(hits))
		for _, hit := range hits {
			out = append(out, searchHitJSON{string(hit.GroupID), string(hit.ArtifactID), string(hit.Version)})
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Fprintf(os.Stdout, "%s\n", b)
		if len(hits) == 0 {
			return 1
		}
		return 0
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
