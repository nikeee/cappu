package compiler

// Port of src/compiler/nullness.test.ts.

import (
	"testing"

	"github.com/nikeee/cappu/internal/config"
)

const codeNullIntoNonNull = 1307

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
		out = append(out, d.Code)
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
		out = append(out, d.Code)
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
