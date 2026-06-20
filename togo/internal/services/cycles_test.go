package services

import (
	"testing"
	"time"

	"github.com/nikeee/cappu/internal/compiler"
)

// Port of src/compiler/cycles.test.ts. Cyclic inheritance and cyclic generics -
// direct and indirect - must never loop: each case runs the CHECKER
// (diagnostics, symbol types), the RESOLVER (identifier resolution, member
// lookup, references) and HOVER over every identifier of the cyclic source. The
// assertion is termination (a regression hangs the runner); the budget
// documents intent. Lives in services (not compiler) because it exercises
// GetHoverText. The broader pipeline counterpart is termination_test.go.

const cyclesBudget = 10 * time.Second // generous for CI; healthy runs take ~100ms

var cyclesCases = map[string]string{
	// inheritance, direct
	"class extends itself":             "class A extends A { int f; void m() { m(); int x = f; } }",
	"two classes extending each other": "class A extends B { void onA() {} } class B extends A { void onB() { onA(); } }",
	"interface extending itself":       "interface I extends I { void m(); } class C implements I { public void m() {} }",
	// inheritance, indirect
	"three-class extends cycle":     "class A extends B { int a; } class B extends C { int b; } class C extends A { int m() { return a + b; } }",
	"three-interface extends cycle": "interface I extends J {} interface J extends K {} interface K extends I { void go(); } class C implements I { public void go() {} }",
	"class/interface mixed cycle":   "interface I extends J {} interface J extends I {} class A extends B implements I {} class B extends A { void m(A a, I i) { } }",
	// generics, direct
	"type parameter bounded by itself":                  "class G<T extends T> { T value; T id(T t) { return value; } }",
	"f-bounded type parameter":                          "class G<T extends G<T>> { T self; T next() { return self; } }",
	"generic class extending itself with new arguments": "class G<T> extends G<G<T>> { T value; void m(G<String> g) { } }",
	// generics, indirect
	"mutually bounded type parameters":      "class C<T extends U, U extends T> { T t; U u; void m() { t = u; u = t; } }",
	"mutually f-bounded classes":            "class X<T extends Y<T>> { T y; } class Y<U extends X<U>> { U x; void m(Y<?> y) { } }",
	"parameterized two-class extends cycle": "class P<T> extends Q<T> { T p; } class Q<T> extends P<T> { T q; void m() { p = q; } }",
}

func TestCycleSafe(t *testing.T) {
	for name, source := range cyclesCases {
		t.Run(name, func(t *testing.T) {
			started := time.Now()
			program := compiler.NewProgram()
			compiler.LoadJdkStub(program)
			program.SetOpenDocument("file:///T.java", source, 1)
			sf := program.GetSourceFile("file:///T.java")
			checker := compiler.NewChecker(program)

			// checker: all diagnostics over the cyclic declarations
			checker.GetSemanticDiagnostics(sf)

			// collect every identifier in the file
			var identifiers []*compiler.Node
			var collect func(*compiler.Node)
			collect = func(node *compiler.Node) {
				if node.Kind == compiler.Identifier {
					identifiers = append(identifiers, node)
				}
				node.ForEachChild(func(child *compiler.Node) bool {
					collect(child)
					return false
				})
			}
			collect(sf)
			if len(identifiers) == 0 {
				t.Fatal("expected at least one identifier")
			}

			// resolver + checker + hover over EVERY identifier
			for _, id := range identifiers {
				compiler.ResolveIdentifier(id, program)
				symbol := checker.ResolveName(id)
				if symbol == nil {
					continue
				}
				checker.GetTypeOfSymbol(symbol)
				checker.TypeStringOfSymbol(symbol)
				GetHoverText(checker, symbol, id) // renders bounds/signatures
				if symbol.Flags&compiler.SymbolFlagsType != 0 {
					compiler.LookupMember(symbol, "toString", compiler.MeaningAny, program)
					compiler.LookupMember(symbol, "no_such_member", compiler.MeaningAny, program)
				}
				compiler.FindReferences(symbol, program, checker.ResolveName)
			}

			if elapsed := time.Since(started); elapsed > cyclesBudget {
				t.Errorf("took %s, over budget %s", elapsed, cyclesBudget)
			}
		})
	}
}
