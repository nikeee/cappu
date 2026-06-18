// Package semver does npm-style major/minor/patch bumping for `cappu version`.
// Pure; the CLI writes the result back to cappu.json and tags. Port of
// src/version.ts.
package semver

import (
	"fmt"
	"regexp"
	"strconv"
)

// ReleaseType is one of major/minor/patch.
type ReleaseType string

const (
	Major ReleaseType = "major"
	Minor ReleaseType = "minor"
	Patch ReleaseType = "patch"
)

// ReleaseTypes are the accepted release kinds, in CLI order.
var ReleaseTypes = []ReleaseType{Major, Minor, Patch}

// IsReleaseType reports whether s names a release kind.
func IsReleaseType(s string) bool {
	for _, r := range ReleaseTypes {
		if string(r) == s {
			return true
		}
	}
	return false
}

var corePrefix = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`)

// Bump returns the next version after a major/minor/patch release. The core
// MAJOR.MINOR.PATCH is bumped and any pre-release / build metadata is dropped (a
// release is a clean version), matching `npm version`.
func Bump(version string, release ReleaseType) (string, error) {
	m := corePrefix.FindStringSubmatch(version)
	if m == nil {
		return "", fmt.Errorf("not a semver version: %s", version)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	switch release {
	case Major:
		major, minor, patch = major+1, 0, 0
	case Minor:
		minor, patch = minor+1, 0
	default:
		patch++
	}
	return fmt.Sprintf("%d.%d.%d", major, minor, patch), nil
}
