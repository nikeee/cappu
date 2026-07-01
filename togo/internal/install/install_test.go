package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/lockfile"
	"github.com/nikeee/cappu/internal/packages"
)

// fakeSource serves metadata, jar bytes and version lists from in-memory maps.
type fakeSource struct {
	name     string
	meta     map[packages.CoordinateString]packages.PackageMetadata
	jars     map[packages.CoordinateString][]byte
	poms     map[packages.CoordinateString][]byte
	versions map[string][]string // "group:artifact" -> versions, oldest first
}

func (s *fakeSource) Name() packages.SourceName                   { return packages.SourceName(s.name) }
func (s *fakeSource) Search(string) ([]packages.SearchHit, error) { return nil, nil }

func (s *fakeSource) ListVersions(groupID, artifactID string) ([]string, error) {
	return s.versions[groupID+":"+artifactID], nil
}

func (s *fakeSource) GetMetadata(c packages.Coordinates) (*packages.PackageMetadata, error) {
	if m, ok := s.meta[c.String()]; ok {
		return &m, nil
	}
	return nil, nil
}

func (s *fakeSource) GetArtifact(c packages.Coordinates) ([]byte, error) {
	return s.jars[c.String()], nil
}

func (s *fakeSource) GetPom(c packages.Coordinates) ([]byte, error) {
	return s.poms[c.String()], nil
}

func coord(spec string) packages.Coordinates {
	p := strings.Split(spec, ":")
	return packages.NewCoordinates(p[0], p[1], p[2])
}

func meta(spec string, deps ...string) packages.PackageMetadata {
	decls := make([]packages.DependencyDeclaration, 0, len(deps))
	for _, d := range deps {
		decls = append(decls, packages.DependencyDeclaration{Coordinates: coord(d)})
	}
	return packages.PackageMetadata{Coordinates: coord(spec), Dependencies: decls}
}

