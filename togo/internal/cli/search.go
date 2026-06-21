package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/packages"
	"github.com/nikeee/cappu/internal/sources"
)

// searchHitJSON is one match in --json output. Port of the shape in search.ts.
// The optional extras are omitted when absent, matching JSON.stringify dropping
// undefined keys.
type searchHitJSON struct {
	GroupID      string `json:"groupId"`
	ArtifactID   string `json:"artifactId"`
	Version      string `json:"version"`
	Packaging    string `json:"packaging,omitempty"`
	VersionCount *int   `json:"versionCount,omitempty"`
	LastUpdated  *int64 `json:"lastUpdated,omitempty"`
}

// extraColumns returns the hit's optional facts as formatted display columns.
// Port of extraColumns in src/cli/search.ts.
func extraColumns(hit packages.SearchHit) []string {
	var columns []string
	if hit.Packaging != "" {
		columns = append(columns, hit.Packaging)
	}
	if hit.VersionCount != nil {
		suffix := "s"
		if *hit.VersionCount == 1 {
			suffix = ""
		}
		columns = append(columns, fmt.Sprintf("%d version%s", *hit.VersionCount, suffix))
	}
	if hit.LastUpdated != nil {
		// epoch ms -> "YYYY-MM"; the day is noise for a "last published" hint
		columns = append(columns, "updated "+time.UnixMilli(*hit.LastUpdated).UTC().Format("2006-01"))
	}
	return columns
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
			out = append(out, searchHitJSON{
				GroupID:      string(hit.GroupID),
				ArtifactID:   string(hit.ArtifactID),
				Version:      string(hit.Version),
				Packaging:    hit.Packaging,
				VersionCount: hit.VersionCount,
				LastUpdated:  hit.LastUpdated,
			})
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

	paint := painter(os.Stdout)

	// The summary line goes to stderr so stdout stays a clean, pipeable list.
	fmt.Fprintf(os.Stderr, "found %s package(s) for '%s'\n",
		paint("bold", paint("cyan", strconv.Itoa(len(hits)))), query)

	// Pad the coordinate and version columns to their widest entry so the
	// optional extra columns line up across rows.
	coordinate := func(h packages.SearchHit) string {
		return string(h.GroupID) + ":" + string(h.ArtifactID)
	}
	coordinateWidth, versionWidth := 0, 0
	for _, hit := range hits {
		if w := len(coordinate(hit)); w > coordinateWidth {
			coordinateWidth = w
		}
		if w := len(hit.Version); w > versionWidth {
			versionWidth = w
		}
	}

	for _, hit := range hits {
		cells := []string{
			"  " + paint("bold", fmt.Sprintf("%-*s", coordinateWidth, coordinate(hit))),
			paint("cyan", fmt.Sprintf("%-*s", versionWidth, string(hit.Version))),
		}
		if extras := extraColumns(hit); len(extras) > 0 {
			cells = append(cells, paint("dim", strings.Join(extras, "  ")))
		}
		fmt.Fprintln(os.Stdout, strings.Join(cells, "  "))
	}
	return 0
}
