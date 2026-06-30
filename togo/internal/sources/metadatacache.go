// On-disk metadata cache: fetched metadata is cached in the package store so
// re-resolves and repeated `cappu add` runs avoid the network.
//   - ListVersions (maven-metadata.xml): a short TTL, because "latest" moves.
//   - GetMetadata (a released version's effective POM): cached forever, since a
//     published POM is immutable; this is what makes lockfile-less resolves fast.
//
// The raw POM is persisted next to its metadata.json with its SHA-256 recorded,
// so `cappu cache verify` can check the cached POM on disk. Only successful
// answers are cached; a read-only or missing store silently degrades to live
// fetches. Port of withMetadataCache in src/install.ts.

package sources

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/nikeee/cappu/internal/cache"
	"github.com/nikeee/cappu/internal/packages"
)

const versionCacheTTL = time.Hour

// metadataCacheVersion is bumped when PackageMetadata's shape grows so entries
// from an older cappu are ignored and re-fetched. Matches METADATA_CACHE_VERSION
// in src/install.ts.
const metadataCacheVersion = 2

// metaSegment is the conservative charset for a store path segment; anything
// else bypasses the cache rather than risking a write outside it.
var metaSegment = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*$`)

var sourceUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func packageStoreDir() string {
	return cache.Dir("packages", os.Getenv("CAPPU_PACKAGE_STORE"))
}

// metadataCachePath is <store>/_metadata/<source>/<segments...>/<file>, or ""
// when a segment is not store-safe (then caching is skipped).
func metadataCachePath(sourceName string, segments []string, file string) string {
	for _, s := range segments {
		if !metaSegment.MatchString(s) {
			return ""
		}
	}
	sourceDir := sourceUnsafe.ReplaceAllString(sourceName, "_")
	parts := append([]string{packageStoreDir(), "_metadata", sourceDir}, segments...)
	parts = append(parts, file)
	return filepath.Join(parts...)
}

type versionsEntry struct {
	FetchedAt int64    `json:"fetchedAt"`
	Versions  []string `json:"versions"`
}

type metadataEntry struct {
	V         int                       `json:"v"`
	Metadata  *packages.PackageMetadata `json:"metadata,omitempty"`
	PomSha256 string                    `json:"pomSha256,omitempty"`
}

// metadataCache wraps a source with the on-disk metadata cache.
type metadataCache struct {
	inner packages.PackageSource
}

// WithMetadataCache layers the on-disk metadata cache over a source.
func WithMetadataCache(inner packages.PackageSource) packages.PackageSource {
	return &metadataCache{inner: inner}
}

func (m *metadataCache) Name() packages.SourceName { return m.inner.Name() }
func (m *metadataCache) Search(q string) ([]packages.SearchHit, error) {
	return m.inner.Search(q)
}
func (m *metadataCache) GetArtifact(c packages.Coordinates) ([]byte, error) {
	return m.inner.GetArtifact(c)
}
func (m *metadataCache) GetPom(c packages.Coordinates) ([]byte, error) {
	return m.inner.GetPom(c)
}

func (m *metadataCache) ListVersions(groupID, artifactID string) ([]string, error) {
	segments := append(strings.Split(groupID, "."), artifactID)
	cacheFile := metadataCachePath(string(m.inner.Name()), segments, "versions.json")
	if cacheFile != "" {
		if data, err := os.ReadFile(cacheFile); err == nil {
			var e versionsEntry
			if json.Unmarshal(data, &e) == nil && time.Since(time.UnixMilli(e.FetchedAt)) < versionCacheTTL {
				return e.Versions, nil
			}
		}
	}
	versions, err := m.inner.ListVersions(groupID, artifactID)
	if err != nil {
		return nil, err
	}
	if cacheFile != "" && len(versions) > 0 {
		if os.MkdirAll(filepath.Dir(cacheFile), 0o755) == nil {
			if b, err := json.Marshal(versionsEntry{FetchedAt: time.Now().UnixMilli(), Versions: versions}); err == nil {
				_ = os.WriteFile(cacheFile, b, 0o644) // a read-only store never fails the lookup
			}
		}
	}
	return versions, nil
}

func (m *metadataCache) GetMetadata(c packages.Coordinates) (*packages.PackageMetadata, error) {
	segments := append(strings.Split(string(c.GroupID), "."), string(c.ArtifactID), string(c.Version))
	cacheFile := metadataCachePath(string(m.inner.Name()), segments, "metadata.json")
	if cacheFile != "" {
		if data, err := os.ReadFile(cacheFile); err == nil {
			var e metadataEntry
			// older/unknown schema: re-fetch and rewrite
			if json.Unmarshal(data, &e) == nil && e.V == metadataCacheVersion && e.Metadata != nil {
				return e.Metadata, nil
			}
		}
	}
	metadata, err := m.inner.GetMetadata(c)
	if err != nil {
		return nil, err
	}
	if cacheFile != "" && metadata != nil {
		if os.MkdirAll(filepath.Dir(cacheFile), 0o755) == nil {
			// Persist the raw POM next to its metadata and record its SHA-256, so a
			// `cappu cache verify` can check the cached POM on disk.
			pomSha := ""
			if pom, perr := m.inner.GetPom(c); perr == nil && pom != nil {
				sum := sha256.Sum256(pom)
				pomSha = hex.EncodeToString(sum[:])
				pomFile := filepath.Join(filepath.Dir(cacheFile), string(c.ArtifactID)+"-"+string(c.Version)+".pom")
				_ = os.WriteFile(pomFile, pom, 0o644)
			}
			if b, err := json.Marshal(metadataEntry{V: metadataCacheVersion, Metadata: metadata, PomSha256: pomSha}); err == nil {
				_ = os.WriteFile(cacheFile, b, 0o644) // a read-only store never fails the lookup
			}
		}
	}
	return metadata, nil
}
