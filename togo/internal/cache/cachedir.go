// Package cache locates and clears cappu's per-user download cache (packages,
// JDKs, resolved POM metadata). It follows XDG_CACHE_HOME (~/.cache/cappu); the
// per-domain env overrides (CAPPU_PACKAGE_STORE, CAPPU_JDK_STORE) win for tests
// and CI. Port of src/cacheDir.ts.
package cache

import (
	"os"
	"path/filepath"
)

// Root is the cappu cache root: $XDG_CACHE_HOME/cappu (or ~/.cache/cappu).
func Root() string {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		if home, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(home, ".cache")
		}
	}
	return filepath.Join(base, "cappu")
}

// Dir returns the cache subdir, or envOverride when it is set.
func Dir(subdir, envOverride string) string {
	if envOverride != "" {
		return envOverride
	}
	return filepath.Join(Root(), subdir)
}

// Clean removes the global download cache and returns the directories actually
// removed. The per-domain env overrides, when set, are cleaned too since they
// may point outside the cache root.
func Clean() []string {
	targets := []string{Root(), os.Getenv("CAPPU_PACKAGE_STORE"), os.Getenv("CAPPU_JDK_STORE")}
	seen := make(map[string]struct{})
	var removed []string
	for _, dir := range targets {
		if dir == "" {
			continue
		}
		if _, dup := seen[dir]; dup {
			continue
		}
		seen[dir] = struct{}{}
		if _, err := os.Stat(dir); err == nil {
			if os.RemoveAll(dir) == nil {
				removed = append(removed, dir)
			}
		}
	}
	return removed
}
