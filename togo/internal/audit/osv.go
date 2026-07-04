package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nikeee/cappu/internal/cache"
	"github.com/nikeee/cappu/internal/httpx"
	"github.com/nikeee/cappu/internal/packages"
)

// OSV.dev as an AuditSource (https://api.osv.dev). Free, no auth, Maven-aware,
// version-range matching server-side: querybatch returns the vuln ids affecting
// each {package, version}, then each id is hydrated once for its details. The
// fetcher is injectable so everything is testable without a network. Port of
// src/audit/osv.ts.

const osvAPI = "https://api.osv.dev"

// osvBatch is the querybatch chunk size (well under OSV's limit).
const osvBatch = 1000

// FetchJSON POSTs when body is non-nil, else GETs; returns the raw JSON bytes.
type FetchJSON func(url string, body any) ([]byte, error)

func defaultFetchJSON(url string, body any) ([]byte, error) {
	method := http.MethodGet
	var reader io.Reader
	if body != nil {
		method = http.MethodPost
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	resp, err := httpx.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("OSV %d for %s", resp.StatusCode, url)
	}
	return httpx.ReadAllCapped(resp.Body)
}

// osvVulnID matches OSV ids (GHSA-/CVE-/GO-...) - safe filename characters.
var osvVulnID = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// vulnCacheTTL bounds the detail cache: advisory details are revised (OSV
// records carry a `modified` timestamp), so they are refreshed daily.
const vulnCacheTTL = 24 * time.Hour

func vulnCachePath(id string) (string, bool) {
	if !osvVulnID.MatchString(id) {
		return "", false
	}
	return filepath.Join(cache.Dir("packages", os.Getenv("CAPPU_PACKAGE_STORE")), "_audit", "osv", id+".json"), true
}

type cacheEntry struct {
	FetchedAt int64           `json:"fetchedAt"`
	Body      json.RawMessage `json:"body"`
}

// CachedFetchJSON wraps inner so the vuln-detail GETs (/v1/vulns/{id}) are
// cached in the package store with a one-day TTL. The querybatch lookup (which
// advisories affect a version) is never cached, so audit always surfaces fresh
// findings. Port of cachedFetchJson.
func CachedFetchJSON(inner FetchJSON) FetchJSON {
	if inner == nil {
		inner = defaultFetchJSON
	}
	return func(url string, body any) ([]byte, error) {
		cacheFile := ""
		if body == nil {
			if _, id, ok := strings.Cut(url, "/v1/vulns/"); ok {
				if path, safe := vulnCachePath(id); safe {
					cacheFile = path
				}
			}
		}
		if cacheFile != "" {
			if raw, err := os.ReadFile(cacheFile); err == nil {
				var entry cacheEntry
				if json.Unmarshal(raw, &entry) == nil &&
					time.Now().UnixMilli()-entry.FetchedAt < vulnCacheTTL.Milliseconds() {
					return entry.Body, nil
				}
			}
		}
		result, err := inner(url, body)
		if err != nil {
			return nil, err
		}
		if cacheFile != "" && result != nil {
			entry := cacheEntry{FetchedAt: time.Now().UnixMilli(), Body: result}
			if encoded, err := json.Marshal(entry); err == nil {
				if mkErr := os.MkdirAll(filepath.Dir(cacheFile), 0o755); mkErr == nil {
					_ = os.WriteFile(cacheFile, encoded, 0o644) // a read-only store never fails the lookup
				}
			}
		}
		return result, nil
	}
}

// osvVuln is the subset of an OSV vulnerability record we read.
type osvVuln struct {
	ID       string   `json:"id"`
	Summary  string   `json:"summary"`
	Details  string   `json:"details"`
	Aliases  []string `json:"aliases"`
	Severity []struct {
		Type  string `json:"type"`
		Score string `json:"score"`
	} `json:"severity"`
	DatabaseSpecific struct {
		Severity string `json:"severity"`
	} `json:"database_specific"`
	Affected []struct {
		Package struct {
			Name string `json:"name"`
		} `json:"package"`
		Ranges []struct {
			Events []struct {
				Introduced string `json:"introduced"`
				Fixed      string `json:"fixed"`
			} `json:"events"`
		} `json:"ranges"`
	} `json:"affected"`
}

// osvSeverity maps GHSA severity -> our bucket; falls back to the CVSS base
// score, else unknown.
func osvSeverity(vuln osvVuln) Severity {
	switch strings.ToLower(vuln.DatabaseSpecific.Severity) {
	case "critical":
		return SeverityCritical
	case "high":
		return SeverityHigh
	case "moderate":
		return SeverityModerate
	case "low":
		return SeverityLow
	}
	var score float64
	have := false
	for _, s := range vuln.Severity {
		if strings.HasPrefix(s.Type, "CVSS") {
			if n, err := strconv.ParseFloat(s.Score, 64); err == nil {
				score, have = n, true
			}
			break
		}
	}
	if !have {
		return SeverityUnknown
	}
	switch {
	case score >= 9:
		return SeverityCritical
	case score >= 7:
		return SeverityHigh
	case score >= 4:
		return SeverityModerate
	default:
		return SeverityLow
	}
}

func cveAliases(vuln osvVuln) []string {
	var cves []string
	for _, a := range vuln.Aliases {
		if strings.HasPrefix(a, "CVE-") {
			cves = append(cves, a)
		}
	}
	return cves
}

