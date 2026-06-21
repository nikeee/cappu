package compiler

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AST baseline tests, in the style of the TypeScript compiler. Each .java file
// under test-fixtures/parser/cases is parsed and bound, the resulting tree +
// diagnostics serialized to a stable text form, and compared to the checked-in
// baseline under test-fixtures/parser/baselines. Port of src/compiler/
// baseline.test.ts; the Go serialization must match the TS-generated baselines
// byte-for-byte, proving the parser produces an identical tree shape.

func flagSuffix(flags NodeFlags) string {
	var parts []string
	if flags&NodeFlagThisNodeHasError != 0 {
		parts = append(parts, "HasError")
	}
	if flags&NodeFlagThisNodeOrAnySubNodesError != 0 {
		parts = append(parts, "SubtreeHasError")
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// jsonString reproduces JS JSON.stringify(value) for a string (no HTML escaping).
func jsonString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimRight(buf.String(), "\n")
}

func nodeLabel(node *Node) string {
	label := node.Kind.String()
	switch node.Kind {
	case Identifier:
		label += " \"" + node.AsIdentifier().Text + "\""
	case NumericLiteral, StringLiteral, CharacterLiteral, TextBlockLiteral:
		label += " " + jsonString(node.AsLiteralExpression().Value)
	}
	return label + " [" + itoaDiff(node.Pos) + "," + itoaDiff(node.End) + "]" + flagSuffix(node.Flags)
}

func printNode(node *Node, depth int, out *[]string) {
	*out = append(*out, strings.Repeat("  ", depth)+nodeLabel(node))
	node.ForEachChild(func(child *Node) bool {
		printNode(child, depth+1, out)
		return false
	})
}

func formatBaselineDiagnostics(title string, diagnostics []Diagnostic) []string {
	if len(diagnostics) == 0 {
		return nil
	}
	out := []string{"", title + ":"}
	for _, d := range diagnostics {
		out = append(out, "  ["+itoaDiff(d.Pos)+","+itoaDiff(d.End)+"] "+d.MessageText)
	}
	return out
}

func serializeTree(fileName, source string) string {
	sf := ParseSourceFile(fileName, source)
	BindSourceFile(sf)
	sfd := sf.AsSourceFile()
	var out []string
	printNode(sf, 0, &out)
	out = append(out, formatBaselineDiagnostics("Parse diagnostics", sfd.ParseDiagnostics)...)
	out = append(out, formatBaselineDiagnostics("Bind diagnostics", sfd.BindDiagnostics)...)
	return strings.Join(out, "\n") + "\n"
}

func TestParserBaselines(t *testing.T) {
	casesDir := filepath.Join("..", "..", "..", "test-fixtures", "parser", "cases")
	baselinesDir := filepath.Join("..", "..", "..", "test-fixtures", "parser", "baselines")
	entries, err := os.ReadDir(casesDir)
	if err != nil {
		t.Skip("no parser cases present")
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".java") {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join(casesDir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			actual := serializeTree(e.Name(), string(source))
			baselinePath := filepath.Join(baselinesDir, strings.TrimSuffix(e.Name(), ".java")+".txt")
			expected, err := os.ReadFile(baselinePath)
			if err != nil {
				t.Fatalf("no baseline %s: %v", baselinePath, err)
			}
			if actual != string(expected) {
				t.Errorf("AST serialization differs from the baseline.\n--- got ---\n%s\n--- want ---\n%s", actual, expected)
			}
		})
	}
}
