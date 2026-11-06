package lockfile

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/config"
)

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// project writes a cappu.json (so BaseDir/FromFile are set) plus a lockfile and
// returns a config rooted there.
func project(t *testing.T, lock string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.DefaultConfigName), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	if lock != "" {
		if err := os.WriteFile(filepath.Join(dir, Name), []byte(lock), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return cfg
}

// installJar writes bytes to the default class path under the maven jar name.
func installJar(t *testing.T, cfg *config.Config, artifact, version string, bytes []byte) {
	t.Helper()
	dir := cfg.ResolvePath(config.DefaultClassPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := artifact + "-" + version + ".jar"
	if err := os.WriteFile(filepath.Join(dir, name), bytes, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyNoLock(t *testing.T) {
	cfg := project(t, "")
	if got := VerifyInstalled(cfg); got.FromLock {
		t.Error("FromLock should be false without a lockfile")
	}
}

func TestVerifyOkModifiedMissing(t *testing.T) {
	jar := []byte("class bytes")
	lock := `{
  "version": 2,
  "roots": {},
  "packages": [
    {"coordinates":{"groupId":"g","artifactId":"ok","version":"1"},"source":"s","sha256":"` + sha(jar) + `"},
    {"coordinates":{"groupId":"g","artifactId":"bad","version":"1"},"source":"s","sha256":"` + sha(jar) + `"},
    {"coordinates":{"groupId":"g","artifactId":"gone","version":"1"},"source":"s","sha256":"` + sha(jar) + `"}
  ]
}`
	cfg := project(t, lock)
	installJar(t, cfg, "ok", "1", jar)
	installJar(t, cfg, "bad", "1", []byte("tampered"))
	// "gone" is never installed.

	got := VerifyInstalled(cfg)
	if !got.FromLock {
		t.Fatal("FromLock should be true")
	}
	if len(got.OK) != 1 || got.OK[0] != "g:ok:1" {
		t.Errorf("OK = %v, want [g:ok:1]", got.OK)
	}
	if len(got.Modified) != 1 || got.Modified[0] != "g:bad:1" {
		t.Errorf("Modified = %v, want [g:bad:1]", got.Modified)
	}
	if len(got.Missing) != 1 || got.Missing[0] != "g:gone:1" {
		t.Errorf("Missing = %v, want [g:gone:1]", got.Missing)
	}
}

func TestVerifyIgnoresWrongVersionLock(t *testing.T) {
	cfg := project(t, `{"version": 1, "packages": []}`)
	if got := VerifyInstalled(cfg); got.FromLock {
		t.Error("a version-1 lock should be ignored (FromLock false)")
	}
}

func TestReadRequiresPackagesArray(t *testing.T) {
	// {"version":2} without packages is no lock at all (TS parity), not a
	// valid empty lock that would install nothing.
	cfg := project(t, `{"version": 2}`)
	if Read(cfg) != nil {
		t.Error("a lock without a packages array should be ignored")
	}
	cfg = project(t, `{"version": 2, "packages": []}`)
	if Read(cfg) == nil {
		t.Error("a lock with an empty packages array is valid")
	}
}

func TestVerifyClassifiedJar(t *testing.T) {
	jar := []byte("native bytes")
	lock := `{
  "version": 2,
  "roots": {},
  "packages": [
    {"coordinates":{"groupId":"g","artifactId":"n","version":"1","classifier":"linux-x86_64"},"source":"s","sha256":"` + sha(jar) + `"}
  ]
}`
	cfg := project(t, lock)
	installJar(t, cfg, "n", "1-linux-x86_64", jar)
	got := VerifyInstalled(cfg)
	if len(got.OK) != 1 || got.OK[0] != "g:n:1:linux-x86_64" {
		t.Errorf("OK = %v, Missing = %v", got.OK, got.Missing)
	}
}

func TestLockRoundTripsObjectRoots(t *testing.T) {
	cfg := project(t, "")
	roots := config.Dependencies{
		Platform:       map[string]string{"org.p:bom": "1"},
		Implementation: map[string]config.Dependency{"g:a": {}, "g:b": {Version: "1", Classifier: "c", Exclude: []string{"x:*"}}},
	}
	if err := Write(cfg, &Lockfile{Version: 2, Roots: roots, PlatformOrder: roots.PlatformKeys(), Packages: []LockedPackage{}}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(Path(cfg))
	for _, want := range []string{`"g:a": ""`, `"classifier": "c"`, `"org.p:bom": "1"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("lock %s lacks %s", data, want)
		}
	}
	back := Read(cfg)
	if back == nil {
		t.Fatal("lock not read back")
	}
	cfg.Dependencies.Platform, cfg.Dependencies.Implementation = roots.Platform, roots.Implementation
	if !back.Matches(cfg) {
		t.Errorf("round-tripped roots %+v should match %+v", back.Roots, cfg.Dependencies)
	}
	cfg.Dependencies.Platform = map[string]string{"org.p:bom": "2"}
	if back.Matches(cfg) {
		t.Error("a platform change must make the lock stale")
	}
}

func TestMatchesTracksPlatformOrder(t *testing.T) {
	cfg := project(t, "")
	cfg.Dependencies.Platform = map[string]string{"org.a:bom": "1", "org.b:bom": "1"}
	cfg.Dependencies.PlatformOrder = []string{"org.b:bom", "org.a:bom"}
	lock := &Lockfile{Version: 2, Roots: cfg.Dependencies, PlatformOrder: cfg.Dependencies.PlatformKeys(), Packages: []LockedPackage{}}
	if !lock.Matches(cfg) {
		t.Error("same platforms in the same order should match")
	}
	cfg.Dependencies.PlatformOrder = []string{"org.a:bom", "org.b:bom"}
	if lock.Matches(cfg) {
		t.Error("reordering platforms changes precedence: the lock is stale")
	}
}
