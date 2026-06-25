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
