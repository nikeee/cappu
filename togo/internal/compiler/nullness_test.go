package compiler

// Port of src/compiler/nullness.test.ts.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nikeee/cappu/internal/config"
)

const codeNullIntoNonNull = 1307
const codeDeref = 1308

func jspecify() *config.Nullness {
	return &config.Nullness{
		Enabled:                 true,
		NullableAnnotations:     []string{"org.jspecify.annotations.Nullable"},
		NonNullAnnotations:      []string{"org.jspecify.annotations.NonNull"},
		NullMarkedAnnotations:   []string{"org.jspecify.annotations.NullMarked"},
		NullUnmarkedAnnotations: []string{"org.jspecify.annotations.NullUnmarked"},
	}
}

func diagnoseNullness(text string, nullness *config.Nullness) []int {
	program := NewProgram()
	LoadJdkStub(program)
	program.SetOpenDocument("file:///T.java", text, 1)
	checker := NewChecker(program, nullness)
	var out []int
	for _, d := range checker.GetSemanticDiagnostics(program.GetSourceFile("file:///T.java")) {
		out = append(out, int(d.Code))
	}
	return out
}

func TestNullnessFlagged(t *testing.T) {
	cases := []struct{ name, code string }{
		{"null into @NonNull parameter", "class C { void f(@NonNull String s) {} void g() { f(null); } }"},
		{"null into @NonNull field", "class C { @NonNull String x = null; }"},
		{"null from @NonNull method", "class C { @NonNull String f() { return null; } }"},
		{"null assigned to @NonNull field", "class C { @NonNull String x = \"a\"; void g() { x = null; } }"},
		{"@NullMarked unannotated parameter rejects null", "@NullMarked class C { void f(String s) {} void g() { f(null); } }"},
		{"@NullMarked unannotated local rejects null", "@NullMarked class C { void g() { String s = null; } }"},
		{"@Nullable return into @NonNull parameter", "class C { @Nullable String n() { return null; } void f(@NonNull String s) {} void g() { f(n()); } }"},
		{"new R(null) into a non-null record component", "@NullMarked record R(String x) {}\nclass C { void m() { new R(null); } }"},
		{"null assigned to a non-null field in a constructor", "@NullMarked class C { String f; C() { this.f = null; } }"},
		{"null assigned to a non-null local in a branch", `@NullMarked class C { boolean c() { return true; } void m() { String x = "foo"; if (c()) x = null; x.length(); } }`},
		{"ternary with a null arm into @NonNull param", `@NullMarked class C { void f(String s) {} void g(boolean b) { f(b ? null : "x"); } }`},
		{"ternary with a @Nullable arm into @NonNull param", `@NullMarked class C { @Nullable String n() { return null; } void f(String s) {} void g(boolean b) { f(b ? n() : "x"); } }`},
		{"cast of null into @NonNull param", "@NullMarked class C { void f(String s) {} void g() { f((String) null); } }"},
		{"null element in a non-null array initializer", "@NullMarked class C { void g() { String[] a = { null }; } }"},
		{"List<@Nullable String> into a non-null loop variable", "import java.util.List; @NullMarked class C { void g(List<@Nullable String> xs) { for (String s : xs) {} } }"},
		{"expression-bodied lambda returning null into a non-null SAM", "import java.util.function.Supplier; @NullMarked class C { Supplier<String> s = () -> null; }"},
		{"block-bodied lambda returning null into a non-null SAM", "import java.util.function.Supplier; @NullMarked class C { Supplier<String> s = () -> { return null; }; }"},
	}
	for _, tc := range cases {
		if !containsCode(diagnoseNullness(tc.code, jspecify()), codeNullIntoNonNull) {
			t.Errorf("%s: expected a nullness warning for %q", tc.name, tc.code)
		}
	}
}

