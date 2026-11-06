package packages

import (
	"errors"
	"reflect"
	"slices"
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

// roots wraps coordinates as plain resolver roots (no exclusions).
func roots(cs ...Coordinates) []DependencyDeclaration {
	out := make([]DependencyDeclaration, len(cs))
	for i, c := range cs {
		out[i] = DependencyDeclaration{Coordinates: c}
	}
	return out
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
	res, err := ResolveTransitive(roots(coord("org.a:a:1")), []PackageSource{source}, ResolveOptions{})
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
		roots(coord("org.a:a:1"), coord("org.missing:m:1")),
		[]PackageSource{source},
		ResolveOptions{OnResolve: func(c Coordinates) { seen = append(seen, string(c.String())) }},
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
	res, err := ResolveTransitive(roots(coord("org.a:a:1")), []PackageSource{source}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := names(res.Packages)
	if !slices.Contains(got, "org.b:b:1") || slices.Contains(got, "org.b:b:2") {
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
	res, err := ResolveTransitive(roots(coord("org.a:a:1")), []PackageSource{source}, ResolveOptions{})
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
	res, err := ResolveTransitive(roots(coord("org.a:a:1")), []PackageSource{source}, ResolveOptions{})
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
	res, err := ResolveTransitive(roots(coord("org.a:a:1")), []PackageSource{primary, fallback}, ResolveOptions{})
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
	res, err := ResolveTransitive(roots(coord("org.a:a:1")), []PackageSource{source}, ResolveOptions{})
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
func (erroringSource) GetPom(Coordinates) ([]byte, error)            { return nil, nil }
func (erroringSource) GetManaged(Coordinates) (map[PackageKey]string, error) {
	return nil, nil
}
func (s erroringSource) GetMetadata(Coordinates) (*PackageMetadata, error) {
	return nil, s.err
}

// Regression for nikeee/cappu#22: a transient fetch failure must abort the
// resolve with the error, never be recorded as a missing package (which the CLI
// would print as "not found in any package source").
func TestResolveTransitivePropagatesFetchError(t *testing.T) {
	boom := errors.New("repo.example: HTTP 429 after 4 attempts")
	res, err := ResolveTransitive(roots(coord("org.a:a:1")), []PackageSource{erroringSource{err: boom}}, ResolveOptions{})
	if err == nil {
		t.Fatal("want the fetch error to propagate, got nil")
	}
	if len(res.Missing) != 0 {
		t.Errorf("Missing = %v, want empty (a transient error is not a miss)", res.Missing)
	}
}

func TestResolveRangeRoot(t *testing.T) {
	source := NewInMemoryPackageSource("test", []PackageMetadata{
		pkg("org.a:a:1.0", dep{spec: "org.b:b:1"}),
		pkg("org.a:a:1.9", dep{spec: "org.b:b:1"}),
		pkg("org.a:a:2.0", dep{spec: "org.b:b:1"}),
		pkg("org.b:b:1"),
	})
	res, err := ResolveTransitive(roots(coord("org.a:a:[1.0,2.0)")), []PackageSource{source}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(res.Packages); !reflect.DeepEqual(got, []string{"org.a:a:1.9", "org.b:b:1"}) {
		t.Errorf("packages = %v", got)
	}
	if len(res.Missing) != 0 {
		t.Errorf("missing = %v", res.Missing)
	}
}

func TestResolveRangeTransitive(t *testing.T) {
	source := NewInMemoryPackageSource("test", []PackageMetadata{
		pkg("org.a:a:1", dep{spec: "org.b:b:[1.0,2.0)"}),
		pkg("org.b:b:1.0"),
		pkg("org.b:b:1.5"),
		pkg("org.b:b:2.0"),
	})
	res, err := ResolveTransitive(roots(coord("org.a:a:1")), []PackageSource{source}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(res.Packages); !reflect.DeepEqual(got, []string{"org.a:a:1", "org.b:b:1.5"}) {
		t.Errorf("packages = %v", got)
	}
	if len(res.Missing) != 0 {
		t.Errorf("missing = %v", res.Missing)
	}
}

func TestResolveRangeUnsatisfiable(t *testing.T) {
	source := NewInMemoryPackageSource("test", []PackageMetadata{
		pkg("org.a:a:1.0"),
		pkg("org.a:a:2.0"),
	})
	res, err := ResolveTransitive(roots(coord("org.a:a:[5.0,6.0)")), []PackageSource{source}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Packages) != 0 {
		t.Errorf("packages = %v, want empty", names(res.Packages))
	}
	if len(res.Missing) != 1 || string(res.Missing[0].Coordinates.String()) != "org.a:a:[5.0,6.0)" {
		t.Errorf("missing = %v", res.Missing)
	}
}

func TestResolveExclusionsPruneSubtreePerPath(t *testing.T) {
	// a -> b (excluding c:*) -> c -> d ; a -> e -> c
	// c is pruned below b but still arrives via e (and brings d along): an
	// exclusion is scoped to its path, not global.
	b := pkg("org.b:b:1", dep{spec: "org.c:c:1"})
	c := pkg("org.c:c:1", dep{spec: "org.d:d:1"})
	source := NewInMemoryPackageSource("test", []PackageMetadata{
		{Coordinates: coord("org.a:a:1"), Dependencies: []DependencyDeclaration{
			{Coordinates: coord("org.b:b:1"), Exclusions: []string{"org.c:*"}},
			{Coordinates: coord("org.e:e:1")},
		}},
		b, c,
		pkg("org.d:d:1"),
		pkg("org.e:e:1", dep{spec: "org.c:c:1"}),
	})
	res, err := ResolveTransitive(roots(coord("org.a:a:1")), []PackageSource{source}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := names(res.Packages)
	if !reflect.DeepEqual(got, []string{"org.a:a:1", "org.b:b:1", "org.e:e:1", "org.c:c:1", "org.d:d:1"}) {
		t.Errorf("packages = %v", got)
	}
	for _, p := range res.Packages {
		if p.Coordinates.String() == "org.c:c:1" && p.RequestedBy != coord("org.e:e:1") {
			t.Errorf("c requested by %v, want e", p.RequestedBy)
		}
	}
}

func TestResolveExclusionsInheritDownThePath(t *testing.T) {
	// a -> b (excluding d) -> c -> d : the exclusion declared on a->b prunes
	// d two levels down, not just b's direct dependencies.
	source := NewInMemoryPackageSource("test", []PackageMetadata{
		{Coordinates: coord("org.a:a:1"), Dependencies: []DependencyDeclaration{
			{Coordinates: coord("org.b:b:1"), Exclusions: []string{"org.d:d"}},
		}},
		pkg("org.b:b:1", dep{spec: "org.c:c:1"}),
		pkg("org.c:c:1", dep{spec: "org.d:d:1"}),
		pkg("org.d:d:1"),
	})
	res, err := ResolveTransitive(roots(coord("org.a:a:1")), []PackageSource{source}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(res.Packages); !reflect.DeepEqual(got, []string{"org.a:a:1", "org.b:b:1", "org.c:c:1"}) {
		t.Errorf("packages = %v", got)
	}
}

func TestResolvePomOnlyAndClassifiers(t *testing.T) {
	native := coord("io.n:n:1").WithClassifier("linux-x86_64")
	source := NewInMemoryPackageSource("test", []PackageMetadata{
		{Coordinates: coord("org.a:a:1"), Dependencies: []DependencyDeclaration{
			{Coordinates: pomOnly("org.bom:bom:1")},
			{Coordinates: coord("io.n:n:1")},
			{Coordinates: native},
		}},
		pkg("org.bom:bom:1", dep{spec: "org.c:c:1"}),
		// c also wants bom:1 as a plain jar: a distinct node from the pom edge
		pkg("org.c:c:1", dep{spec: "org.bom:bom:1"}),
		pkg("io.n:n:1"),
	})
	res, err := ResolveTransitive(roots(coord("org.a:a:1")), []PackageSource{source}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 {
		t.Errorf("the plain and the classified jar are different keys, got conflicts %v", res.Conflicts)
	}
	var labels []string
	for _, p := range res.Packages {
		labels = append(labels, p.Coordinates.Label())
	}
	want := []string{"org.a:a:1", "org.bom:bom:1:pom", "io.n:n:1", "io.n:n:1:linux-x86_64", "org.c:c:1", "org.bom:bom:1"}
	if !reflect.DeepEqual(labels, want) {
		t.Errorf("packages = %v, want %v", labels, want)
	}
	if !res.Packages[1].Coordinates.PomOnly() || res.Packages[5].Coordinates.PomOnly() {
		t.Error("the pom edge is pom-only, the jar edge is not")
	}
}

func TestExcluded(t *testing.T) {
	c := coord("org.slf4j:slf4j-api:2")
	for pattern, want := range map[string]bool{
		"org.slf4j:slf4j-api": true,
		"org.slf4j:*":         true,
		"*:slf4j-api":         true,
		"*:*":                 true,
		"org.slf4j:jcl":       false,
		"org.other:*":         false,
	} {
		if got := excluded(c, []string{pattern}); got != want {
			t.Errorf("excluded(%s) = %v, want %v", pattern, got, want)
		}
	}
}

func TestResolveManagedRootAndTransitiveOverride(t *testing.T) {
	source := NewInMemoryPackageSource("test", []PackageMetadata{
		pkg("org.a:a:1", dep{spec: "org.b:b:1"}),
		pkg("org.b:b:1"),
		pkg("org.b:b:2"),
		pkg("org.c:c:3"),
	})
	managed := map[PackageKey]string{"org.b:b": "2", "org.c:c": "3"}
	// c declared without a version: the platform fills it; a's b:1 is
	// overridden to the managed b:2 without a conflict (Maven semantics).
	res, err := ResolveTransitive(roots(coord("org.a:a:1"), coord("org.c:c:")), []PackageSource{source}, ResolveOptions{Managed: managed})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(res.Packages); !reflect.DeepEqual(got, []string{"org.a:a:1", "org.c:c:3", "org.b:b:2"}) {
		t.Errorf("packages = %v", got)
	}
	if len(res.Conflicts) != 0 {
		t.Errorf("a managed override is not a conflict: %v", res.Conflicts)
	}
	if res.Packages[0].Managed || !res.Packages[1].Managed || !res.Packages[2].Managed {
		t.Errorf("managed flags = %v %v %v", res.Packages[0].Managed, res.Packages[1].Managed, res.Packages[2].Managed)
	}
}

func TestResolveExplicitRootBeatsPlatform(t *testing.T) {
	source := NewInMemoryPackageSource("test", []PackageMetadata{pkg("org.a:a:1"), pkg("org.a:a:2")})
	res, err := ResolveTransitive(roots(coord("org.a:a:1")), []PackageSource{source}, ResolveOptions{Managed: map[PackageKey]string{"org.a:a": "2"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(res.Packages); !reflect.DeepEqual(got, []string{"org.a:a:1"}) || res.Packages[0].Managed {
		t.Errorf("packages = %v managed=%v, want the declared a:1", got, res.Packages[0].Managed)
	}
}

func TestResolveUnmanagedRootErrors(t *testing.T) {
	source := NewInMemoryPackageSource("test", []PackageMetadata{pkg("org.a:a:1")})
	_, err := ResolveTransitive(roots(coord("org.x:x:")), []PackageSource{source}, ResolveOptions{})
	if err == nil || !strings.Contains(err.Error(), "org.x:x: no version given and no platform manages it") {
		t.Errorf("err = %v", err)
	}
}

func TestResolvePinnedRootIsNotOverriddenByPlatform(t *testing.T) {
	// cappu.json pins b:1 while the platform says b:2: the pin wins, and a's
	// transitive b:1 request is left alone, so there is no conflict either.
	source := NewInMemoryPackageSource("test", []PackageMetadata{
		pkg("org.a:a:1", dep{spec: "org.b:b:1"}),
		pkg("org.b:b:1"),
		pkg("org.b:b:2"),
	})
	res, err := ResolveTransitive(roots(coord("org.a:a:1"), coord("org.b:b:1")), []PackageSource{source},
		ResolveOptions{Managed: map[PackageKey]string{"org.b:b": "2"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(res.Packages); !reflect.DeepEqual(got, []string{"org.a:a:1", "org.b:b:1"}) || len(res.Conflicts) != 0 {
		t.Errorf("packages = %v conflicts = %v", got, res.Conflicts)
	}
}

func TestResolveRangeKeepsExtensionAndClassifier(t *testing.T) {
	source := NewInMemoryPackageSource("test", []PackageMetadata{pkg("org.a:a:1.0"), pkg("org.a:a:1.5")})
	ranged := coord("org.a:a:[1.0,2.0)").WithClassifier("tests")
	ranged.Extension = "pom"
	got, ok, err := resolveRange(ranged, []PackageSource{source})
	if err != nil || !ok || got.Label() != "org.a:a:1.5:tests:pom" {
		t.Errorf("resolveRange = %q ok=%v err=%v", got.Label(), ok, err)
	}
}

func TestUnmanagedRootErrorIsTyped(t *testing.T) {
	_, err := ResolveTransitive(roots(coord("org.x:x:")), nil, ResolveOptions{})
	var unmanaged UnmanagedRootError
	if !errors.As(err, &unmanaged) || unmanaged.Key != "org.x:x" {
		t.Errorf("err = %v, want UnmanagedRootError for org.x:x", err)
	}
}

func TestResolveRecordsEveryParentEdge(t *testing.T) {
	// a -> c and b -> c: c is resolved once (via a) but b's edge is kept, so a
	// consumer can rebuild the full graph.
	source := NewInMemoryPackageSource("test", []PackageMetadata{
		pkg("org.a:a:1", dep{spec: "org.c:c:1"}),
		pkg("org.b:b:1", dep{spec: "org.c:c:1"}),
		pkg("org.c:c:1"),
	})
	res, err := ResolveTransitive(roots(coord("org.a:a:1"), coord("org.b:b:1")), []PackageSource{source}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c := res.Packages[2]
	if c.Coordinates.String() != "org.c:c:1" || c.RequestedBy != coord("org.a:a:1") || !reflect.DeepEqual(c.AlsoRequestedBy, []Coordinates{coord("org.b:b:1")}) {
		t.Errorf("c = %+v", c)
	}
}

func TestResolveRecordsLosingEdgeOnTheWinner(t *testing.T) {
	// a -> b -> l:1 ; r -> l:2 (nearer): l:2 wins and b's edge now points at
	// it, which is what a consumer partitioning by reachability must see.
	source := NewInMemoryPackageSource("test", []PackageMetadata{
		pkg("org.a:a:1", dep{spec: "org.b:b:1"}),
		pkg("org.b:b:1", dep{spec: "org.l:l:1"}),
		pkg("org.r:r:1", dep{spec: "org.l:l:2"}),
		pkg("org.l:l:1"),
		pkg("org.l:l:2"),
	})
	res, err := ResolveTransitive(roots(coord("org.a:a:1"), coord("org.r:r:1")), []PackageSource{source}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range res.Packages {
		if p.Coordinates.Key() == "org.l:l" {
			if p.Coordinates.Version != "2" || p.RequestedBy != coord("org.r:r:1") || !reflect.DeepEqual(p.AlsoRequestedBy, []Coordinates{coord("org.b:b:1")}) {
				t.Errorf("l = %+v", p)
			}
		}
	}
}
