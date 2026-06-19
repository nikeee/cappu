package services

// Fourslash-style language-service baseline tests (analogous to the TypeScript
// compiler's fourslash tests). A fixture is a .java file with markers /*name*/;
// the marker is stripped and its offset becomes a query position. Completion and
// hover results are serialized and compared against checked-in baselines shared
// with the TypeScript build. Port of src/services/fourslash*.test.ts.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/compiler"
)

const (
	fourslashDir      = "../../../test-fixtures/language-service/fourslash"
	fourslashBaseline = "../../../test-fixtures/language-service/fourslash-baselines"
	hoverDir          = "../../../test-fixtures/language-service/fourslash-hover"
	hoverBaseline     = "../../../test-fixtures/language-service/fourslash-hover-baselines"
)

var markerRE = regexp.MustCompile(`/\*([A-Za-z0-9_]+)\*/`)

type marker struct {
	name   string
	offset int
}

func extractMarkers(text string) (string, []marker) {
	var markers []marker
	var clean strings.Builder
	last := 0
	for _, m := range markerRE.FindAllStringSubmatchIndex(text, -1) {
		clean.WriteString(text[last:m[0]])
		markers = append(markers, marker{name: text[m[2]:m[3]], offset: clean.Len()})
		last = m[1]
	}
	clean.WriteString(text[last:])
	return clean.String(), markers
}

func javaFixtures(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("fixtures dir %s missing: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".java") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

var completionKindNames = map[CompletionItemKind]string{
	CompletionItemKindMethod:        "method",
	CompletionItemKindField:         "field",
	CompletionItemKindVariable:      "variable",
	CompletionItemKindClass:         "class",
	CompletionItemKindInterface:     "interface",
	CompletionItemKindEnum:          "enum",
	CompletionItemKindEnumMember:    "enum-constant",
	CompletionItemKindTypeParameter: "type-parameter",
}

func serializeCompletions(items []CompletionItem) string {
	if len(items) == 0 {
		return "  (none)"
	}
	var lines []string
	for _, it := range items {
		name, ok := completionKindNames[it.Kind]
		if !ok {
			name = "?"
		}
		lines = append(lines, "  "+name+" "+it.Label)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func TestFourslashCompletions(t *testing.T) {
	for _, fixture := range javaFixtures(t, fourslashDir) {
		t.Run(fixture, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(fourslashDir, fixture))
			if err != nil {
				t.Fatal(err)
			}
			clean, markers := extractMarkers(string(raw))
			program := compiler.NewProgram()
			compiler.LoadJdkStub(program)
			uri := compiler.URI("file:///" + fixture)
			program.SetOpenDocument(uri, clean, 1)
			checker := compiler.NewChecker(program)
			sourceFile := program.GetSourceFile(uri)

			var sections []string
			for _, m := range markers {
				items := GetCompletions(program, checker, sourceFile, m.offset, nil)
				sections = append(sections, "=== "+m.name+" ===\n"+serializeCompletions(items))
			}
			actual := strings.Join(sections, "\n\n") + "\n"

			want, err := os.ReadFile(filepath.Join(fourslashBaseline, strings.TrimSuffix(fixture, ".java")+".txt"))
			if err != nil {
				t.Fatalf("baseline missing: %v", err)
			}
			if actual != string(want) {
				t.Errorf("baseline mismatch for %s:\n--- got ---\n%s\n--- want ---\n%s", fixture, actual, want)
			}
		})
	}
}

func TestFourslashHover(t *testing.T) {
	for _, fixture := range javaFixtures(t, hoverDir) {
		t.Run(fixture, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(hoverDir, fixture))
			if err != nil {
				t.Fatal(err)
			}
			clean, markers := extractMarkers(string(raw))
			program := compiler.NewProgram()
			compiler.LoadJdkStub(program)
			uri := compiler.URI("file:///" + fixture)
			program.SetOpenDocument(uri, clean, 1)
			checker := compiler.NewChecker(program)
			sourceFile := program.GetSourceFile(uri)

			var sections []string
			for _, m := range markers {
				id := compiler.GetIdentifierAtPosition(sourceFile, m.offset)
				text := "(unresolved)"
				if id != nil {
					if symbol := checker.ResolveName(id); symbol != nil {
						text = GetHoverText(checker, symbol, id)
					}
				}
				sections = append(sections, "=== "+m.name+" ===\n  "+text)
			}
			actual := strings.Join(sections, "\n") + "\n"

			want, err := os.ReadFile(filepath.Join(hoverBaseline, strings.TrimSuffix(fixture, ".java")+".txt"))
			if err != nil {
				t.Fatalf("baseline missing: %v", err)
			}
			if actual != string(want) {
				t.Errorf("baseline mismatch for %s:\n--- got ---\n%s\n--- want ---\n%s", fixture, actual, want)
			}
		})
	}
}