func TestNullnessAccepted(t *testing.T) {
	cases := []struct{ name, code string }{
		{"@Nullable parameter accepts null", "class C { void f(@Nullable String s) {} void g() { f(null); } }"},
		{"@NullMarked @Nullable parameter accepts null", "@NullMarked class C { void f(@Nullable String s) {} void g() { f(null); } }"},
		{"@NullUnmarked method opts out", "@NullMarked class C { @NullUnmarked void f(String s) {} void g() { f(null); } }"},
		{"plain parameter outside @NullMarked accepts null", "class C { void f(String s) {} void g() { f(null); } }"},
		{"non-null value into @NonNull parameter", "class C { @NonNull String n() { return \"a\"; } void f(@NonNull String s) {} void g() { f(n()); } }"},
		{"new R(null) into a @Nullable record component", "record R(@Nullable String x) {}\nclass C { void m() { new R(null); } }"},
		{"non-null arg into a non-null record component", "@NullMarked record R(String x) {}\nclass C { void m() { new R(\"a\"); } }"},
		{"ternary with both arms non-null", `@NullMarked class C { void f(String s) {} void g(boolean b) { f(b ? "y" : "x"); } }`},
		{"returning a value narrowed by an early-return", `@NullMarked class C { String g(@Nullable String x) { if (x == null) return ""; return x; } }`},
		{"ternary with both arms non-null as an argument", `@NullMarked class C { void f(String s) {} void g(boolean b) { f(b ? "y" : "x"); } }`},
		{"non-null array initializer", `@NullMarked class C { void g() { String[] a = { "x", "y" }; } }`},
		{"List<String> into a non-null loop variable", "import java.util.List; @NullMarked class C { void g(List<String> xs) { for (String s : xs) s.trim(); } }"},
		{"lambda returning null into a @Nullable SAM element", "import java.util.function.Supplier; @NullMarked class C { Supplier<@Nullable String> s = () -> null; }"},
		{"lambda returning a non-null value into a non-null SAM", `import java.util.function.Supplier; @NullMarked class C { Supplier<String> s = () -> "x"; }`},
	}
	for _, tc := range cases {
		if containsCode(diagnoseNullness(tc.code, jspecify()), codeNullIntoNonNull) {
			t.Errorf("%s: should NOT flag %q", tc.name, tc.code)
		}
	}
}

func TestNullnessDisabled(t *testing.T) {
	code := "class C { void f(@NonNull String s) {} void g() { f(null); } }"
	if containsCode(diagnoseNullness(code, nil), codeNullIntoNonNull) {
		t.Error("nullness checks should be off when no options are passed")
	}
	off := jspecify()
	off.Enabled = false
	if containsCode(diagnoseNullness(code, off), codeNullIntoNonNull) {
		t.Error("nullness checks should be off when enabled is false")
	}
}

func TestNullnessCustomAnnotationList(t *testing.T) {
	code := "class C { void f(@Nonnull String s) {} void g() { f(null); } }"
	cfg := jspecify()
	cfg.NonNullAnnotations = []string{"javax.annotation.Nonnull"}
	if !containsCode(diagnoseNullness(code, cfg), codeNullIntoNonNull) {
		t.Error("a custom non-null annotation list (JSR-305) should be honored")
	}
}

const boxClass = "class Box<T> { void put(T t) {} T get() { return get(); } }\n"

func TestNullnessGenericFlagged(t *testing.T) {
	cases := []struct{ name, code string }{
		{"null into Box<@NonNull String>.put", boxClass + "class C { void g(Box<@NonNull String> b) { b.put(null); } }"},
		{"@NullMarked Box<String> element rejects null", boxClass + "@NullMarked class C { void g(Box<String> b) { b.put(null); } }"},
		{"@Nullable generic element from get into @NonNull param", boxClass + "class C { void f(@NonNull String s) {} void g(Box<@Nullable String> b) { f(b.get()); } }"},
	}
	for _, tc := range cases {
		if !containsCode(diagnoseNullness(tc.code, jspecify()), codeNullIntoNonNull) {
			t.Errorf("%s: expected a nullness warning for %q", tc.name, tc.code)
		}
	}
}

