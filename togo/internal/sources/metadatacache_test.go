package sources

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nikeee/cappu/internal/packages"
)

// fakeMetaSource serves a fixed metadata/pom/version list and counts fetches.
type fakeMetaSource struct {
	name      string
	versions  []string
	meta      *packages.PackageMetadata
	pom       []byte
	vCalls    int
	metaCalls int
}

func (s *fakeMetaSource) Name() packages.SourceName                   { return packages.SourceName(s.name) }
func (s *fakeMetaSource) Search(string) ([]packages.SearchHit, error) { return nil, nil }
func (s *fakeMetaSource) ListVersions(string, string) ([]string, error) {
	s.vCalls++
	return s.versions, nil
}
func (s *fakeMetaSource) GetMetadata(packages.Coordinates) (*packages.PackageMetadata, error) {
	s.metaCalls++
	return s.meta, nil
}
func (s *fakeMetaSource) GetArtifact(packages.Coordinates) ([]byte, error) { return nil, nil }
func (s *fakeMetaSource) GetPom(packages.Coordinates) ([]byte, error)      { return s.pom, nil }

func TestMetadataCacheListVersionsTTL(t *testing.T) {
	t.Setenv("CAPPU_PACKAGE_STORE", filepath.Join(t.TempDir(), "store"))
	inner := &fakeMetaSource{name: "https://repo.test/m2", versions: []string{"1.0", "1.1"}}
	src := WithMetadataCache(inner)
	if v, _ := src.ListVersions("org.example", "thing"); len(v) != 2 {
		t.Fatalf("got %v", v)
	}
	if v, _ := src.ListVersions("org.example", "thing"); len(v) != 2 {
		t.Fatalf("got %v", v)
	}
	if inner.vCalls != 1 {
		t.Errorf("listVersions fetched %d times, want 1 (second answer from cache)", inner.vCalls)
	}
}

func TestMetadataCachePersistsPomAndHash(t *testing.T) {
	store := filepath.Join(t.TempDir(), "store")
	t.Setenv("CAPPU_PACKAGE_STORE", store)
	c := packages.NewCoordinates("org.x", "y", "1.0")
	inner := &fakeMetaSource{
		name: "https://repo.test/m2",
		meta: &packages.PackageMetadata{Coordinates: c},
		pom:  []byte("<project>raw pom</project>"),
	}
	src := WithMetadataCache(inner)
	if _, err := src.GetMetadata(c); err != nil {
		t.Fatal(err)
	}
	if _, err := src.GetMetadata(c); err != nil {
		t.Fatal(err)
	}
	if inner.metaCalls != 1 {
		t.Errorf("getMetadata fetched %d times, want 1 (second from cache)", inner.metaCalls)
	}

	dir := filepath.Join(store, "_metadata", "https_repo.test_m2", "org", "x", "y", "1.0")
	// the raw POM is persisted next to its metadata
	if got, err := os.ReadFile(filepath.Join(dir, "y-1.0.pom")); err != nil || string(got) != string(inner.pom) {
		t.Fatalf("pom = %q err=%v", got, err)
	}
	// and metadata.json records its hash
	data, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var e struct {
		PomSha256 string `json:"pomSha256"`
	}
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(inner.pom)
	if want := hex.EncodeToString(sum[:]); e.PomSha256 != want {
		t.Errorf("pomSha256 = %q, want %q", e.PomSha256, want)
	}
}
