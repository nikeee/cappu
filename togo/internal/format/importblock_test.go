package format

// Mirrors src/format/import-block.test.ts: the import block end to end, as
// opposed to importorder_test.go, which tests the grouping in isolation. The
// concerns here are the ones only the printer can get wrong - stray blank
// lines, a block that does not exist, and stability under a second pass.

import (
	"strings"
	"testing"
)

func formatImports(t *testing.T, source string, options FormatOptions) string {
	t.Helper()
	out, err := FormatSource(source, options, "T.java")
	if err != nil {
		t.Fatalf("FormatSource: %v", err)
	}
	return out
}

var importOrderFixture = []string{"java.*", "", "*"}

func TestImportBlock(t *testing.T) {
	tests := []struct {
		name     string
		source   []string
		options  FormatOptions
		want     string // the whole output
		contains string // or just a fragment of it
	}{
		{
			name:    "a file without imports is unaffected by importOrder",
			source:  []string{"package p;", "", "class T {}"},
			options: FormatOptions{Style: "google", ImportOrder: importOrderFixture},
			want:    "package p;\n\nclass T {}\n",
		},
		{
			name: "a file of only static imports gets one block and no stray blank line",
			source: []string{
				"package p;", "",
				"import static org.junit.Assert.fail;",
				"import static java.util.Arrays.asList;", "",
				"class T {}",
			},
			options: FormatOptions{Style: "google", ImportOrder: importOrderFixture},
			want: "package p;\n\nimport static java.util.Arrays.asList;\n" +
				"import static org.junit.Assert.fail;\n\nclass T {}\n",
		},
		{
			// Whatever blank lines the source had inside the import block are
			// discarded: the configured groups decide where they go.
			name: "source blank lines inside the import block are replaced by the configured ones",
			source: []string{
				"package p;", "",
				"import java.io.File;", "", "",
				"import org.junit.Test;",
				"import com.acme.Widget;", "",
				"class T {", "  File f;", "  Test t;", "  Widget w;", "}",
			},
			options:  FormatOptions{Style: "google", ImportOrder: importOrderFixture},
			contains: "import java.io.File;\n\nimport com.acme.Widget;\nimport org.junit.Test;\n\nclass T {",
		},
		{
			// The name of `import java.util.*;` is `java.util`, so it has to match
			// the pattern for its own package rather than falling through to a
			// broader one. gjf sorts the wildcard first within its group.
			name: "an on-demand import lands in its own package's group",
			source: []string{
				"package p;", "",
				"import java.util.*;",
				"import java.util.List;",
				"import java.io.File;", "",
				"class T {", "  File f;", "  List<String> l;", "}",
			},
			options:  FormatOptions{Style: "google", ImportOrder: []string{"java.util.*", "", "java.*"}},
			contains: "import java.util.*;\nimport java.util.List;\n\nimport java.io.File;",
		},
		{
			name: "a file without a package declaration still groups its imports",
			source: []string{
				"import java.io.File;",
				"import com.acme.Widget;", "",
				"class T {", "  File f;", "  Widget w;", "}",
			},
			options: FormatOptions{Style: "google", ImportOrder: importOrderFixture},
			want:    "import java.io.File;\n\nimport com.acme.Widget;\n\nclass T {\n  File f;\n  Widget w;\n}\n",
		},
		{
			name: "aosp style groups android, third party and java without a configured order",
			source: []string{
				"package p;", "",
				"import java.io.File;",
				"import android.view.View;",
				"import com.acme.Widget;", "",
				"class T {", "  File f;", "  View v;", "  Widget w;", "}",
			},
			options:  FormatOptions{Style: "aosp"},
			contains: "import android.view.View;\n\nimport com.acme.Widget;\n\nimport java.io.File;",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := formatImports(t, strings.Join(tt.source, "\n")+"\n", tt.options)
			if tt.want != "" && out != tt.want {
				t.Errorf("FormatSource:\n got: %q\nwant: %q", out, tt.want)
			}
			if tt.contains != "" && !strings.Contains(out, tt.contains) {
				t.Errorf("output:\n%s\nwant it to contain:\n%s", out, tt.contains)
			}
		})
	}
}

// Formatting an already-formatted file must not move imports again, in either
// style and with or without a configured order.
func TestImportGroupingIsIdempotent(t *testing.T) {
	source := strings.Join([]string{
		"package p;", "",
		"import static org.junit.Assert.fail;",
		"import org.junit.Test;",
		"import java.io.File;",
		"import android.view.View;",
		"import com.acme.Widget;", "",
		"class T {", "  File f;", "  Test t;", "  View v;", "  Widget w;", "}",
	}, "\n") + "\n"
	for _, options := range []FormatOptions{
		{Style: "google"},
		{Style: "aosp"},
		{Style: "google", ImportOrder: importOrderFixture},
		{Style: "aosp", ImportOrder: []string{"android.*", "", "com.*", "", "*"}},
	} {
		once := formatImports(t, source, options)
		if twice := formatImports(t, once, options); twice != once {
			t.Errorf("%+v not idempotent:\n once: %q\ntwice: %q", options, once, twice)
		}
	}
}