func TestNullnessGenericAccepted(t *testing.T) {
	cases := []struct{ name, code string }{
		{"null into Box<@Nullable String>.put", boxClass + "class C { void g(Box<@Nullable String> b) { b.put(null); } }"},
		{"non-null value into Box<@NonNull String>.put", boxClass + "class C { void g(Box<@NonNull String> b) { b.put(\"x\"); } }"},
	}
	for _, tc := range cases {
		if containsCode(diagnoseNullness(tc.code, jspecify()), codeNullIntoNonNull) {
			t.Errorf("%s: should NOT flag %q", tc.name, tc.code)
		}
	}
}

func diagnoseFilesNullness(files map[string]string, target string) []int {
	program := NewProgram()
	LoadJdkStub(program)
	for uri, text := range files {
		program.SetOpenDocument(URI(uri), text, 1)
	}
	checker := NewChecker(program, jspecify())
	var out []int
	for _, d := range checker.GetSemanticDiagnostics(program.GetSourceFile(URI(target))) {
		out = append(out, int(d.Code))
	}
	return out
}

func TestNullnessPackageInfo(t *testing.T) {
	marked := diagnoseFilesNullness(map[string]string{
		"file:///p/package-info.java": "@NullMarked package p;",
		"file:///p/C.java":            "package p; class C { void f(String s) {} void g() { f(null); } }",
	}, "file:///p/C.java")
	if !containsCode(marked, codeNullIntoNonNull) {
		t.Error("@NullMarked in package-info.java should mark another file of the package")
	}
	unmarked := diagnoseFilesNullness(map[string]string{
		"file:///p/package-info.java": "package p;",
		"file:///p/C.java":            "package p; class C { void f(String s) {} void g() { f(null); } }",
	}, "file:///p/C.java")
	if containsCode(unmarked, codeNullIntoNonNull) {
		t.Error("without a @NullMarked package-info.java the code should not be flagged")
	}
}

// --- additional coverage -----------------------------------------------------------

func TestNullnessMoreFlagged(t *testing.T) {
	cases := []struct{ name, code string }{
		{"null into a @NonNull constructor parameter",
			"class Foo { Foo(@NonNull String s) {} }\nclass C { void g() { new Foo(null); } }"},
		{"null reassigned to a @NonNull local",
			"class C { void g() { @NonNull String x = \"a\"; x = null; } }"},
		{"a @Nullable field passed to a @NonNull parameter",
			"class C { @Nullable String fld; void f(@NonNull String s) {} void g() { f(fld); } }"},
		{"a @Nullable return initializing a @NonNull local",
			"class C { @Nullable String n() { return n(); } void g() { @NonNull String x = n(); } }"},
		{"in-file @NullMarked package marks an unannotated parameter",
			"@NullMarked package p;\nclass C { void f(String s) {} void g() { f(null); } }"},
		{"@NullMarked on an enclosing type marks a nested type's parameter",
			"@NullMarked class Outer { static class Inner { void f(String s) {} void g() { f(null); } } }"},
	}
	for _, tc := range cases {
		if !containsCode(diagnoseNullness(tc.code, jspecify()), codeNullIntoNonNull) {
			t.Errorf("%s: expected a nullness warning for %q", tc.name, tc.code)
		}
	}
}

