package format

// Golden tests for the Java formatter, sharing the fixtures with the TypeScript
// suite (test-fixtures/format). Each cases/*.input is formatted in both styles
// and compared to the checked-in baselines/<style>/*.output. The baselines are
// the real google-java-format output, so these tests measure actual
// compatibility - and that the Go port matches the TypeScript build byte for
// byte. No JDK is needed; the baselines are read from disk.
//
// A baseline is only ever (re)written by running the REAL google-java-format,
// which needs a jar. Either point GJF_JAR at the all-deps jar:
//
//	GJF_JAR=/path/to/google-java-format-all-deps.jar \
//	  UPDATE_BASELINES=1 go test ./internal/format/
//
// or, when only the maven repo jar is present (it is not all-deps), point
// GJF_CP at a resolved classpath (mvn dependency:build-classpath
// -Dmdep.outputFile=cp.txt):
//
//	GJF_CP=$(cat cp.txt) UPDATE_BASELINES=1 go test ./internal/format/
//
// Download the jar from https://github.com/google/google-java-format/releases.
// Port of src/format/format.test.ts. One deliberate deviation from it: when
// UPDATE_BASELINES=1 is set but neither variable is, a present baseline is
// asserted against instead of failing the run, so `UPDATE_BASELINES=1 go test
// ./...` stays usable without a jar. A MISSING baseline with no jar is still
// fatal - there is nothing to compare against.
//
// Regeneration is gjf-version-sensitive, so always review the diff: with gjf
// 1.34.1 the 72-text-block-deindent and 73-text-block-dot-chain baselines come
// back with the text block's content left at its source indentation, where the
// committed ones (written by an older gjf build) de-indent it to column 0.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/baselines"
)

// gjfJVMArgs are the exports google-java-format needs on a modern JDK: it
// reaches into javac internals. Mirrors the wrapper its README documents.
var gjfJVMArgs = []string{
	"--add-exports", "jdk.compiler/com.sun.tools.javac.api=ALL-UNNAMED",
	"--add-exports", "jdk.compiler/com.sun.tools.javac.file=ALL-UNNAMED",
	"--add-exports", "jdk.compiler/com.sun.tools.javac.parser=ALL-UNNAMED",
	"--add-exports", "jdk.compiler/com.sun.tools.javac.tree=ALL-UNNAMED",
	"--add-exports", "jdk.compiler/com.sun.tools.javac.util=ALL-UNNAMED",
}

// haveGjf reports whether a real google-java-format can be launched.
func haveGjf() bool {
	return os.Getenv("GJF_JAR") != "" || os.Getenv("GJF_CP") != ""
}

// runGoogleJavaFormat pipes source through the real google-java-format and
// returns its output - the only thing a baseline is ever written from.
func runGoogleJavaFormat(source, style string) (string, error) {
	args := append([]string{}, gjfJVMArgs...)
	if cp := os.Getenv("GJF_CP"); cp != "" {
		args = append(args, "-cp", cp, "com.google.googlejavaformat.java.Main")
	} else {
		args = append(args, "-jar", os.Getenv("GJF_JAR"))
	}
	if style == "aosp" {
		args = append(args, "--aosp")
	}
	args = append(args, "-")

	cmd := exec.Command("java", args...)
	cmd.Stdin = strings.NewReader(source)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("google-java-format: %w: %s", err, stderr.String())
	}
	return stdout.String(), nil
}

