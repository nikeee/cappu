package install

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/nikeee/cappu/internal/lockfile"
)

func TestVerifyCache(t *testing.T) {
	store := filepath.Join(t.TempDir(), "store")
	t.Setenv("CAPPU_PACKAGE_STORE", store)
	sha := func(s string) string { return string(lockfile.Sha256Of([]byte(s))) }
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(store, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// a good jar (bytes match the sidecar)
	write(filepath.Join("org", "a", "a", "1", "a-1.jar"), "good")
	write(filepath.Join("org", "a", "a", "1", "a-1.jar.sha256"), sha("good"))
	// a tampered jar (sidecar records other bytes) and an orphan sidecar
	write(filepath.Join("org", "b", "b", "1", "b-1.jar"), "tampered")
	write(filepath.Join("org", "b", "b", "1", "b-1.jar.sha256"), sha("original"))
	write(filepath.Join("org", "b", "b", "1", "ghost-1.jar.sha256"), sha("x"))
	// a good cached POM
	write(filepath.Join("_metadata", "src", "org", "c", "c", "1", "c-1.pom"), "<pom/>")
	write(filepath.Join("_metadata", "src", "org", "c", "c", "1", "metadata.json"),
		`{"v":2,"pomSha256":"`+sha("<pom/>")+`"}`)

	r := VerifyCache()
	eq := func(name string, got, want []string) {
		t.Helper()
		sort.Strings(got)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	eq("ok", r.OK, []string{
		filepath.Join("org", "a", "a", "1", "a-1.jar"),
		filepath.Join("_metadata", "src", "org", "c", "c", "1", "c-1.pom"),
	})
	eq("modified", r.Modified, []string{filepath.Join("org", "b", "b", "1", "b-1.jar")})
	eq("missing", r.Missing, []string{filepath.Join("org", "b", "b", "1", "ghost-1.jar")})
}
