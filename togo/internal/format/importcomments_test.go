package format

// Mirrors src/format/import-comments.test.ts: reordering the import block moves
// lines past each other, so no comment may vanish, land next to an import it did
// not document, or leave output that no longer parses. gjf's rule is that an
// own-line comment between two imports belongs to the PRECEDING import and
// travels with it; comments above the first import head the whole block.

import (
	"sort"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/compiler"
)

// formatPreservingComments formats and asserts the output still parses and
// carries exactly the comments the input did.
func formatPreservingComments(t *testing.T, source string, importOrder []string) string {
	t.Helper()
	out, err := FormatSource(source, FormatOptions{Style: "google", ImportOrder: importOrder}, "T.java")
	if err != nil {
		t.Fatalf("FormatSource: %v", err)
	}
	if sf := compiler.ParseSourceFile("T.java", out); len(sf.AsSourceFile().ParseDiagnostics) > 0 {
		t.Fatalf("output does not parse:\n%s", out)
	}
	before, after := commentTexts(source), commentTexts(out)
	if strings.Join(before, "\x00") != strings.Join(after, "\x00") {
		t.Errorf("comments changed:\n before: %v\n after:  %v\noutput:\n%s", before, after, out)
	}
	return out
}

func commentTexts(text string) []string {
	var out []string
	for _, c := range collectComments(text) {
		out = append(out, c.text)
	}
	sort.Strings(out)
	return out
}

func TestImportCommentsTravelWithTheirImport(t *testing.T) {
	tests := []struct {
		name        string
		source      []string
		importOrder []string
		want        string
	}{
		{
			name: "a trailing comment follows its import into another block",
			source: []string{
				"package p;", "",
				"import java.io.File; // needed for the temp dir",
				"import org.junit.Test; // NOPMD", "",
				"class T {", "  File f;", "  Test t;", "}",
			},
			importOrder: []string{"org.*", "", "java.*"},
			want:        "import org.junit.Test; // NOPMD\n\nimport java.io.File; // needed",
		},
		{
			// gjf hangs it off the PRECEDING import, so it moves with org.junit.Test.
			name: "an own-line comment follows the import it sits under",
			source: []string{
				"package p;", "",
				"import org.junit.Test;",
				"// only used by the parameterized cases",
				"import java.io.File;", "",
				"class T {", "  File f;", "  Test t;", "}",
			},
			importOrder: []string{"java.*", "", "org.*"},
			want:        "import java.io.File;\n\nimport org.junit.Test;\n// only used by the parameterized cases",
		},
		{
			name: "comments above the first import head the block and stay on top",
			source: []string{
				"package p;", "",
				"// Source: http://example.com/snippet",
				"// License: Open Source", "",
				"import org.junit.Test;",
				"import java.io.File;", "",
				"class T {", "  File f;", "  Test t;", "}",
			},
			importOrder: []string{"java.*", "", "org.*"},
			want:        "// Source: http://example.com/snippet\n// License: Open Source\n\nimport java.io.File;",
		},
		{
			name: "a comment after the last import belongs to what follows",
			source: []string{
				"package p;", "",
				"import java.io.File;", "",
				"/** The class javadoc. */",
				"class T {", "  File f;", "}",
			},
			importOrder: []string{"*"},
			want:        "/** The class javadoc. */\nclass T {",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := formatPreservingComments(t, strings.Join(tt.source, "\n")+"\n", tt.importOrder)
			if !strings.Contains(out, tt.want) {
				t.Errorf("output:\n%s\nwant it to contain:\n%s", out, tt.want)
			}
		})
	}
}

func TestImportCommentsSurviveADuplicateAndAFullRegroup(t *testing.T) {
	out := formatPreservingComments(t, strings.Join([]string{
		"package p;", "",
		"import java.io.File;",
		"import java.io.File; // the duplicate's comment",
		"// and its follower",
		"import org.junit.Test;", "",
		"class T {", "  File f;", "  Test t;", "}",
	}, "\n")+"\n", nil)
	if strings.Count(out, "import java.io.File;") != 1 {
		t.Errorf("duplicate import not deduped:\n%s", out)
	}

	out = formatPreservingComments(t, strings.Join([]string{
		"package p;", "",
		"import static org.junit.Assert.assertTrue; // static one",
		"import android.view.View; // android",
		"// follows the android import",
		"import java.io.File; // java",
		"import com.acme.Widget; // third party", "",
		"class T {", "  File f;", "  View v;", "  Widget w;", "}",
	}, "\n")+"\n", []string{"java.*", "", "com.*", "", "android.*"})
	for _, want := range []string{
		"import java.io.File; // java",
		"import com.acme.Widget; // third party",
		"import android.view.View; // android\n// follows the android import",
		"import static org.junit.Assert.assertTrue; // static one",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output:\n%s\nwant it to contain:\n%s", out, want)
		}
	}
}
