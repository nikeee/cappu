package packages

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// dep is a test dependency spec with optional scope/optional flags.
type dep struct {
	spec     string
	scope    string
	optional bool
}

func coord(spec string) Coordinates {
	parts := strings.Split(spec, ":")
	return NewCoordinates(parts[0], parts[1], parts[2])
}

// pkg builds package metadata from a "group:artifact:version" spec and deps.
func pkg(spec string, deps ...dep) PackageMetadata {
	declarations := make([]DependencyDeclaration, 0, len(deps))
	for _, d := range deps {
		declarations = append(declarations, DependencyDeclaration{
			Coordinates: coord(d.spec),
			Scope:       MavenScope(d.scope),
			Optional:    d.optional,
		})
	}
	return PackageMetadata{Coordinates: coord(spec), Dependencies: declarations}
}

func names(packages []ResolvedPackage) []string {
	out := make([]string, len(packages))
	for i, p := range packages {
		out[i] = string(p.Coordinates.String())
	}
	return out
}

// Port of src/packages/resolver.test.ts.

func TestResolveTransitiveBreadthFirst(t *testing.T) {
	source := NewInMemoryPackageSource("test", []PackageMetadata{
		pkg("org.a:a:1", dep{spec: "org.b:b:1"}),
		pkg("org.b:b:1", dep{spec: "org.c:c:1"}),
		pkg("org.c:c:1"),
	})
	res, err := ResolveTransitive([]Coordinates{coord("org.a:a:1")}, []PackageSource{source}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(res.Packages); !reflect.DeepEqual(got, []string{"org.a:a:1", "org.b:b:1", "org.c:c:1"}) {
		t.Errorf("packages = %v", got)
	}
	depths := []int{res.Packages[0].Depth, res.Packages[1].Depth, res.Packages[2].Depth}
	if !reflect.DeepEqual(depths, []int{0, 1, 2}) {
		t.Errorf("depths = %v", depths)
	}
	if res.Packages[1].RequestedBy != coord("org.a:a:1") {
		t.Errorf("requestedBy = %v", res.Packages[1].RequestedBy)
	}
	if len(res.Conflicts) != 0 || len(res.Missing) != 0 {
		t.Errorf("expected no conflicts/missing, got %v / %v", res.Conflicts, res.Missing)
	}
}

func TestResolveOnResolveOrder(t *testing.T) {
	source := NewInMemoryPackageSource("test", []PackageMetadata{
		pkg("org.a:a:1", dep{spec: "org.b:b:1"}, dep{spec: "org.c:c:1"}),
		pkg("org.b:b:1", dep{spec: "org.c:c:1"}), // c reached twice, resolved once
		pkg("org.c:c:1"),
		pkg("org.x:x:1"),
	})
	var seen []string
	res, err := ResolveTransitive(
		[]Coordinates{coord("org.a:a:1"), coord("org.missing:m:1")},
		[]PackageSource{source},
		func(c Coordinates) { seen = append(seen, string(c.String())) },
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"org.a:a:1", "org.missing:m:1", "org.b:b:1", "org.c:c:1"}
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("seen = %v, want %v", seen, want)
	}
	if len(seen) != len(res.Packages)+len(res.Missing) {
		t.Errorf("seen %d != resolved %d + missing %d", len(seen), len(res.Packages), len(res.Missing))
	}
}

