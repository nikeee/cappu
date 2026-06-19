package services

import (
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/compiler"
)

// Port of src/services/codeLens.test.ts.

type lensResult struct {
	name, kind string
	count      int
}

func codeLenses(files map[string]string, lensFile string) []lensResult {
	program := compiler.NewProgram()
	compiler.LoadJdkStub(program)
	for name, text := range files {
		program.AddProjectFile(compiler.URI("file:///"+name), text)
	}
	checker := compiler.NewChecker(program)
	sourceFile := program.GetSourceFile(compiler.URI("file:///" + lensFile))
	var out []lensResult
	for _, e := range GetCodeLenses(program, checker, sourceFile) {
		out = append(out, lensResult{name: e.Name.AsIdentifier().Text, kind: e.Kind, count: len(e.Sites)})
	}
	return out
}

func refCounts(out []lensResult) map[string]int {
	m := map[string]int{}
	for _, e := range out {
		if e.kind == "references" {
			m[e.name] = e.count
		}
	}
	return m
}

func implCounts(out []lensResult) (map[string]int, map[string]bool) {
	m := map[string]int{}
	present := map[string]bool{}
	for _, e := range out {
		if e.kind == "implementations" {
			m[e.name] = e.count
			present[e.name] = true
		}
	}
	return m, present
}

func TestCrossFileReferenceCounts(t *testing.T) {
	out := codeLenses(map[string]string{
		"Pet.java":    strings.Join([]string{"package zoo;", "public class Pet {", "  public int legs() { return 4; }", "  void unused() {}", "}"}, "\n"),
		"Keeper.java": strings.Join([]string{"package zoo;", "class Keeper {", "  int count(Pet a, Pet b) { return a.legs() + b.legs(); }", "}"}, "\n"),
	}, "Pet.java")
	refs := refCounts(out)
	if refs["Pet"] != 2 || refs["legs"] != 2 || refs["unused"] != 0 {
		t.Errorf("refs = %v", refs)
	}
}

func TestInFileReferences(t *testing.T) {
	out := codeLenses(map[string]string{
		"C.java": strings.Join([]string{"class C {", "  int twice(int x) { return x * 2; }", "  int m() { return twice(1) + twice(2); }", "}"}, "\n"),
	}, "C.java")
	refs := refCounts(out)
	if refs["twice"] != 2 || refs["m"] != 0 {
		t.Errorf("refs = %v", refs)
	}
}

func TestInterfaceImplementationCounts(t *testing.T) {
	out := codeLenses(map[string]string{
		"Shape.java": strings.Join([]string{"package geo;", "public interface Shape {", "  double area();", "  default String label() { return \"shape\"; }", "}"}, "\n"),
		"Impls.java": strings.Join([]string{"package geo;", "class Circle implements Shape { public double area() { return 3.14; } }", "class Square implements Shape { public double area() { return 1.0; } }", "interface Polygon extends Shape {}"}, "\n"),
	}, "Shape.java")
	impls, present := implCounts(out)
	if impls["Shape"] != 3 || impls["area"] != 2 {
		t.Errorf("impls = %v", impls)
	}
	if present["label"] {
		t.Error("default methods should get no implementations lens")
	}
}

func TestAbstractClassImplementationCounts(t *testing.T) {
	out := codeLenses(map[string]string{
		"Base.java": strings.Join([]string{"abstract class Base {", "  abstract int weight();", "  int common() { return 0; }", "}", "class Heavy extends Base { int weight() { return 100; } }"}, "\n"),
	}, "Base.java")
	impls, present := implCounts(out)
	if impls["Base"] != 1 || impls["weight"] != 1 {
		t.Errorf("impls = %v", impls)
	}
	if present["common"] {
		t.Error("concrete methods should get no implementations lens")
	}
}

func TestTransitiveImplementationCounts(t *testing.T) {
	out := codeLenses(map[string]string{
		"I.java":    "interface I { int f(); }",
		"Mid.java":  "abstract class Mid implements I {}",
		"Leaf.java": "class Leaf extends Mid { public int f() { return 1; } }",
	}, "I.java")
	impls, _ := implCounts(out)
	if impls["I"] != 2 || impls["f"] != 1 {
		t.Errorf("impls = %v", impls)
	}
}