func fixturesRoot(t *testing.T) string {
	// togo/internal/format -> repo root.
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "test-fixtures", "format"))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// regenerate (re)writes one baseline from the real google-java-format when
// UPDATE_BASELINES=1 or the baseline does not exist yet.
func regenerate(t *testing.T, baselinePath, source, style string) {
	t.Helper()
	_, missing := os.Stat(baselinePath)
	if !baselines.Update && missing == nil {
		return
	}
	if !haveGjf() {
		if missing != nil {
			t.Fatalf("missing baseline %s and neither GJF_JAR nor GJF_CP is set; "+
				"set one to (re)generate baselines (see this file's header)", baselinePath)
		}
		t.Logf("UPDATE_BASELINES=1 but neither GJF_JAR nor GJF_CP is set: "+
			"asserting the committed %s instead of regenerating it", baselinePath)
		return
	}
	out, err := runGoogleJavaFormat(source, style)
	if err != nil {
		t.Fatalf("regenerate %s: %v", baselinePath, err)
	}
	if _, err := baselines.Write(baselinePath, []byte(out)); err != nil {
		t.Fatalf("write baseline %s: %v", baselinePath, err)
	}
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
			regenerate(t, baselinePath, string(source), style)
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

// Port of src/format/format.test.ts "comments inside a module declaration are
// formatted, not refused".
func TestModuleDeclarationComments(t *testing.T) {
	source := strings.Join([]string{
		"@SuppressWarnings(\"requires-automatic\") // automatic module names",
		"module com.acme.app {",
		"  exports com.acme.api;",
		"",
		"  // Optional dependency",
		"  requires static java.sql;",
		"  requires java.base; // the platform module",
		"  // dangling",
		"}",
		"",
	}, "\n")
	got, err := FormatSource(source, FormatOptions{Style: "google"}, "module-info.java")
	if err != nil {
		t.Fatalf("FormatSource: %v", err)
	}
	if got != source {
		t.Errorf("got:\n%s\nwant:\n%s", got, source)
	}
}

// TestLineSeparatorPreserved mirrors the TS test "the source's line separator
// is preserved": gjf writes its output with the separator the source uses.
func TestLineSeparatorPreserved(t *testing.T) {
	lines := []string{"package p;", "", "class T {", "  int x;", "}", ""}
	for _, sep := range []string{"\r\n", "\n"} {
		source := strings.Join(lines, sep)
		got, err := FormatSource(source, FormatOptions{Style: "google"}, "T.java")
		if err != nil {
			t.Fatalf("FormatSource: %v", err)
		}
		if got != source {
			t.Errorf("sep %q: got %q, want %q", sep, got, source)
		}
	}
}

// TestTrailingCommentCodeBreaks mirrors the TS test "breaking the code keeps a
// trailing comment on one line": a trailing comment wraps like gjf's, but the
// layout rules come first - gjf breaks the CODE hard enough that most such
// comments fit, and so do we.
func TestTrailingCommentCodeBreaks(t *testing.T) {
	source := strings.Join([]string{
		"package p;",
		"",
		"class T {",
		"  void m(int[] rankArray, HeapNode min) {",
		"    int[] a = new int[(int) Math.floor(Math.log(size()) / Math.log(RATIO)) + 1]; // creates the array",
		"  }",
		"}",
		"",
	}, "\n")
	once, err := FormatSource(source, FormatOptions{Style: "google"}, "T.java")
	if err != nil {
		t.Fatalf("FormatSource: %v", err)
	}
	if !strings.Contains(once, "+ 1]; // creates the array") {
		t.Errorf("trailing comment was wrapped:\n%s", once)
	}
	twice, err := FormatSource(once, FormatOptions{Style: "google"}, "T.java")
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if twice != once {
		t.Errorf("not idempotent:\n1st:\n%s\n2nd:\n%s", once, twice)
	}
}

// TestTrailingCommentWrapped mirrors the TS test "a trailing comment that still
// does not fit is wrapped like gjf": when no code break can help, the comment
// itself wraps at the last space that fits, continuing under its own column.
func TestTrailingCommentWrapped(t *testing.T) {
	source := strings.Join([]string{
		"package p;",
		"",
		"class T {",
		"  void m() {",
		"    go(); // a trailing comment long enough that no amount of code breaking will ever make it fit at all",
		"  }",
		"}",
		"",
	}, "\n")
	out, err := FormatSource(source, FormatOptions{Style: "google"}, "T.java")
	if err != nil {
		t.Fatalf("FormatSource: %v", err)
	}
	first := "go(); // a trailing comment long enough that no amount of code breaking will ever make it fit at"
	if !strings.Contains(out, first) || !strings.Contains(out, "\n          // all") {
		t.Errorf("comment not wrapped like gjf:\n%s", out)
	}
}

// TestStringWrapper mirrors the TS tests for the StringWrapper post-pass: an
// over-long literal is reflowed exactly like gjf, the result is a fixpoint, and
// the concatenated value never changes.
func TestStringWrapper(t *testing.T) {
	source := strings.Join([]string{
		"package p;",
		"",
		"class T {",
		"  void m() {",
		`    throw new IllegalArgumentException("Absolute value of Long.MIN_VALUE does not fit into signed long. Use gcdBig() for full-range support.");`,
		"  }",
		"}",
		"",
	}, "\n")
	want := strings.Join([]string{
		"package p;",
		"",
		"class T {",
		"  void m() {",
		"    throw new IllegalArgumentException(",
		`        "Absolute value of Long.MIN_VALUE does not fit into signed long. Use gcdBig() for"`,
		`            + " full-range support.");`,
		"  }",
		"}",
		"",
	}, "\n")
	got, err := FormatSource(source, FormatOptions{Style: "google"}, "T.java")
	if err != nil {
		t.Fatalf("FormatSource: %v", err)
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	twice, err := FormatSource(got, FormatOptions{Style: "google"}, "T.java")
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if twice != got {
		t.Errorf("string reflow is not idempotent:\n%s", twice)
	}
}

// TestStringWrapperLeavesShortLiterals is the negative case: a literal that
// already fits is never touched.
func TestStringWrapperLeavesShortLiterals(t *testing.T) {
	source := strings.Join([]string{
		"package p;",
		"",
		"class T {",
		`  String ok = "this one fits well inside the column limit and must not be touched at all";`,
		"}",
		"",
	}, "\n")
	got, err := FormatSource(source, FormatOptions{Style: "google"}, "T.java")
	if err != nil {
		t.Fatalf("FormatSource: %v", err)
	}
	if got != source {
		t.Errorf("short literal was touched:\n%s", got)
	}
}