func TestNullnessMoreAccepted(t *testing.T) {
	cases := []struct{ name, code string }{
		{"a non-null value into a @NonNull constructor parameter",
			"class Foo { Foo(@NonNull String s) {} }\nclass C { void g() { new Foo(\"a\"); } }"},
		{"a varargs @NonNull parameter is not checked (array, not element)",
			"class C { void f(@NonNull String... xs) {} void g() { f(null); } }"},
		{"a @NonNull field read passed to a @NonNull parameter",
			"class C { @NonNull String fld = \"a\"; void f(@NonNull String s) {} void g() { f(fld); } }"},
		{"@NullUnmarked on a type opts out of a @NullMarked package",
			"@NullMarked package p;\n@NullUnmarked class C { void f(String s) {} void g() { f(null); } }"},
	}
	for _, tc := range cases {
		if containsCode(diagnoseNullness(tc.code, jspecify()), codeNullIntoNonNull) {
			t.Errorf("%s: should NOT flag %q", tc.name, tc.code)
		}
	}
}

func TestNullnessGenericDisabled(t *testing.T) {
	code := boxClass + "class C { void g(Box<@NonNull String> b) { b.put(null); } }"
	if containsCode(diagnoseNullness(code, nil), codeNullIntoNonNull) {
		t.Error("generic nullness should be off when no options are passed")
	}
}

func TestNullnessCustomNullableList(t *testing.T) {
	// A project using JSR-305 @CheckForNull as its @Nullable marker.
	code := "class C { @CheckForNull String n() { return n(); } void f(@NonNull String s) {} void g() { f(n()); } }"
	cfg := jspecify()
	cfg.NullableAnnotations = []string{"javax.annotation.CheckForNull"}
	if !containsCode(diagnoseNullness(code, cfg), codeNullIntoNonNull) {
		t.Error("a custom @Nullable annotation list should be honored")
	}
}

// narrowBody splices a statement body in around a @Nullable local x, with non-null sinks.
func narrowBody(body string) string {
	return "import java.util.Objects;\n" +
		"class C {\n" +
		"  void f(@NonNull String s) {}\n" +
		"  boolean ok(@NonNull String s) { return true; }\n" +
		"  String use(@NonNull String s) { return s; }\n" +
		"  void h(@NonNull Object o) {}\n" +
		"  @Nullable String src() { return src(); }\n" +
		"  void m() { @Nullable String x = src(); " + body + " }\n" +
		"}"
}

func TestNullnessNarrowingAccepted(t *testing.T) {
	cases := []struct{ name, body string }{
		{"if (x != null) guard", "if (x != null) { f(x); }"},
		{"early-return on null", "if (x == null) return; f(x);"},
		{"&& short-circuit", "boolean b = x != null && ok(x);"},
		{"|| short-circuit", "boolean b = x == null || ok(x);"},
		{"ternary whenTrue", "String r = x != null ? use(x) : \"\";"},
		{"instanceof", "if (x instanceof String) { h(x); }"},
		{"Objects.requireNonNull", "Objects.requireNonNull(x); f(x);"},
		{"assert x != null", "assert x != null; f(x);"},
		{"reassign to non-null", "x = \"y\"; f(x);"},
	}
	for _, tc := range cases {
		if containsCode(diagnoseNullness(narrowBody(tc.body), jspecify()), codeNullIntoNonNull) {
			t.Errorf("%s: narrowing should suppress the warning for %q", tc.name, tc.body)
		}
	}
}

func TestNullnessNarrowingFlagged(t *testing.T) {
	cases := []struct{ name, body string }{
		{"use before the guard", "f(x); if (x != null) {}"},
		{"reassign to null then use", "if (x == null) return; x = null; f(x);"},
		{"wrong branch (then of == null)", "if (x == null) { f(x); }"},
		{"reassignment between guard and use", "if (x != null) { x = src(); f(x); }"},
	}
	for _, tc := range cases {
		if !containsCode(diagnoseNullness(narrowBody(tc.body), jspecify()), codeNullIntoNonNull) {
			t.Errorf("%s: expected a warning for %q", tc.name, tc.body)
		}
	}
}

