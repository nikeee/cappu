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
