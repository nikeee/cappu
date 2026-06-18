package packages

import "strings"

// Version-spec matching for `cappu add`: a spec is an exact version or a
// leading-segment prefix ("2" matches 2.10.1 and 2-rc1, "2.1" matches 2.1.3 but
// not 2.10.1). No ordering model beyond publish order: maven-metadata.xml lists
// versions oldest first, and "latest" means last in that list. Port of
// src/packages/versions.ts.

// MatchesVersionSpec reports whether version is spec itself or refines it
// segment-wise.
func MatchesVersionSpec(spec, version string) bool {
	return version == spec ||
		strings.HasPrefix(version, spec+".") ||
		strings.HasPrefix(version, spec+"-")
}

// MatchingVersions returns the matching versions, newest (per publish order)
// first. An empty spec matches all of them.
func MatchingVersions(versions []string, spec string) []string {
	matching := make([]string, 0, len(versions))
	for _, v := range versions {
		if spec == "" || MatchesVersionSpec(spec, v) {
			matching = append(matching, v)
		}
	}
	// reverse in place: newest (last published) first
	for i, j := 0, len(matching)-1; i < j; i, j = i+1, j-1 {
		matching[i], matching[j] = matching[j], matching[i]
	}
	return matching
}