func TestNullnessNarrowingConditionForms(t *testing.T) {
	cases := []struct{ name, body string }{
		{"negation !(x == null)", "if (!(x == null)) { f(x); }"},
		{"else of (x == null)", "if (x == null) {} else { f(x); }"},
		{"Objects.nonNull condition", "if (Objects.nonNull(x)) { f(x); }"},
		{"Objects.isNull else-branch", "if (Objects.isNull(x)) {} else { f(x); }"},
		{"null on the left", "if (null != x) { f(x); }"},
		{"early-exit via throw", "if (x == null) throw new RuntimeException(); f(x);"},
		{"early-exit via break", "for (;;) { if (x == null) break; f(x); }"},
		{"early-exit via continue", "for (;;) { if (x == null) continue; f(x); }"},
		{"block-bodied early-exit", "if (x == null) { System.out.println(); return; } f(x);"},
		{"ternary whenFalse arm", "String r = x == null ? \"\" : use(x);"},
		{"&&-chain of three", "@Nullable String y = src(); boolean b = y != null && x != null && ok(x);"},
		{"requireNonNull with message", "Objects.requireNonNull(x, \"m\"); f(x);"},
		{"assert with message", "assert x != null : \"m\"; f(x);"},
	}
	for _, tc := range cases {
		if containsCode(diagnoseNullness(narrowBody(tc.body), jspecify()), codeNullIntoNonNull) {
			t.Errorf("%s: narrowing should suppress the warning for %q", tc.name, tc.body)
		}
	}
}

func TestNullnessLoopNarrowing(t *testing.T) {
	accepted := []struct{ name, body string }{
		{"while-loop condition", "while (x != null) { f(x); break; }"},
		{"for-loop condition", "for (; x != null; ) { f(x); break; }"},
	}
	for _, tc := range accepted {
		if containsCode(diagnoseNullness(narrowBody(tc.body), jspecify()), codeNullIntoNonNull) {
			t.Errorf("%s: should narrow x in the body of %q", tc.name, tc.body)
		}
	}
	flagged := []struct{ name, body string }{
		{"do-while body runs once first", "do { f(x); break; } while (x != null);"},
		{"reassignment inside loop body", "while (x != null) { x = src(); f(x); break; }"},
	}
	for _, tc := range flagged {
		if !containsCode(diagnoseNullness(narrowBody(tc.body), jspecify()), codeNullIntoNonNull) {
			t.Errorf("%s: expected a warning for %q", tc.name, tc.body)
		}
	}
}

func TestNullnessBranchMerge(t *testing.T) {
	accepted := []struct{ name, body string }{
		{"if (x==null) x=default;", "if (x == null) x = \"d\"; f(x);"},
		{"if (x==null) { x=default; }", "if (x == null) { x = \"d\"; } f(x);"},
	}
	for _, tc := range accepted {
		if containsCode(diagnoseNullness(narrowBody(tc.body), jspecify()), codeNullIntoNonNull) {
			t.Errorf("%s: branch-merge should suppress the warning for %q", tc.name, tc.body)
		}
	}
	// Assigning a possibly-null value in the branch cannot prove non-null.
	flagged := "if (x == null) x = src(); f(x);"
	if !containsCode(diagnoseNullness(narrowBody(flagged), jspecify()), codeNullIntoNonNull) {
		t.Errorf("expected a warning for %q", flagged)
	}
}

func TestNullnessFieldNotNarrowed(t *testing.T) {
	code := "class C { @Nullable String fld; void f(@NonNull String s) {} void m() { if (fld != null) { f(fld); } } }"
	if !containsCode(diagnoseNullness(code, jspecify()), codeNullIntoNonNull) {
		t.Error("fields must not be narrowed by a guard")
	}
}

