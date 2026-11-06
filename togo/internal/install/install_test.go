package install

import (
	"os"
	"path/filepath"
	"reflect"
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
	// managed is what each BOM manages, for platform tests.
	managed map[packages.CoordinateString]map[packages.PackageKey]string
}

func (s *fakeSource) GetManaged(c packages.Coordinates) (map[packages.PackageKey]string, error) {
	return s.managed[c.String()], nil
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

func TestInstallSkipsPomOnlyAndNamesClassifiedJars(t *testing.T) {
	cfg := project(t, `{"dependencies":{"implementation":{"org.a:a":"1"}}}`)
	native := coord("io.n:n:1").WithClassifier("linux-x86_64")
	bom := coord("org.bom:bom:1")
	bom.Extension = "pom"
	src := &fakeSource{
		name: "test",
		meta: map[packages.CoordinateString]packages.PackageMetadata{
			"org.a:a:1": {Coordinates: coord("org.a:a:1"), Dependencies: []packages.DependencyDeclaration{
				{Coordinates: bom},
				{Coordinates: native},
			}},
			"org.bom:bom:1": meta("org.bom:bom:1", "org.b:b:1"),
			"org.b:b:1":     meta("org.b:b:1"),
			"io.n:n:1":      meta("io.n:n:1"),
		},
		jars: map[packages.CoordinateString][]byte{
			"org.a:a:1": []byte("jar-a"),
			"org.b:b:1": []byte("jar-b"),
			"io.n:n:1":  []byte("jar-n"), // the fake serves any classifier of a GAV
		},
	}
	res, err := Dependencies(cfg, []packages.PackageSource{src}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.NoArtifact) != 0 {
		t.Errorf("a pom-only dependency must not be reported as a missing jar: %v", res.NoArtifact)
	}
	for _, name := range []string{"a-1.jar", "b-1.jar", "n-1-linux-x86_64.jar"} {
		if _, err := os.Stat(filepath.Join(cfg.ResolvePath(config.DefaultClassPath), name)); err != nil {
			t.Errorf("expected %s installed: %v", name, err)
		}
	}
	lock := lockfile.Read(cfg)
	if lock == nil {
		t.Fatal("lock not written")
	}
	var locked []string
	for _, p := range lock.Packages {
		locked = append(locked, p.Coords().Label())
	}
	want := []string{"io.n:n:1:linux-x86_64", "org.a:a:1", "org.b:b:1"}
	if !reflect.DeepEqual(locked, want) {
		t.Errorf("locked = %v, want %v (no pom-only entry)", locked, want)
	}
	// A locked install (second run) reads the classifier back and verifies the
	// same file name.
	again, err := Dependencies(cfg, []packages.PackageSource{src}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !again.FromLock || len(again.IntegrityFailures) != 0 || len(again.NoArtifact) != 0 {
		t.Errorf("locked reinstall: fromLock=%v integrity=%v noArtifact=%v", again.FromLock, again.IntegrityFailures, again.NoArtifact)
	}
}

func TestInstallWithPlatformWritesManagedLock(t *testing.T) {
	cfg := project(t, `{"dependencies":{"platform":{"org.p:bom":"1"},"implementation":{"org.a:a":"","org.b:b":"1"}}}`)
	src := &fakeSource{
		name: "test",
		meta: map[packages.CoordinateString]packages.PackageMetadata{
			"org.a:a:2": meta("org.a:a:2", "org.c:c:1"),
			"org.b:b:1": meta("org.b:b:1", "org.c:c:1"),
			"org.c:c:1": meta("org.c:c:1"),
			"org.c:c:5": meta("org.c:c:5"),
		},
		jars: map[packages.CoordinateString][]byte{
			"org.a:a:2": []byte("a"), "org.b:b:1": []byte("b"), "org.c:c:5": []byte("c"),
		},
		managed: map[packages.CoordinateString]map[packages.PackageKey]string{
			"org.p:bom:1": {"org.a:a": "2", "org.c:c": "5"},
		},
	}
	res, err := Dependencies(cfg, []packages.PackageSource{src}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// a comes from the platform, c is overridden to the managed 5 for both
	// declarers, and none of that is a conflict.
	for _, name := range []string{"a-2.jar", "b-1.jar", "c-5.jar"} {
		if _, err := os.Stat(filepath.Join(cfg.ResolvePath(config.DefaultClassPath), name)); err != nil {
			t.Errorf("expected %s installed: %v", name, err)
		}
	}
	if len(res.Resolution.Conflicts) != 0 || len(res.Resolution.Missing) != 0 {
		t.Errorf("conflicts = %v missing = %v", res.Resolution.Conflicts, res.Resolution.Missing)
	}
	lock := lockfile.Read(cfg)
	if lock == nil {
		t.Fatal("lock not written")
	}
	if lock.Roots.Platform["org.p:bom"] != "1" || !lock.Roots.Implementation["org.a:a"].Managed() {
		t.Errorf("lock roots = %+v", lock.Roots)
	}
	if !lock.Matches(cfg) {
		t.Error("the lock should match the config it was resolved from")
	}
	// A platform bump makes the lock stale like any dependency change.
	if err := os.WriteFile(filepath.Join(cfg.BaseDir, config.DefaultConfigName),
		[]byte(`{"dependencies":{"platform":{"org.p:bom":"2"},"implementation":{"org.a:a":"","org.b:b":"1"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	bumped, err := config.Load("", cfg.BaseDir)
	if err != nil {
		t.Fatal(err)
	}
	if ok, reason := CheckLocked(bumped); ok || !strings.Contains(reason, "disagree") {
		t.Errorf("platform change: ok=%v reason=%q", ok, reason)
	}
}

func TestInstallSplitsCompileOnlyAndRuntimeOnly(t *testing.T) {
	// compile: a -> shared:1 ; compileOnly: servlet -> shared:2 (loses to the
	// compile graph's shared:1, which stays in lib/classes) ; runtimeOnly: driver
	cfg := project(t, `{"dependencies":{
		"implementation":{"org.a:a":"1"},
		"compileOnly":{"org.s:servlet":"1"},
		"runtimeOnly":{"org.d:driver":"1"}}}`)
	src := &fakeSource{
		name: "test",
		meta: map[packages.CoordinateString]packages.PackageMetadata{
			"org.a:a:1":       meta("org.a:a:1", "org.x:shared:1"),
			"org.x:shared:1":  meta("org.x:shared:1"),
			"org.x:shared:2":  meta("org.x:shared:2"),
			"org.s:servlet:1": meta("org.s:servlet:1", "org.x:shared:2"),
			"org.d:driver:1":  meta("org.d:driver:1"),
		},
		jars: map[packages.CoordinateString][]byte{
			"org.a:a:1": []byte("a"), "org.x:shared:1": []byte("s1"), "org.x:shared:2": []byte("s2"),
			"org.s:servlet:1": []byte("servlet"), "org.d:driver:1": []byte("driver"),
		},
	}
	res, err := Dependencies(cfg, []packages.PackageSource{src}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	expect := map[string][]string{
		config.DefaultClassPath:       {"a-1.jar", "shared-1.jar"},
		config.DefaultCompileOnlyPath: {"servlet-1.jar"},
		config.DefaultRuntimeOnlyPath: {"driver-1.jar"},
	}
	for dir, names := range expect {
		entries, err := os.ReadDir(cfg.ResolvePath(dir))
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		var got []string
		for _, e := range entries {
			got = append(got, e.Name())
		}
		if !reflect.DeepEqual(got, names) {
			t.Errorf("%s = %v, want %v", dir, got, names)
		}
	}
	if len(res.InstalledByCategory.CompileOnly) != 1 || len(res.InstalledByCategory.RuntimeOnly) != 1 {
		t.Errorf("categories = %+v", res.InstalledByCategory)
	}
	// one graph: servlet's shared:2 loses to a's shared:1 (nearest wins, a
	// declared first) and that is reported once
	if len(res.Resolution.Conflicts) != 1 || res.Resolution.Conflicts[0].Key != "org.x:shared" || res.Resolution.Conflicts[0].Selected != "1" {
		t.Errorf("conflicts = %v", res.Resolution.Conflicts)
	}
	lock := lockfile.Read(cfg)
	if lock == nil || len(lock.CompileOnlyPackages) != 1 || len(lock.RuntimeOnlyPackages) != 1 || len(lock.Packages) != 2 {
		t.Fatalf("lock sections = %+v", lock)
	}
	// a locked reinstall materializes the same split
	again, err := Dependencies(cfg, []packages.PackageSource{src}, Options{})
	if err != nil || !again.FromLock || len(again.InstalledByCategory.CompileOnly) != 1 || len(again.InstalledByCategory.RuntimeOnly) != 1 {
		t.Errorf("locked reinstall: %+v err=%v", again.InstalledByCategory, err)
	}
}

func TestInstallScopedRootBeatsTransitiveCompileRequest(t *testing.T) {
	// implementation a -> servlet:4 ; compileOnly servlet:6. The root wins
	// (nearest) and keeps its provided-style scope: servlet:6 goes to
	// lib/compile-only, lib/classes holds no servlet at all, and the losing
	// servlet:4 is reported.
	cfg := project(t, `{"dependencies":{"implementation":{"org.a:a":"1"},"compileOnly":{"org.s:servlet":"6"}}}`)
	src := &fakeSource{
		name: "test",
		meta: map[packages.CoordinateString]packages.PackageMetadata{
			"org.a:a:1":       meta("org.a:a:1", "org.s:servlet:4"),
			"org.s:servlet:4": meta("org.s:servlet:4"),
			"org.s:servlet:6": meta("org.s:servlet:6", "org.s:spi:1"),
			"org.s:spi:1":     meta("org.s:spi:1"),
		},
		jars: map[packages.CoordinateString][]byte{
			"org.a:a:1": []byte("a"), "org.s:servlet:6": []byte("s6"), "org.s:spi:1": []byte("spi"),
		},
	}
	res, err := Dependencies(cfg, []packages.PackageSource{src}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	dirNames := func(dir string) []string {
		entries, _ := os.ReadDir(cfg.ResolvePath(dir))
		var got []string
		for _, e := range entries {
			got = append(got, e.Name())
		}
		return got
	}
	if got := dirNames(config.DefaultClassPath); !reflect.DeepEqual(got, []string{"a-1.jar"}) {
		t.Errorf("classes = %v, want only a", got)
	}
	if got := dirNames(config.DefaultCompileOnlyPath); !reflect.DeepEqual(got, []string{"servlet-6.jar", "spi-1.jar"}) {
		t.Errorf("compile-only = %v, want servlet 6 and its own dependency", got)
	}
	if len(res.Resolution.Conflicts) != 1 || res.Resolution.Conflicts[0].Rejected != "4" || res.Resolution.Conflicts[0].Selected != "6" {
		t.Errorf("conflicts = %v, want servlet 4 (via a) loses to 6", res.Resolution.Conflicts)
	}
}

func TestPartitionScopesSharedByCompileOnlyAndRuntimeOnly(t *testing.T) {
	// co -> shared ; ro -> shared ; the compile graph never reaches shared, so
	// it has to be in both dirs (compile needs it through co, runtime through ro).
	co := packages.DependencyDeclaration{Coordinates: coord("org.c:co:1")}
	ro := packages.DependencyDeclaration{Coordinates: coord("org.r:ro:1")}
	res := packages.Resolution{Packages: []packages.ResolvedPackage{
		{Coordinates: coord("org.c:co:1")},
		{Coordinates: coord("org.r:ro:1")},
		{Coordinates: coord("org.x:shared:1"), RequestedBy: coord("org.c:co:1"), AlsoRequestedBy: []packages.Coordinates{coord("org.r:ro:1")}},
	}}
	classes, compileOnly, runtimeOnly := partitionScopes(res, nil, []packages.DependencyDeclaration{co}, []packages.DependencyDeclaration{ro})
	labels := func(ps []packages.ResolvedPackage) []string {
		var out []string
		for _, p := range ps {
			out = append(out, p.Coordinates.Label())
		}
		return out
	}
	if len(classes) != 0 || !reflect.DeepEqual(labels(compileOnly), []string{"org.c:co:1", "org.x:shared:1"}) || !reflect.DeepEqual(labels(runtimeOnly), []string{"org.r:ro:1", "org.x:shared:1"}) {
		t.Errorf("classes=%v compileOnly=%v runtimeOnly=%v", labels(classes), labels(compileOnly), labels(runtimeOnly))
	}
}

func TestSnapshotJarBypassesStore(t *testing.T) {
	cfg := project(t, `{"dependencies":{"implementation":{"org.a:a":"1.0-SNAPSHOT"}}}`)
	src := &fakeSource{
		name: "test",
		meta: map[packages.CoordinateString]packages.PackageMetadata{"org.a:a:1.0-SNAPSHOT": meta("org.a:a:1.0-SNAPSHOT")},
		jars: map[packages.CoordinateString][]byte{"org.a:a:1.0-SNAPSHOT": []byte("build 1")},
	}
	if _, err := Dependencies(cfg, []packages.PackageSource{src}, Options{}); err != nil {
		t.Fatal(err)
	}
	if stored, _ := StorePathFor(coord("org.a:a:1.0-SNAPSHOT")); fileExists(stored) {
		t.Error("a snapshot must not be kept in the global store")
	}
	// the snapshot is rebuilt: a re-resolve (cappu add rewrites the lock) sees
	// the new bytes instead of a stale store copy
	src.jars["org.a:a:1.0-SNAPSHOT"] = []byte("build 2")
	if _, err := Dependencies(cfg, []packages.PackageSource{src}, Options{UpdateLock: true}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(cfg.ResolvePath(config.DefaultClassPath), "a-1.0-SNAPSHOT.jar"))
	if string(got) != "build 2" {
		t.Errorf("installed snapshot = %q, want the rebuilt bytes", got)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestInstallCompileEdgeLosingToRuntimeOnlyStaysOnCompilePath(t *testing.T) {
	// implementation a -> b -> l:1 ; runtimeOnly r -> l:2. l:2 wins (nearer)
	// and the compile graph still needs l, so it belongs to lib/classes (Maven
	// keeps the widest scope), not only to lib/runtime-only.
	cfg := project(t, `{"dependencies":{"implementation":{"org.a:a":"1"},"runtimeOnly":{"org.r:r":"1"}}}`)
	src := &fakeSource{
		name: "test",
		meta: map[packages.CoordinateString]packages.PackageMetadata{
			"org.a:a:1": meta("org.a:a:1", "org.b:b:1"),
			"org.b:b:1": meta("org.b:b:1", "org.l:l:1"),
			"org.r:r:1": meta("org.r:r:1", "org.l:l:2"),
			"org.l:l:1": meta("org.l:l:1"),
			"org.l:l:2": meta("org.l:l:2"),
		},
		jars: map[packages.CoordinateString][]byte{
			"org.a:a:1": []byte("a"), "org.b:b:1": []byte("b"), "org.r:r:1": []byte("r"), "org.l:l:2": []byte("l2"),
		},
	}
	if _, err := Dependencies(cfg, []packages.PackageSource{src}, Options{}); err != nil {
		t.Fatal(err)
	}
	if !fileExists(filepath.Join(cfg.ResolvePath(config.DefaultClassPath), "l-2.jar")) {
		t.Error("l-2.jar should be in lib/classes: b compiles against it")
	}
	if fileExists(filepath.Join(cfg.ResolvePath(config.DefaultRuntimeOnlyPath), "l-2.jar")) {
		t.Error("l-2.jar must not be duplicated into lib/runtime-only")
	}
}

func TestLockedInstallAcceptsRebuiltSnapshot(t *testing.T) {
	cfg := project(t, `{"dependencies":{"implementation":{"org.a:a":"1.0-SNAPSHOT"}}}`)
	src := &fakeSource{
		name: "test",
		meta: map[packages.CoordinateString]packages.PackageMetadata{"org.a:a:1.0-SNAPSHOT": meta("org.a:a:1.0-SNAPSHOT")},
		jars: map[packages.CoordinateString][]byte{"org.a:a:1.0-SNAPSHOT": []byte("build 1")},
	}
	if _, err := Dependencies(cfg, []packages.PackageSource{src}, Options{}); err != nil {
		t.Fatal(err)
	}
	src.jars["org.a:a:1.0-SNAPSHOT"] = []byte("build 2")
	// a plain (locked) install, as after `mvn install` of the sibling
	res, err := Dependencies(cfg, []packages.PackageSource{src}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.FromLock || len(res.IntegrityFailures) != 0 {
		t.Errorf("fromLock=%v integrity=%v; a snapshot is never an integrity failure", res.FromLock, res.IntegrityFailures)
	}
	got, _ := os.ReadFile(filepath.Join(cfg.ResolvePath(config.DefaultClassPath), "a-1.0-SNAPSHOT.jar"))
	if string(got) != "build 2" {
		t.Errorf("installed = %q, want the rebuilt bytes", got)
	}
	if v := lockfile.VerifyInstalled(cfg); len(v.Modified) != 0 || len(v.OK) != 1 {
		t.Errorf("verify = %+v; a present snapshot is OK whatever its bytes", v)
	}
}