func TestResolveNearestWins(t *testing.T) {
	source := NewInMemoryPackageSource("test", []PackageMetadata{
		pkg("org.a:a:1", dep{spec: "org.b:b:1"}, dep{spec: "org.c:c:1"}),
		pkg("org.b:b:1"),
		pkg("org.b:b:2"),
		pkg("org.c:c:1", dep{spec: "org.b:b:2"}), // farther than a's direct b:1
	})
	res, err := ResolveTransitive([]Coordinates{coord("org.a:a:1")}, []PackageSource{source}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := names(res.Packages)
	if !contains(got, "org.b:b:1") || contains(got, "org.b:b:2") {
		t.Errorf("expected b:1 selected, b:2 rejected; got %v", got)
	}
	want := []VersionConflict{{Key: "org.b:b", Selected: "1", Rejected: "2", RejectedBy: coord("org.c:c:1")}}
	if !reflect.DeepEqual(res.Conflicts, want) {
		t.Errorf("conflicts = %v, want %v", res.Conflicts, want)
	}
}

func TestResolveCyclesTerminate(t *testing.T) {
	source := NewInMemoryPackageSource("test", []PackageMetadata{
		pkg("org.a:a:1", dep{spec: "org.b:b:1"}),
		pkg("org.b:b:1", dep{spec: "org.a:a:1"}),
	})
	res, err := ResolveTransitive([]Coordinates{coord("org.a:a:1")}, []PackageSource{source}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Packages) != 2 {
		t.Errorf("expected 2 packages, got %d", len(res.Packages))
	}
}

func TestResolveScopeAndOptionalDoNotPropagate(t *testing.T) {
	source := NewInMemoryPackageSource("test", []PackageMetadata{
		pkg("org.a:a:1",
			dep{spec: "org.t:t:1", scope: "test"},
			dep{spec: "org.o:o:1", optional: true},
			dep{spec: "org.r:r:1", scope: "runtime"},
		),
		pkg("org.r:r:1"),
	})
	res, err := ResolveTransitive([]Coordinates{coord("org.a:a:1")}, []PackageSource{source}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(res.Packages); !reflect.DeepEqual(got, []string{"org.a:a:1", "org.r:r:1"}) {
		t.Errorf("packages = %v", got)
	}
	if len(res.Missing) != 0 {
		t.Errorf("missing = %v", res.Missing)
	}
}

func TestResolveSourcesInOrder(t *testing.T) {
	primary := NewInMemoryPackageSource("primary", []PackageMetadata{pkg("org.a:a:1", dep{spec: "org.b:b:1"})})
	fallback := NewInMemoryPackageSource("fallback", []PackageMetadata{pkg("org.a:a:1"), pkg("org.b:b:1")})
	res, err := ResolveTransitive([]Coordinates{coord("org.a:a:1")}, []PackageSource{primary, fallback}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := [][2]string{}
	for _, p := range res.Packages {
		got = append(got, [2]string{string(p.Coordinates.String()), string(p.Source)})
	}
	want := [][2]string{{"org.a:a:1", "primary"}, {"org.b:b:1", "fallback"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("packages = %v, want %v", got, want)
	}
}

func TestResolveMissingReportedOnce(t *testing.T) {
	source := NewInMemoryPackageSource("test", []PackageMetadata{
		pkg("org.a:a:1", dep{spec: "org.gone:gone:9"}, dep{spec: "org.gone:gone:9"}),
	})
	res, err := ResolveTransitive([]Coordinates{coord("org.a:a:1")}, []PackageSource{source}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []MissingPackage{{Coordinates: coord("org.gone:gone:9"), RequestedBy: coord("org.a:a:1")}}
	if !reflect.DeepEqual(res.Missing, want) {
		t.Errorf("missing = %v, want %v", res.Missing, want)
	}
}

func TestSearchMergesSources(t *testing.T) {
	primary := NewInMemoryPackageSource("primary", []PackageMetadata{pkg("org.x:json-lib:2")})
	fallback := NewInMemoryPackageSource("fallback", []PackageMetadata{
		pkg("org.x:json-lib:1"),
		pkg("org.y:json-other:1"),
	})
	hits, err := SearchPackages("json", []PackageSource{primary, fallback})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(hits))
	for i, h := range hits {
		got[i] = string(h.String())
	}
	if !reflect.DeepEqual(got, []string{"org.x:json-lib:2", "org.y:json-other:1"}) {
		t.Errorf("hits = %v", got)
	}
}

func TestLatestVersion(t *testing.T) {
	source := NewInMemoryPackageSource("test", []PackageMetadata{pkg("org.a:a:1"), pkg("org.a:a:2")})
	if v, _ := LatestVersion("org.a", "a", []PackageSource{source}); v != "2" {
		t.Errorf("LatestVersion = %q, want 2", v)
	}
	if v, _ := LatestVersion("org.nope", "a", []PackageSource{source}); v != "" {
		t.Errorf("LatestVersion(missing) = %q, want empty", v)
	}
}

// erroringSource fails GetMetadata, standing in for a source that exhausted its
// retries on a transient HTTP failure (429/5xx).
type erroringSource struct{ err error }

func (erroringSource) Name() SourceName                              { return "erroring" }
func (erroringSource) Search(string) ([]SearchHit, error)            { return nil, nil }
func (erroringSource) ListVersions(string, string) ([]string, error) { return nil, nil }
func (erroringSource) GetArtifact(Coordinates) ([]byte, error)       { return nil, nil }
func (s erroringSource) GetMetadata(Coordinates) (*PackageMetadata, error) {
	return nil, s.err
}

// Regression for nikeee/cappu#22: a transient fetch failure must abort the
// resolve with the error, never be recorded as a missing package (which the CLI
// would print as "not found in any package source").
func TestResolveTransitivePropagatesFetchError(t *testing.T) {
	boom := errors.New("repo.example: HTTP 429 after 4 attempts")
	res, err := ResolveTransitive([]Coordinates{coord("org.a:a:1")}, []PackageSource{erroringSource{err: boom}}, nil)
	if err == nil {
		t.Fatal("want the fetch error to propagate, got nil")
	}
	if len(res.Missing) != 0 {
		t.Errorf("Missing = %v, want empty (a transient error is not a miss)", res.Missing)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