func TestNullnessDereferenceFlagged(t *testing.T) {
	cases := []struct{ name, code string }{
		{"method on @Nullable receiver", "class C { void m(@Nullable String x) { x.trim(); } }"},
		{"field on @Nullable receiver", "class A { int v; }\nclass C { void m(@Nullable A a) { int n = a.v; } }"},
		{"index of @Nullable array", "class C { void m(@Nullable String[] arr) { String s = arr[0]; } }"},
		{"throw of @Nullable value", "class C { void m(@Nullable RuntimeException e) { throw e; } }"},
		{"synchronized on @Nullable lock", "class C { void m(@Nullable Object lock) { synchronized (lock) {} } }"},
		{"enhanced-for over @Nullable collection", "class C { void m(@Nullable java.util.List<String> xs) { for (String s : xs) {} } }"},
		{"switch on @Nullable selector without case null", `class C { void m(@Nullable String x) { switch (x) { case "a" -> {} default -> {} } } }`},
		{"switch expression on @Nullable selector without case null", `class C { int m(@Nullable String x) { return switch (x) { case "a" -> 1; default -> 0; }; } }`},
		{"non-final @Nullable field not narrowed by a guard", "class C { @Nullable String f = null; void m() { if (this.f != null) this.f.trim(); } }"},
		{"final @Nullable field dereferenced without a guard", "class C { final @Nullable String f = null; void m() { this.f.trim(); } }"},
		{"guard on one receiver does not narrow another receiver's final field", "class C { final @Nullable String f = null; void m(C a, C b) { if (a.f != null) b.f.trim(); } }"},
		{"catch falls through after requireNonNull", "class C { void m(@Nullable String x) { try { java.util.Objects.requireNonNull(x); } catch (RuntimeException e) {} x.trim(); } }"},
		{"assignment in try with fall-through catch", `class C { void m(@Nullable String x) { try { x = "a"; } catch (RuntimeException e) {} x.trim(); } }`},
		{"chained call on a @Nullable return", "@NullMarked class C { @Nullable String n() { return null; } void g() { n().trim(); } }"},
		{"element of a @Nullable[] dereferenced", "@NullMarked class C { void g(@Nullable String[] a) { a[0].trim(); } }"},
		{"static @Nullable field dereferenced", "@NullMarked class C { static @Nullable String F = null; void g() { F.trim(); } }"},
		{"var that infers a @Nullable type", "@NullMarked class C { @Nullable String n() { return null; } void g() { var x = n(); x.trim(); } }"},
		{"chained field access through a @Nullable field", "@NullMarked class C { @Nullable C a; final @Nullable String b = null; void g() { this.a.b.trim(); } }"},
		{"conditional reassignment to null defeats an earlier narrowing", `@NullMarked class C { boolean c() { return true; } void m() { @Nullable String x = "foo"; if (c()) x = null; x.length(); } }`},
		{"dereferencing a ternary whose arm is null", `@NullMarked class C { void g(boolean b) { (b ? null : "x").trim(); } }`},
		{"without case null, selector still possibly-null in arm", `@NullMarked class C { void g(@Nullable String x) { switch (x) { case "a" -> {} default -> x.trim(); } } }`},
		{"reassignment to null inside a switch arm", "@NullMarked class C { void g(@Nullable String x) { switch (x) { case null -> {} default -> { x = null; x.trim(); } } } }"},
	}
	for _, tc := range cases {
		if !containsCode(diagnoseNullness(tc.code, jspecify()), codeDeref) {
			t.Errorf("%s: expected a dereference warning for %q", tc.name, tc.code)
		}
	}
}

