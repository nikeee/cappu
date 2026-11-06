package sources

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/packages"
)

func TestManagedVersionsOfFirstDeclaredPlatformWins(t *testing.T) {
	src := packages.NewInMemoryPackageSource("test", nil)
	src.SetManaged(packages.NewCoordinates("org.a", "bom", "1"), map[packages.PackageKey]string{"g:x": "1", "g:y": "1"})
	src.SetManaged(packages.NewCoordinates("org.b", "bom", "1"), map[packages.PackageKey]string{"g:x": "2", "g:z": "3"})
	platform := map[string]string{"org.b:bom": "1", "org.a:bom": "1"}
	// declared order: org.b first, so its g:x wins over org.a's
	got, err := ManagedVersionsOf(config.Dependencies{Platform: platform, PlatformOrder: []string{"org.b:bom", "org.a:bom"}}, []packages.PackageSource{src})
	if err != nil {
		t.Fatal(err)
	}
	want := map[packages.PackageKey]string{"g:x": "2", "g:y": "1", "g:z": "3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("managed = %v, want %v", got, want)
	}
	// without a declared order the sorted key order stands in: org.a first
	got, err = ManagedVersionsOf(config.Dependencies{Platform: platform}, []packages.PackageSource{src})
	if err != nil || got["g:x"] != "1" {
		t.Errorf("unordered managed g:x = %q, %v; want 1", got["g:x"], err)
	}
	if _, err := ManagedVersionsOf(config.Dependencies{Platform: map[string]string{"org.none:bom": "1"}}, []packages.PackageSource{src}); err == nil || !strings.Contains(err.Error(), "platform org.none:bom:1: not found") {
		t.Errorf("missing BOM err = %v", err)
	}
}

func TestRootsOfCarriesClassifierAndExclusions(t *testing.T) {
	roots := RootsOf(map[string]config.Dependency{
		"org.a:a": {Version: "1", Exclude: []string{"x:*"}},
		"io.n:n":  {Classifier: "linux-x86_64"},
	})
	want := []packages.DependencyDeclaration{
		{Coordinates: packages.NewCoordinates("io.n", "n", "").WithClassifier("linux-x86_64")},
		{Coordinates: packages.NewCoordinates("org.a", "a", "1"), Exclusions: []string{"x:*"}},
	}
	if !reflect.DeepEqual(roots, want) {
		t.Errorf("roots = %+v\nwant %+v", roots, want)
	}
}

func TestFromEntriesAuthenticatedAndLocal(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		if strings.HasSuffix(r.URL.Path, "maven-metadata.xml") {
			_, _ = w.Write([]byte(`<metadata><versioning><versions><version>1</version></versions></versioning></metadata>`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	cfg := &config.Config{BaseDir: t.TempDir()}
	entries := []config.PackageSource{{URL: server.URL, Auth: "NEXUS"}, {URL: "./repo"}}
	env := map[string]string{"CAPPU_SOURCE_NEXUS_TOKEN": "t0k"}
	srcs := FromEntries(cfg, entries, false, func(k string) string { return env[k] })
	if len(srcs) != 2 {
		t.Fatalf("sources = %d", len(srcs))
	}
	if v, err := srcs[0].ListVersions("g", "a"); err != nil || len(v) != 1 || authorization != "Bearer t0k" {
		t.Errorf("authenticated fetch: versions=%v err=%v Authorization=%q", v, err, authorization)
	}
	if _, ok := srcs[1].(*packages.FileRepositorySource); !ok || srcs[1].Name() != "./repo" {
		t.Errorf("local entry = %T %q", srcs[1], srcs[1].Name())
	}

	// missing credentials: the source fails on use, naming the variables
	none := FromEntries(cfg, entries[:1], false, func(string) string { return "" })
	_, err := none[0].ListVersions("g", "a")
	if err == nil || !strings.Contains(err.Error(), "CAPPU_SOURCE_NEXUS_TOKEN") {
		t.Errorf("err = %v, want the env vars named", err)
	}
}
