package format

// Golden tests for the Java formatter, sharing the fixtures with the TypeScript
// suite (test-fixtures/format). Each cases/*.input is formatted in both styles
// and compared to the checked-in baselines/<style>/*.output. The baselines are
// the real google-java-format output, so these tests measure actual
// compatibility - and that the Go port matches the TypeScript build byte for
// byte. No JDK is needed; the baselines are read from disk.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixturesRoot(t *testing.T) string {
	// togo/internal/format -> repo root.
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "test-fixtures", "format"))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestFormatGolden(t *testing.T) {
	root := fixturesRoot(t)
	casesDir := filepath.Join(root, "cases")
	entries, err := os.ReadDir(casesDir)
	if err != nil {
		t.Fatalf("read cases dir: %v", err)
	}
	styles := []string{"google", "aosp"}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".input") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".input")
		source, err := os.ReadFile(filepath.Join(casesDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, style := range styles {
			baselinePath := filepath.Join(root, "baselines", style, base+".output")
			expected, err := os.ReadFile(baselinePath)
			if err != nil {
				t.Fatalf("missing baseline %s: %v", baselinePath, err)
			}
			t.Run(base+"/"+style+"/matches", func(t *testing.T) {
				got, err := FormatSource(string(source), FormatOptions{Style: style}, "input.java")
				if err != nil {
					t.Fatalf("FormatSource: %v", err)
				}
				if got != string(expected) {
					t.Errorf("mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, expected)
				}
			})
			t.Run(base+"/"+style+"/idempotent", func(t *testing.T) {
				got, err := FormatSource(string(expected), FormatOptions{Style: style}, "input.java")
				if err != nil {
					t.Fatalf("FormatSource: %v", err)
				}
				if got != string(expected) {
					t.Errorf("not idempotent:\n--- got ---\n%s\n--- want ---\n%s", got, expected)
				}
			})
		}
	}
}

// Port of src/format/format.test.ts "array constructor references survive
// formatting": the reference parses as a class literal carrying the array type,
// and printing it as one produced "Foo[].class::new", which does not compile.
func TestArrayConstructorReferenceRoundTrip(t *testing.T) {
	source := strings.Join([]string{
		"class T {",
		"  Object f = java.util.stream.Stream.of(1).toArray(Integer[]::new);",
		"  Object g = String[][]::new;",
		"  Object h = int[]::new;",
		"  Class<?> i = Integer[].class;",
		"  java.util.function.Function<Class<?>, String> j = Integer[].class::getName;",
		"}",
		"",
	}, "\n")
	got, err := FormatSource(source, FormatOptions{Style: "google"}, "input.java")
	if err != nil {
		t.Fatalf("FormatSource: %v", err)
	}
	if got != source {
		t.Errorf("got:\n%s\nwant:\n%s", got, source)
	}
}

// Port of src/format/format.test.ts "enum constant separators stay in front of
// a trailing comment": "A(1), // one" came back as "A(1) // one,", commenting
// out the separator.
func TestEnumConstantTrailingComment(t *testing.T) {
	source := strings.Join([]string{
		"enum T {",
		"  A(1), // one",
		"  B(2); // two",
		"",
		"  private final int n;",
		"",
		"  T(int n) {",
		"    this.n = n;",
		"  }",
		"}",
		"",
	}, "\n")
	got, err := FormatSource(source, FormatOptions{Style: "google"}, "input.java")
	if err != nil {
		t.Fatalf("FormatSource: %v", err)
	}
	if got != source {
		t.Errorf("got:\n%s\nwant:\n%s", got, source)
	}
}

// Port of src/format/format.test.ts "comments inside a verbatim-printed
// declaration are not duplicated": an @interface degrades to a raw source
// slice, and its members' comments were flushed again at the end of the file.
func TestVerbatimDeclarationCommentsNotDuplicated(t *testing.T) {
	source := strings.Join([]string{
		"public @interface A {",
		"  /** doc. */",
		"  boolean on() default true;",
		"}",
		"",
	}, "\n")
	once, err := FormatSource(source, FormatOptions{Style: "google"}, "input.java")
	if err != nil {
		t.Fatalf("FormatSource: %v", err)
	}
	if n := strings.Count(once, "doc."); n != 1 {
		t.Errorf("doc. appears %d times, want 1:\n%s", n, once)
	}
	twice, err := FormatSource(once, FormatOptions{Style: "google"}, "input.java")
	if err != nil {
		t.Fatalf("FormatSource (2nd): %v", err)
	}
	if twice != once {
		t.Errorf("not idempotent:\n%s\n---\n%s", once, twice)
	}
}

// Port of src/format/format.test.ts "comment wrapping counts UTF-16 units, not
// bytes": measuring bytes wrapped every comment with a non-ASCII character a
// few columns early, so the two builds formatted real files differently.
func TestCommentWrapCountsUTF16Units(t *testing.T) {
	source := strings.Join([]string{
		"class T {",
		"  void m() {",
		"    // Euler is low-order \u2014 allow a small tolerance but assert it remains close for small dt + short time.",
		"    int x = 0;",
		"  }",
		"}",
		"",
	}, "\n")
	expected := strings.Join([]string{
		"class T {",
		"  void m() {",
		"    // Euler is low-order \u2014 allow a small tolerance but assert it remains close for small dt + short",
		"    // time.",
		"    int x = 0;",
		"  }",
		"}",
		"",
	}, "\n")
	got, err := FormatSource(source, FormatOptions{Style: "google"}, "input.java")
	if err != nil {
		t.Fatalf("FormatSource: %v", err)
	}
	if got != expected {
		t.Errorf("got:\n%s\nwant:\n%s", got, expected)
	}
}

// Port of src/format/format.test.ts "JSR-308 types, qualified inner types and
// qualified this/super survive formatting".
func TestExoticTypesAndQualifiedThisSuperRoundTrip(t *testing.T) {
	source := strings.Join([]string{
		"class T {",
		"  String @A [] @B [] arr;",
		"  Outer<Number>.B field;",
		"  Outer.@A Middle.@B Inner deep;",
		"",
		"  static class P<@A U> {",
		"    public void receiver(@F P<U> this) {}",
		"  }",
		"",
		"  class Inner {",
		"    int outer() {",
		"      return T.this.hashCode();",
		"    }",
		"",
		"    String parent() {",
		"      return T.super.toString();",
		"    }",
		"  }",
		"",
		"  static class Sub extends T.Inner {",
		"    Sub(T t) {",
		"      t.super();",
		"    }",
		"  }",
		"}",
		"",
	}, "\n")
	got, err := FormatSource(source, FormatOptions{Style: "google"}, "input.java")
	if err != nil {
		t.Fatalf("FormatSource: %v", err)
	}
	if got != source {
		t.Errorf("got:\n%s\nwant:\n%s", got, source)
	}
}

// Port of src/format/format.test.ts "a trailing comment after a nested
// initializer stays with the statement".
func TestTrailingCommentAfterNestedInitializer(t *testing.T) {
	source := strings.Join([]string{
		"class T {",
		"  void m() {",
		"    int[][] edges = {{0, 1}, {1, 2}, {2, 3}, {3, 0}}; // Even cycle",
		"  }",
		"}",
		"",
	}, "\n")
	got, err := FormatSource(source, FormatOptions{Style: "google"}, "input.java")
	if err != nil {
		t.Fatalf("FormatSource: %v", err)
	}
	if got != source {
		t.Errorf("got:\n%s\nwant:\n%s", got, source)
	}
}

// Port of src/format/format.test.ts "a leading block comment stays on its item's
// line when the list is broken".
func TestLeadingBlockCommentStaysWithItem(t *testing.T) {
	source := strings.Join([]string{
		"class T {",
		"  Object[] m() {",
		"    return new Object[] {",
		"      \"for\", \"then\", \"despite\", /* of */ \"space\", \"I\", \"would\", \"be\", \"brought\", \"from\",",
		"      \"limits\", \"far\", \"remote\", \"where\", \"thou\", \"dost\", \"stay\"",
		"    };",
		"  }",
		"}",
		"",
	}, "\n")
	once, err := FormatSource(source, FormatOptions{Style: "google"}, "input.java")
	if err != nil {
		t.Fatalf("FormatSource: %v", err)
	}
	if !strings.Contains(once, "/* of */ \"space\",") {
		t.Errorf("comment detached from its item:\n%s", once)
	}
	twice, err := FormatSource(once, FormatOptions{Style: "google"}, "input.java")
	if err != nil {
		t.Fatalf("FormatSource (2nd): %v", err)
	}
	if twice != once {
		t.Errorf("not idempotent:\n%s\n---\n%s", once, twice)
	}
}
