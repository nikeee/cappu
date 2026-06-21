package packages

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMavenSearchParsesSolrResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "gson" {
			t.Errorf("query q = %q, want gson", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":{"docs":[
			{"g":"com.google.code.gson","a":"gson","latestVersion":"2.13.2"},
			{"g":"com.example","a":"no-version"}
		]}}`))
	}))
	defer server.Close()

	source := NewMavenRepositorySource("https://repo.example/maven2", server.URL)
	hits, err := source.Search("gson")
	if err != nil {
		t.Fatal(err)
	}
	// The doc missing latestVersion is filtered out.
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1: %v", len(hits), hits)
	}
	if hits[0].String() != "com.google.code.gson:gson:2.13.2" {
		t.Errorf("hit = %q", hits[0].String())
	}
}

func TestToSolrQueryTranslatesCoordinate(t *testing.T) {
	cases := map[string]string{
		"org.apache.commons:commons-lang3": `g:"org.apache.commons" AND a:"commons-lang3"`,
		"commons-lang3":                    "commons-lang3",
		"foo bar":                          "foo bar",
	}
	for in, want := range cases {
		if got := toSolrQuery(in); got != want {
			t.Errorf("toSolrQuery(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSearchSendsStructuredQueryForCoordinate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != `g:"org.apache.commons" AND a:"commons-lang3"` {
			t.Errorf("query q = %q", got)
		}
		_, _ = w.Write([]byte(`{"response":{"docs":[]}}`))
	}))
	defer server.Close()

	source := NewMavenRepositorySource("https://repo.example/maven2", server.URL)
	if _, err := source.Search("org.apache.commons:commons-lang3"); err != nil {
		t.Fatal(err)
	}
}

func TestSearchWithoutIndexReturnsNil(t *testing.T) {
	source := NewMavenRepositorySource("https://repo.example/maven2", "")
	hits, err := source.Search("anything")
	if err != nil || hits != nil {
		t.Errorf("Search without an index = (%v, %v), want (nil, nil)", hits, err)
	}
}

func TestSearchPackagesDedupsByGroupArtifact(t *testing.T) {
	dup := stubSource{hits: []Coordinates{
		NewCoordinates("g", "a", "1"),
		NewCoordinates("g", "a", "2"), // same key: dropped
		NewCoordinates("g", "b", "1"),
	}}
	hits, err := SearchPackages("q", []PackageSource{dup})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2 (deduped): %v", len(hits), hits)
	}
}

type stubSource struct{ hits []Coordinates }

func (s stubSource) Name() string { return "stub" }

func (s stubSource) Search(string) ([]Coordinates, error) { return s.hits, nil }

func (s stubSource) ListVersions(string, string) ([]string, error) { return nil, nil }

func (s stubSource) GetMetadata(Coordinates) (*PackageMetadata, error) { return nil, nil }

func (s stubSource) GetArtifact(Coordinates) ([]byte, error) { return nil, nil }
