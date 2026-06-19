package services

import (
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/compiler"
)

// Port of src/services/inlayHints.test.ts.

type inlayHint struct {
	label, kind, at string
}

func inlayHints(source string, settings InlayHintsSettings) []inlayHint {
	program := compiler.NewProgram()
	compiler.LoadJdkStub(program)
	program.SetOpenDocument("file:///T.java", source, 1)
	checker := compiler.NewChecker(program)
	sourceFile := program.GetSourceFile("file:///T.java")
	var out []inlayHint
	for _, h := range GetInlayHints(checker, sourceFile, 0, len(source), settings) {
		end := h.Offset + 6
		if end > len(source) {
			end = len(source)
		}
		out = append(out, inlayHint{label: h.Label, kind: h.Kind, at: source[h.Offset:end]})
	}
	return out
}

func hintsOfKind(hints []inlayHint, kind string) []string {
	var out []string
	for _, h := range hints {
		if h.kind == kind {
			out = append(out, h.label)
		}
	}
	return out
}

func TestParameterHintsLiteralAndExpression(t *testing.T) {
	out := inlayHints(strings.Join([]string{
		"class C {",
		"  static int clamp(int value, int low, int high) { return value; }",
		"  void m(int x) {",
		"    int limit = 9;",
		"    clamp(x, 1 + 2, limit);",
		"    clamp(5, x, compute());",
		"  }",
		"  int compute() { return 0; }",
		"}",
	}, "\n"), DefaultInlayHints)
	params := hintsOfKind(out, "parameter")
	want := []string{"low:", "value:", "high:"}
	if len(params) != 3 || params[0] != want[0] || params[1] != want[1] || params[2] != want[2] {
		t.Errorf("parameter hints = %v, want %v", params, want)
	}
	var paramEntries []inlayHint
	for _, h := range out {
		if h.kind == "parameter" {
			paramEntries = append(paramEntries, h)
		}
	}
	if !strings.HasPrefix(paramEntries[0].at, "1 + 2") || !strings.HasPrefix(paramEntries[1].at, "5") || !strings.HasPrefix(paramEntries[2].at, "comput") {
		t.Errorf("attachment points wrong: %v", paramEntries)
	}
}

func TestVarargsHintFirstOfTail(t *testing.T) {
	out := inlayHints(strings.Join([]string{
		"class C {",
		"  static int sum(int... xs) { return 0; }",
		"  void m() { sum(1, 2, 3); }",
		"}",
	}, "\n"), DefaultInlayHints)
	params := hintsOfKind(out, "parameter")
	if len(params) != 1 || params[0] != "...xs:" {
		t.Errorf("varargs hints = %v, want [...xs:]", params)
	}
}

func TestVarAndForEachTypeHints(t *testing.T) {
	out := inlayHints(strings.Join([]string{
		"import java.util.List;",
		"class C {",
		"  void m(List<String> xs) {",
		"    var s = \"hi\";",
		"    var n = 1 + 2;",
		"    for (var item : xs) { use(item); }",
		"  }",
		"  void use(String s) {}",
		"}",
	}, "\n"), DefaultInlayHints)
	types := hintsOfKind(out, "type")
	want := []string{": String", ": int", ": String"}
	if len(types) != 3 || types[0] != want[0] || types[1] != want[1] || types[2] != want[2] {
		t.Errorf("type hints = %v, want %v", types, want)
	}
}

func TestSettingsDisableFamilies(t *testing.T) {
	source := strings.Join([]string{
		"class C {",
		"  static int twice(int value) { return value * 2; }",
		"  void m() { var n = twice(21); }",
		"}",
	}, "\n")
	all := inlayHints(source, DefaultInlayHints)
	kinds := map[string]bool{}
	for _, h := range all {
		kinds[h.kind] = true
	}
	if !kinds["parameter"] || !kinds["type"] {
		t.Errorf("default should have both families, got %v", kinds)
	}
	noParam := inlayHints(source, InlayHintsSettings{ParameterNames: false, VarTypes: true})
	if k := hintsOfKind(noParam, "parameter"); len(k) != 0 {
		t.Errorf("parameterNames=false should suppress parameter hints, got %v", k)
	}
	noType := inlayHints(source, InlayHintsSettings{ParameterNames: true, VarTypes: false})
	if k := hintsOfKind(noType, "type"); len(k) != 0 {
		t.Errorf("varTypes=false should suppress type hints, got %v", k)
	}
}
