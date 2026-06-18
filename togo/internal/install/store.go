// Package install resolves the cappu.json dependencies and downloads the jars,
// pinning the outcome in cappu-lock.json. Print-free: the CLI renders the
// result. Sequential by design (issue #18 defers concurrency). Port of
// src/install.ts.
package install

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nikeee/cappu/internal/cache"
	"github.com/nikeee/cappu/internal/packages"
)

// packageStoreDir is the per-user jar cache: XDG_CACHE_HOME/cappu/packages, or
// $CAPPU_PACKAGE_STORE when set (tests, CI).
func packageStoreDir() string {
	return cache.Dir("packages", os.Getenv("CAPPU_PACKAGE_STORE"))
}

// storeSegment is the one conservative charset for every store path segment;
// anything else (separators, "..", empty) bypasses the store rather than
// risking a write outside it.
var storeSegment = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*$`)

// storePathFor is the store path for exact coordinates, or ok=false for unsafe
// segments. The layout is maven2's (group segments as directories), which keeps
// "a.b:c" and "a.b.c:d" apart.
func storePathFor(c packages.Coordinates) (string, bool) {
	segments := append(strings.Split(string(c.GroupID), "."), string(c.ArtifactID), string(c.Version))
	for _, seg := range segments {
		if !storeSegment.MatchString(seg) {
			return "", false
		}
	}
	parts := append([]string{packageStoreDir()}, segments...)
	parts = append(parts, string(c.ArtifactID)+"-"+string(c.Version)+".jar")
	return filepath.Join(parts...), true
}
