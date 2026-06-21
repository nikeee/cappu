package services

import (
	"testing"
	"time"

	"github.com/nikeee/cappu/internal/compiler"
)

// Port of src/compiler/termination.test.ts. Inputs that once could make the
// pipeline loop forever: each runs parse -> bind -> check -> emit -> subtype
// index -> code lenses -> completions and must complete inside a generous budget
// (a regression hangs the runner). Lives in services (it drives the service
// layer + the emitter).

const caseBudget = 10 * time.Second

var constellations = map[string]string{
	"self extends":                                 "class A extends A { void m() { m(); } }",
	"two-class extends cycle":                      "class A extends B { int x; } class B extends A { void m() { x = 1; } }",
	"interface extends cycle":                      "interface I extends J {} interface J extends I {} class C implements I { }",
	"type-parameter bound cycle":                   "class C<T extends U, U extends T> { T f; U m(T t) { return null; } }",
	"f-bounded self type":                          "class C<T extends C<T>> { T self; void m(C<?> c) { } }",
	"assignability over a cyclic hierarchy":        "class A extends B {} class B extends A {} class C { void m(A a, B b) { a = b; b = a; boolean t = a instanceof B; } }",
	"constructor chain over a cyclic hierarchy":    "class A extends B { A() { super(); } } class B extends A {} class C { void m() { new A(); } }",
	"for-each over a cyclic Iterable":              "class A extends B implements Iterable<String> {} class B extends A {} class C { void m(A a) { for (String s : a) {} } }",
	"member lookup over a cyclic hierarchy":        "class A extends B {} class B extends A { void go() {} } class C { void m(A a) { a.go(); } }",
	"implementations lens over an interface cycle": "interface I extends J { void m(); } interface J extends I {} class K implements I { public void m() {} }",
	"nested classes extending their outers":        "class A { class B extends A { class C extends B {} } }",
}

func TestTerminatesConstellations(t *testing.T) {
	for name, source := range constellations {
		t.Run(name, func(t *testing.T) {
			started := time.Now()
			program := compiler.NewProgram()
			compiler.LoadJdkStub(program)
			program.AddProjectFile("file:///T.java", source)
			sourceFile := program.GetSourceFile("file:///T.java")
			checker := compiler.NewChecker(program)
			checker.GetSemanticDiagnostics(sourceFile)
			func() {
				defer func() { _ = recover() }() // degrading on a malformed hierarchy is fine
				compiler.EmitSourceFile(sourceFile, program, checker, false)
			}()
			GetSubtypeIndex(program)
			GetCodeLenses(program, checker, sourceFile)
			GetCompletions(program, checker, sourceFile, len(source)/2, nil)
			if elapsed := time.Since(started); elapsed > caseBudget {
				t.Errorf("took %s, over budget %s", elapsed, caseBudget)
			}
		})
	}
}

// TestTerminatesParserFuzz is deterministic mutation fuzz over parse+bind: the
// parser's error recovery must always make progress.
func TestTerminatesParserFuzz(t *testing.T) {
	seeds := []string{
		"class A<T extends Comparable<? super T>> { int m(int[] a, String... s) { for (;;) { switch (a[0]) { case 1 -> m(a, s); default -> { yield; } } } } }",
		"record R(int a, String b) implements I { R { a = 1; } }",
		"class B { String s = \"\"\"\n  text block\n  \"\"\"; char c = '\\u0041'; }",
		"sealed interface I permits A, B {} non-sealed class A implements I {}",
	}
	fragments := []string{
		"", "@", "<<<<<<<", "class A { void m( { } }", "/*", "\"", "\"\"\"",
		"class A { String s = \"", "<T extends <T extends <T", "class A { { { { { ",
		")))))", "}}}}}}", "enum E { A B C }",
	}
	const parseBudget = time.Second
	insert := []rune("{}()<>;,@\"'`\\")
	var rng uint32 = 0x9e3779b9
	rand := func() float64 {
		rng = rng*1103515245 + 12345
		return float64(rng) / 4294967296.0
	}
	check := func(text string) {
		started := time.Now()
		compiler.BindSourceFile(compiler.ParseSourceFile("f.java", text))
		if elapsed := time.Since(started); elapsed > parseBudget {
			t.Fatalf("parse+bind took %s, over budget %s", elapsed, parseBudget)
		}
	}
	for _, fragment := range fragments {
		check(fragment)
	}
	for _, seed := range seeds {
		check(seed)
		for i := 0; i < 800; i++ {
			chars := []rune(seed)
			edits := 1 + int(rand()*4)
			for e := 0; e < edits; e++ {
				if len(chars) == 0 {
					break
				}
				at := int(rand() * float64(len(chars)))
				if at >= len(chars) {
					at = len(chars) - 1
				}
				op := rand()
				switch {
				case op < 0.4:
					chars = append(chars[:at], chars[at+1:]...)
				case op < 0.8:
					chars[at] = rune(32 + int(rand()*95))
				default:
					c := insert[int(rand()*float64(len(insert)))]
					chars = append(chars[:at], append([]rune{c}, chars[at:]...)...)
				}
			}
			check(string(chars))
		}
	}
}
