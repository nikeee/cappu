package lspserver

// cappu.json dependency code lenses: a "newer version: X" lens above each
// dependency whose newest published version differs from the pinned one. The
// newest-version lookups go to the network, cached briefly per group:artifact.
// Port of the dependency-lens half of src/services/server.ts.

import (
	"strconv"
	"time"

	"github.com/nikeee/cappu/internal/compiler"
	"github.com/nikeee/cappu/internal/lsp"
	"github.com/nikeee/cappu/internal/packages"
	"github.com/nikeee/cappu/internal/services"
)

func itoa(n int) string { return strconv.Itoa(n) }

func (s *Server) cachedLatestVersion(groupID, artifactID string) (string, bool) {
	key := groupID + ":" + artifactID
	if cached, ok := s.latestCache[key]; ok && time.Since(cached.at) < latestTTL {
		return cached.value, cached.ok
	}
	if s.packageSources == nil {
		for _, url := range s.packageSourceURL {
			s.packageSources = append(s.packageSources, packages.NewMavenRepositorySource(url, ""))
		}
	}
	value, err := packages.LatestVersion(groupID, artifactID, s.packageSources)
	entry := latestEntry{value: value, ok: err == nil && value != "", at: time.Now()}
	s.latestCache[key] = entry
	return entry.value, entry.ok
}

func (s *Server) dependencyCodeLenses(uri compiler.URI) []lsp.CodeLens {
	text, ok := s.docs[uri]
	if !ok {
		return []lsp.CodeLens{}
	}
	out := []lsp.CodeLens{}
	for _, l := range services.DependencyLenses(text, s.cachedLatestVersion) {
		out = append(out, lsp.CodeLens{
			Range: lsp.Range{
				Start: lsp.Position{Line: l.Entry.Line, Character: l.Entry.StartChar},
				End:   lsp.Position{Line: l.Entry.Line, Character: l.Entry.EndChar},
			},
			Command: &lsp.Command{Title: l.Title, Command: ""},
		})
	}
	return out
}