func fixedVersionsOf(vuln osvVuln, c packages.Coordinates) []string {
	name := string(c.GroupID) + ":" + string(c.ArtifactID)
	seen := map[string]struct{}{}
	var fixed []string
	for _, affected := range vuln.Affected {
		if affected.Package.Name != "" && affected.Package.Name != name {
			continue
		}
		for _, r := range affected.Ranges {
			for _, e := range r.Events {
				if e.Fixed != "" {
					if _, ok := seen[e.Fixed]; !ok {
						seen[e.Fixed] = struct{}{}
						fixed = append(fixed, e.Fixed)
					}
				}
			}
		}
	}
	return fixed
}

func toAdvisory(vuln osvVuln, c packages.Coordinates) Advisory {
	summary := vuln.Summary
	if summary == "" {
		if line, _, _ := strings.Cut(vuln.Details, "\n"); line != "" {
			summary = line
		} else {
			summary = "(no summary)"
		}
	}
	return Advisory{
		ID:            AdvisoryID(vuln.ID),
		Aliases:       cveAliases(vuln),
		Summary:       summary,
		Severity:      osvSeverity(vuln),
		FixedVersions: fixedVersionsOf(vuln, c),
		URL:           "https://osv.dev/vulnerability/" + vuln.ID,
	}
}

// OsvSource implements AuditSource over OSV.dev.
type OsvSource struct {
	fetchJSON FetchJSON
}

// NewOsvSource builds an OSV source; fetchJSON defaults to a live HTTP fetcher.
func NewOsvSource(fetchJSON FetchJSON) *OsvSource {
	if fetchJSON == nil {
		fetchJSON = defaultFetchJSON
	}
	return &OsvSource{fetchJSON: fetchJSON}
}

func (s *OsvSource) Name() string { return osvAPI }

// FindVulnerabilities looks up the advisories affecting each coordinate.
func (s *OsvSource) FindVulnerabilities(coordinates []packages.Coordinates) (map[packages.CoordinateString][]Advisory, error) {
	result := map[packages.CoordinateString][]Advisory{}
	if len(coordinates) == 0 {
		return result, nil
	}

	// 1. batched id lookup: results[i] lines up with the chunk's coordinates[i]
	idsByCoordinate := map[packages.CoordinateString][]string{}
	var wantedIDs []string
	wanted := map[string]struct{}{}
	type pageQuery struct {
		coordinates packages.Coordinates
		token       string
	}
	for start := 0; start < len(coordinates); start += osvBatch {
		end := min(start+osvBatch, len(coordinates))
		chunk := coordinates[start:end]
		// Follow per-result pagination (next_page_token): a version with more
		// vulns than one page would otherwise be silently truncated.
		pending := make([]pageQuery, len(chunk))
		for i, c := range chunk {
			pending[i] = pageQuery{coordinates: c}
		}
		for len(pending) > 0 {
			queries := make([]map[string]any, len(pending))
			for i, p := range pending {
				q := map[string]any{
					"version": string(p.coordinates.Version),
					"package": map[string]string{"name": string(p.coordinates.GroupID) + ":" + string(p.coordinates.ArtifactID), "ecosystem": "Maven"},
				}
				if p.token != "" {
					q["page_token"] = p.token
				}
				queries[i] = q
			}
			raw, err := s.fetchJSON(osvAPI+"/v1/querybatch", map[string]any{"queries": queries})
			if err != nil {
				return nil, err
			}
			var response struct {
				Results []struct {
					Vulns []struct {
						ID string `json:"id"`
					} `json:"vulns"`
					NextPageToken string `json:"next_page_token"`
				} `json:"results"`
			}
			if err := json.Unmarshal(raw, &response); err != nil {
				return nil, err
			}
			var next []pageQuery
			for i, entry := range response.Results {
				if i >= len(pending) {
					continue
				}
				page := pending[i]
				if len(entry.Vulns) > 0 {
					key := page.coordinates.String()
					for _, v := range entry.Vulns {
						idsByCoordinate[key] = append(idsByCoordinate[key], v.ID)
						if _, ok := wanted[v.ID]; !ok {
							wanted[v.ID] = struct{}{}
							wantedIDs = append(wantedIDs, v.ID)
						}
					}
				}
				if entry.NextPageToken != "" {
					next = append(next, pageQuery{coordinates: page.coordinates, token: entry.NextPageToken})
				}
			}
			pending = next
		}
	}

	// 2. hydrate each distinct vuln once (sequential; cached ones resolve from
	// disk). Issue #18 defers concurrency.
	vulns := map[string]osvVuln{}
	for _, id := range wantedIDs {
		raw, err := s.fetchJSON(osvAPI+"/v1/vulns/"+id, nil)
		if err != nil {
			return nil, err
		}
		var v osvVuln
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		vulns[id] = v
	}

	// 3. attach advisories to their coordinates
	for _, c := range coordinates {
		ids := idsByCoordinate[c.String()]
		if len(ids) == 0 {
			continue
		}
		advisories := make([]Advisory, 0, len(ids))
		for _, id := range ids {
			advisories = append(advisories, toAdvisory(vulns[id], c))
		}
		result[c.String()] = advisories
	}
	return result, nil
}
