package cli

// Port of src/cli/tree.test.ts.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/packages"
)

func treeCoord(spec string) packages.Coordinates {
	p := strings.Split(spec, ":")
	return packages.NewCoordinates(p[0], p[1], p[2])
}

func treeSource() *packages.InMemoryPackageSource {
	pkg := func(spec string, deps ...string) packages.PackageMetadata {
		decls := make([]packages.DependencyDeclaration, 0, len(deps))
		for _, d := range deps {
			decls = append(decls, packages.DependencyDeclaration{Coordinates: treeCoord(d)})
		}
		return packages.PackageMetadata{Coordinates: treeCoord(spec), Dependencies: decls}
	}
	return packages.NewInMemoryPackageSource("registry", []packages.PackageMetadata{
		pkg("org.x:app:1.0", "org.x:lib:2.0", "org.y:util:3.0"),
		pkg("org.x:lib:2.0", "org.y:util:3.0"),
		pkg("org.y:util:3.0"),
	})
}

func TestBuildForestNestsTransitiveUnderRequester(t *testing.T) {
	res, err := packages.ResolveTransitive([]packages.Coordinates{treeCoord("org.x:app:1.0")},
		[]packages.PackageSource{treeSource()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// org.y:util:3.0 is reached first via org.x:app (nearest wins), so it nests
	// there and not again under org.x:lib.
	want := []treeNode{{
		Coordinate: "org.x:app:1.0",
		Dependencies: []treeNode{
			{Coordinate: "org.x:lib:2.0", Dependencies: []treeNode{}},
			{Coordinate: "org.y:util:3.0", Dependencies: []treeNode{}},
		},
	}}
	if got := buildForest(res); !reflect.DeepEqual(got, want) {
		t.Errorf("buildForest = %+v", got)
	}
}

func TestBuildForestMarksUnresolvedRoot(t *testing.T) {
	res, err := packages.ResolveTransitive([]packages.Coordinates{treeCoord("org.z:missing:9.9")},
		[]packages.PackageSource{treeSource()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []treeNode{{Coordinate: "org.z:missing:9.9", Dependencies: []treeNode{}, Unresolved: true}}
	if got := buildForest(res); !reflect.DeepEqual(got, want) {
		t.Errorf("buildForest = %+v", got)
	}
}

func TestFormatTreeRendersIndentedSections(t *testing.T) {
	sections := []treeSection{
		{Configuration: "api", Tree: []treeNode{{
			Coordinate: "org.x:app:1.0",
			Dependencies: []treeNode{
				{Coordinate: "org.x:lib:2.0", Dependencies: []treeNode{}},
				{Coordinate: "org.y:util:3.0", Dependencies: []treeNode{}, Unresolved: true},
			},
		}}},
		{Configuration: "implementation", Tree: []treeNode{}}, // empty: skipped
	}
	want := strings.Join([]string{
		"api",
		"└── org.x:app:1.0",
		"    ├── org.x:lib:2.0",
		"    └── org.y:util:3.0 (unresolved)",
		"",
	}, "\n")
	if got := FormatTree(sections, plainPaint); got != want {
		t.Errorf("FormatTree = %q", got)
	}
}

func TestFormatTreeNothingDeclared(t *testing.T) {
	got := FormatTree([]treeSection{{Configuration: "api", Tree: []treeNode{}}}, plainPaint)
	if got != "no dependencies declared\n" {
		t.Errorf("FormatTree = %q", got)
	}
}