func TestNullnessDereferenceAccepted(t *testing.T) {
	cases := []struct{ name, code string }{
		{"guard narrows the receiver", "class C { void m(@Nullable String x) { if (x != null) x.trim(); } }"},
		{"early-return narrows the receiver", "class C { void m(@Nullable String x) { if (x == null) return; x.trim(); } }"},
		{"@NonNull receiver", "class C { void m(@NonNull String x) { x.trim(); } }"},
		{"this-qualified access", "class C { String fld; void m() { this.fld.trim(); } }"},
		{"guard narrows a thrown @Nullable value", "class C { void m(@Nullable RuntimeException e) { if (e != null) throw e; } }"},
		{"case null (JEP 441) handles a @Nullable switch selector", `class C { void m(@Nullable String x) { switch (x) { case null -> {} case "a" -> {} default -> {} } } }`},
		{"case null narrows the selector in the default arm", "@NullMarked class C { void g(@Nullable String x) { switch (x) { case null -> {} default -> x.trim(); } } }"},
		{"case null narrows the selector in a value-case arm", `@NullMarked class C { void g(@Nullable String x) { switch (x) { case null -> {} case "a" -> x.trim(); default -> {} } } }`},
		{"guard narrows a final @Nullable field via this.f", "class C { final @Nullable String f = null; void m() { if (this.f != null) this.f.trim(); } }"},
		{"guard narrows a final @Nullable field via bare f", "class C { final @Nullable String f = null; void m() { if (f != null) f.trim(); } }"},
		{"guard narrows a final @Nullable record component", "record R(@Nullable String name) { void m() { if (this.name != null) this.name.trim(); } }"},
		{"requireNonNull in a try whose catch exits", "class C { void m(@Nullable String x) { try { java.util.Objects.requireNonNull(x); } catch (RuntimeException e) { return; } x.trim(); } }"},
		{"assignment in a try whose catch exits", `class C { void m(@Nullable String x) { try { x = "a"; } catch (RuntimeException e) { return; } x.trim(); } }`},
		{"guard before a try narrows inside the try body", "class C { void m(@Nullable String x) { if (x == null) return; try { x.trim(); } catch (RuntimeException e) {} } }"},
		{"var that infers @Nullable narrowed by a guard", "@NullMarked class C { @Nullable String n() { return null; } void g() { var x = n(); if (x != null) x.trim(); } }"},
		{"instanceof pattern variable is non-null", "@NullMarked class C { void g(@Nullable Object o) { if (o instanceof String s) s.length(); } }"},
		{"Objects.isNull early-return guard", "import java.util.Objects; @NullMarked class C { void g(@Nullable String x) { if (Objects.isNull(x)) return; x.trim(); } }"},
		{"negated null check early-return guard", "@NullMarked class C { void g(@Nullable String x) { if (!(x != null)) return; x.trim(); } }"},
		{"&& chain narrows the receiver", "@NullMarked class C { void g(@Nullable String x) { if (x != null && x.length() > 0) x.trim(); } }"},
		{"string concatenation with a @Nullable value", `@NullMarked class C { String g(@Nullable String x) { return "a" + x; } }`},
	}
	for _, tc := range cases {
		if containsCode(diagnoseNullness(tc.code, jspecify()), codeDeref) {
			t.Errorf("%s: should NOT flag a dereference for %q", tc.name, tc.code)
		}
	}
	if containsCode(diagnoseNullness("class C { void m(@Nullable String x) { x.trim(); } }", nil), codeDeref) {
		t.Error("dereference checks should be off when nullness is disabled")
	}
}

func TestNullnessExampleApp(t *testing.T) {
	main, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "nullness-app",
		"src", "main", "java", "example", "Main.java"))
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	n := 0
	for _, c := range diagnoseNullness(string(main), jspecify()) {
		if c == codeNullIntoNonNull {
			n++
		}
	}
	// shout(lookup("greeting")) is the single intended warning; the narrowed branch stays quiet.
	if n != 1 {
		t.Errorf("examples/nullness-app: expected exactly 1 nullness warning, got %d", n)
	}
}

func TestNullnessNestedPackage(t *testing.T) {
	// @NullMarked package-info applies only to its exact package, not a parent of it.
	codes := diagnoseFilesNullness(map[string]string{
		"file:///a/b/package-info.java": "@NullMarked package a.b;",
		"file:///a/C.java":              "package a; class C { void f(String s) {} void g() { f(null); } }",
	}, "file:///a/C.java")
	if containsCode(codes, codeNullIntoNonNull) {
		t.Error("a @NullMarked sub-package must not mark its parent package")
	}
}
