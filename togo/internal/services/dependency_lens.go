package services

// Code lenses over the dependencies section of cappu.json: when a newer version
// of an entry is published, a lens above the line shows it. The parsing side is
// line-based and pure: in cappu.json, a dependency is the only kind of KEY
// containing a colon ("group:artifact"). Port of src/services/dependencyLens.ts.

import (
	"regexp"
	"strings"
)

// DependencyEntry is one `"group:artifact": "version"` line, or the one-line
// object form `"group:artifact": { "version": "...", ... }` (Version is ""
// when the object has none: a platform manages it).
type DependencyEntry struct {
	GroupID    string
	ArtifactID string
	Version    string
	Line       int // 0-based
	StartChar  int
	EndChar    int
}

var (
	dependencyEntryRE = regexp.MustCompile(`^(\s*)("([^"\s:]+:[^"\s:]+)"\s*:\s*(?:"([^"]*)"|(\{[^}]*\})))`)
	objectVersionRE   = regexp.MustCompile(`"version"\s*:\s*"([^"]*)"`)
)

// FindDependencyEntries parses the dependency lines of a cappu.json text.
func FindDependencyEntries(text string) []DependencyEntry {
	var entries []DependencyEntry
	for line, lineText := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimLeft(lineText, " \t"), "//") {
			continue // commented-out entry
		}
		match := dependencyEntryRE.FindStringSubmatch(lineText)
		if match == nil {
			continue
		}
		coords := strings.SplitN(match[3], ":", 2)
		groupID, artifactID := coords[0], ""
		if len(coords) > 1 {
			artifactID = coords[1]
		}
		version := match[4]
		if object := match[5]; object != "" {
			if v := objectVersionRE.FindStringSubmatch(object); v != nil {
				version = v[1]
			}
		}
		entries = append(entries, DependencyEntry{
			GroupID:    groupID,
			ArtifactID: artifactID,
			Version:    version,
			Line:       line,
			StartChar:  len(match[1]),
			EndChar:    len(match[1]) + len(match[2]),
		})
	}
	return entries
}

// LatestVersionLookup returns the newest published version of group:artifact,
// and whether it is known.
type LatestVersionLookup func(groupID, artifactID string) (string, bool)

// DependencyLens is one lens over a dependency entry.
type DependencyLens struct {
	Entry DependencyEntry
	Title string // e.g. "newer version: 2.14.0"
}

// DependencyLenses returns a lens per dependency whose newest published version
// differs from the entry. An entry written as "" takes its version from a
// platform and gets no lens of its own.
func DependencyLenses(text string, lookup LatestVersionLookup) []DependencyLens {
	var lenses []DependencyLens
	for _, entry := range FindDependencyEntries(text) {
		if entry.Version == "" {
			continue
		}
		latest, ok := lookup(entry.GroupID, entry.ArtifactID)
		if ok && latest != entry.Version {
			lenses = append(lenses, DependencyLens{Entry: entry, Title: "newer version: " + latest})
		}
	}
	return lenses
}