// project writes a cappu.json with the given implementation deps and isolates
// the package store under a temp dir.
func project(t *testing.T, body string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CAPPU_PACKAGE_STORE", filepath.Join(t.TempDir(), "store"))
	if err := os.WriteFile(filepath.Join(dir, config.DefaultConfigName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func source() *fakeSource {
	return &fakeSource{
		name: "test",
		meta: map[packages.CoordinateString]packages.PackageMetadata{
			"org.a:a:1": meta("org.a:a:1", "org.b:b:1"),
			"org.b:b:1": meta("org.b:b:1"),
		},
		jars: map[packages.CoordinateString][]byte{
			"org.a:a:1": []byte("jar-a"),
			"org.b:b:1": []byte("jar-b"),
		},
	}
}

func TestInstallResolvesDownloadsAndWritesLock(t *testing.T) {
	cfg := project(t, `{"dependencies":{"implementation":{"org.a:a":"1"}}}`)
	res, err := Dependencies(cfg, []packages.PackageSource{source()}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.FromLock {
		t.Error("first install should not be from a lock")
	}
	if len(res.InstalledByCategory.Compile) != 2 {
		t.Errorf("installed %d compile jars, want 2", len(res.InstalledByCategory.Compile))
	}
	for _, name := range []string{"a-1.jar", "b-1.jar"} {
		if _, err := os.Stat(filepath.Join(cfg.ResolvePath(config.DefaultClassPath), name)); err != nil {
			t.Errorf("expected %s installed: %v", name, err)
		}
	}
	lock := lockfile.Read(cfg)
	if lock == nil || len(lock.Packages) != 2 {
		t.Fatalf("lockfile not written with 2 packages: %+v", lock)
	}
	if lock.Packages[0].Sha256 != lockfile.Sha256Of([]byte("jar-a")) {
		t.Errorf("locked sha mismatch for a")
	}
	// The set is sorted by coordinate so the lock is deterministic across runs.
	if got := []string{string(lock.Packages[0].Coords().String()), string(lock.Packages[1].Coords().String())}; got[0] != "org.a:a:1" || got[1] != "org.b:b:1" {
		t.Errorf("lock packages not sorted by coordinate: %v", got)
	}
}

func TestDownloadedJarGetsSha256Sidecar(t *testing.T) {
	cfg := project(t, `{"dependencies":{"implementation":{"org.a:a":"1"}}}`)
	if _, err := Dependencies(cfg, []packages.PackageSource{source()}, Options{}); err != nil {
		t.Fatal(err)
	}
	stored, ok := StorePathFor(coord("org.a:a:1"))
	if !ok {
		t.Fatal("store path unexpectedly unsafe")
	}
	got, err := os.ReadFile(stored + ".sha256")
	if err != nil {
		t.Fatalf("expected sidecar: %v", err)
	}
	if string(got) != string(lockfile.Sha256Of([]byte("jar-a"))) {
		t.Errorf("sidecar = %q, want jar hash", got)
	}
}

func TestCheckLocked(t *testing.T) {
	cfg := project(t, `{"dependencies":{"implementation":{"org.a:a":"1"}}}`)
	dir := cfg.BaseDir
	reload := func() *config.Config {
		c, err := config.Load("", dir)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	// No lock yet, but a dependency is declared -> missing.
	if ok, reason := CheckLocked(cfg); ok || !strings.Contains(reason, "no cappu-lock.json") {
		t.Errorf("missing lock: ok=%v reason=%q", ok, reason)
	}
	// Resolve to write the lock; now it matches -> ok.
	if _, err := Dependencies(reload(), []packages.PackageSource{source()}, Options{}); err != nil {
		t.Fatal(err)
	}
	if ok, reason := CheckLocked(reload()); !ok {
		t.Errorf("matching lock should be ok, got reason=%q", reason)
	}
	// Change the declared dependency: the lock is now stale.
	if err := os.WriteFile(filepath.Join(dir, config.DefaultConfigName),
		[]byte(`{"dependencies":{"implementation":{"org.a:a":"2"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, reason := CheckLocked(reload()); ok || !strings.Contains(reason, "disagree") {
		t.Errorf("stale lock: ok=%v reason=%q", ok, reason)
	}
	// A project with no declared dependencies and no lock is fine.
	bare := project(t, `{}`)
	if ok, _ := CheckLocked(bare); !ok {
		t.Error("bare project with no deps should be ok")
	}
}

func TestInstallFromLockReused(t *testing.T) {
	cfg := project(t, `{"dependencies":{"implementation":{"org.a:a":"1"}}}`)
	if _, err := Dependencies(cfg, []packages.PackageSource{source()}, Options{}); err != nil {
		t.Fatal(err)
	}
	res, err := Dependencies(cfg, []packages.PackageSource{source()}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.FromLock {
		t.Error("second install should reuse the lock")
	}
	if len(res.IntegrityFailures) != 0 {
		t.Errorf("unexpected integrity failures: %v", res.IntegrityFailures)
	}
}

func TestInstallIntegrityFailure(t *testing.T) {
	cfg := project(t, `{"dependencies":{"implementation":{"org.a:a":"1"}}}`)
	if _, err := Dependencies(cfg, []packages.PackageSource{source()}, Options{}); err != nil {
		t.Fatal(err)
	}
	// Corrupt the locked sha for one package, then reinstall: the download no
	// longer matches the lock.
	lock := lockfile.Read(cfg)
	lock.Packages[0].Sha256 = "deadbeef"
	if err := lockfile.Write(cfg, lock); err != nil {
		t.Fatal(err)
	}
	res, err := Dependencies(cfg, []packages.PackageSource{source()}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.IntegrityFailures) != 1 {
		t.Errorf("expected 1 integrity failure, got %v", res.IntegrityFailures)
	}
	// The poisoned store entry and its hash sidecar are evicted so a later good
	// install can re-download.
	stored, _ := StorePathFor(lock.Packages[0].Coords())
	if _, err := os.Stat(stored); !os.IsNotExist(err) {
		t.Errorf("poisoned jar not evicted from store: %v", err)
	}
	if _, err := os.Stat(stored + ".sha256"); !os.IsNotExist(err) {
		t.Errorf("poisoned jar sidecar not evicted: %v", err)
	}
}

func TestInstallMissingDependencyNoLock(t *testing.T) {
	cfg := project(t, `{"dependencies":{"implementation":{"org.gone:gone":"9"}}}`)
	res, err := Dependencies(cfg, []packages.PackageSource{source()}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Resolution.Missing) != 1 {
		t.Errorf("expected 1 missing, got %v", res.Resolution.Missing)
	}
	// resolution incomplete -> no lockfile written
	if lockfile.Read(cfg) != nil {
		t.Error("lockfile should not be written when resolution is incomplete")
	}
}

func TestStorePathForClassifier(t *testing.T) {
	plain := packages.NewCoordinates("org.jacoco", "org.jacoco.agent", "0.8.12")
	runtime := plain.WithClassifier("runtime")
	pp, _ := StorePathFor(plain)
	rp, _ := StorePathFor(runtime)
	if !strings.HasSuffix(pp, "org.jacoco.agent-0.8.12.jar") {
		t.Fatalf("plain: %s", pp)
	}
	if !strings.HasSuffix(rp, "org.jacoco.agent-0.8.12-runtime.jar") {
		t.Fatalf("runtime: %s", rp)
	}
	if pp == rp {
		t.Fatal("classified path collides with plain")
	}
}
