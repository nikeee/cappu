package packages

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"
)

// MavenRepositorySource is a maven2 repository, optionally with a solr index
// service for search (search.maven.org style; plain repositories have none).
// Port of MavenRepositorySource in src/packages/maven.ts - only the search
// path is implemented for milestone 1.
type MavenRepositorySource struct {
	baseURL   string
	searchURL string // empty when the repository has no index service
	client    *http.Client
}

// NewMavenRepositorySource builds a source for baseURL. searchURL is the solr
// index endpoint, or "" for a repository without one.
func NewMavenRepositorySource(baseURL, searchURL string) *MavenRepositorySource {
	return &MavenRepositorySource{
		baseURL:   baseURL,
		searchURL: searchURL,
		client:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Name is the repository url (a stable display name).
func (s *MavenRepositorySource) Name() string { return s.baseURL }

// searchResponse is the subset of the solr answer we read.
type searchResponse struct {
	Response struct {
		Docs []struct {
			G             string `json:"g"`
			A             string `json:"a"`
			LatestVersion string `json:"latestVersion"`
		} `json:"docs"`
	} `json:"response"`
}

// Search runs a free-text query via the index service; it returns nil without
// one (or on any error - a broken index answer must not fail the command).
func (s *MavenRepositorySource) Search(query string) ([]Coordinates, error) {
	if s.searchURL == "" {
		return nil, nil
	}
	u, err := url.Parse(s.searchURL)
	if err != nil {
		return nil, nil
	}
	u.RawQuery = url.Values{"q": {query}, "rows": {"20"}, "wt": {"json"}}.Encode()

	text, err := s.fetchText(u.String())
	if err != nil || text == nil {
		return nil, nil
	}
	var doc searchResponse
	if err := json.Unmarshal(text, &doc); err != nil {
		return nil, nil
	}
	var hits []Coordinates
	for _, d := range doc.Response.Docs {
		if d.G != "" && d.A != "" && d.LatestVersion != "" {
			hits = append(hits, NewCoordinates(d.G, d.A, d.LatestVersion))
		}
	}
	return hits, nil
}

// fetchText GETs url and returns the body, or nil on a non-2xx or transport
// error (the caller degrades to an empty result, like the TS fetchText).
func (s *MavenRepositorySource) fetchText(url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil
	}
	return io.ReadAll(resp.Body)
}
