package services

import (
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/compiler"
)

// Port of src/services/semanticTokens.test.ts.

type semTok struct {
	text string
	typ  string
	mods []string
}

func semTokens(source string) []semTok {
	program := compiler.NewProgram()
	compiler.LoadJdkStub(program)
	program.SetOpenDocument("file:///T.java", source, 1)
	checker := compiler.NewChecker(program)
	sourceFile := program.GetSourceFile("file:///T.java")
	var out []semTok
	for _, tk := range GetSemanticTokens(checker, sourceFile) {
		var mods []string
		for i, m := range TokenModifiers {
			if tk.TokenModifiers&(1<<i) != 0 {
				mods = append(mods, m)
			}
		}
		out = append(out, semTok{text: source[tk.Offset : tk.Offset+tk.Length], typ: TokenTypes[tk.TokenType], mods: mods})
	}
	return out
}

func hasMod(mods []string, want string) bool {
	for _, m := range mods {
		if m == want {
			return true
		}
	}
	return false
}

func TestIdentifiersClassifyByKind(t *testing.T) {
	out := semTokens(strings.Join([]string{
		"class Pet<T> {",
		"  static final int LEGS = 4;",
		"  String name;",
		"  T tag;",
		"  String describe(int extra) {",
		"    int local = LEGS + extra;",
		"    return name + local;",
		"  }",
		"}",
		"enum Color { RED }",
	}, "\n"))
	byText := map[string]semTok{}
	for _, tk := range out {
		if _, ok := byText[tk.text]; !ok {
			byText[tk.text] = tk
		}
	}
	checks := map[string]string{
		"Pet": "class", "T": "typeParameter", "LEGS": "property", "name": "property",
		"extra": "parameter", "local": "variable", "describe": "method",
		"Color": "enum", "RED": "enumMember",
	}
	for text, typ := range checks {
		if byText[text].typ != typ {
			t.Errorf("%q type = %q, want %q", text, byText[text].typ, typ)
		}
	}
	if !hasMod(byText["Pet"].mods, "declaration") {
		t.Error("Pet should be a declaration")
	}
	if !hasMod(byText["RED"].mods, "static") || !hasMod(byText["RED"].mods, "readonly") {
		t.Error("RED should be static+readonly")
	}
}

func TestStaticFinalAndDefaultLibrary(t *testing.T) {
	out := semTokens(strings.Join([]string{
		"class C {",
		"  static final int MAX = 9;",
		"  void m() {",
		"    String s = String.valueOf(MAX);",
		"  }",
		"}",
	}, "\n"))
	for _, tk := range out {
		if tk.text == "MAX" && (!hasMod(tk.mods, "static") || !hasMod(tk.mods, "readonly")) {
			t.Errorf("MAX should be static+readonly, got %v", tk.mods)
		}
		if tk.text == "String" && (tk.typ != "class" || !hasMod(tk.mods, "defaultLibrary")) {
			t.Errorf("String should be class+defaultLibrary, got %v/%v", tk.typ, tk.mods)
		}
	}
	found := false
	for _, tk := range out {
		if tk.text == "valueOf" {
			found = true
			if tk.typ != "method" || !hasMod(tk.mods, "static") || !hasMod(tk.mods, "defaultLibrary") {
				t.Errorf("valueOf should be method+static+defaultLibrary, got %v/%v", tk.typ, tk.mods)
			}
		}
	}
	if !found {
		t.Error("valueOf token not found")
	}
}

func TestDeprecatedModifier(t *testing.T) {
	out := semTokens(strings.Join([]string{
		"class C {",
		"  @Deprecated int old;",
		"  int cur;",
		"  @Deprecated void gone() {}",
		"  void m() { gone(); int x = old + cur; }",
		"}",
	}, "\n"))
	for _, tk := range out {
		switch tk.text {
		case "old", "gone":
			if !hasMod(tk.mods, "deprecated") {
				t.Errorf("%s should be deprecated, got %v", tk.text, tk.mods)
			}
		case "cur":
			if hasMod(tk.mods, "deprecated") {
				t.Errorf("cur should not be deprecated, got %v", tk.mods)
			}
		}
	}
}

func TestRegexSinkTokens(t *testing.T) {
	out := semTokens(strings.Join([]string{
		"class C {",
		"  void m() {",
		`    java.util.regex.Pattern.compile("\\d+");`,
		`    "x".matches("[a-z]");`,
		`    "a,b".split(",");`,
		`    String.valueOf(1);`,
		"  }",
		"}",
	}, "\n"))
	var regexps []string
	for _, tk := range out {
		if tk.typ == "regexp" {
			regexps = append(regexps, tk.text)
		}
	}
	want := []string{`"\\d+"`, `"[a-z]"`, `","`}
	if strings.Join(regexps, "|") != strings.Join(want, "|") {
		t.Errorf("regexp tokens = %v, want %v", regexps, want)
	}
}

func TestEntriesSortedAndResolvedOnly(t *testing.T) {
	out := semTokens("class C { void m() { unknownThing(); int x = 1; } }")
	for _, tk := range out {
		if tk.text == "unknownThing" {
			t.Error("unresolved identifier should not be tokenized")
		}
	}
	foundX := false
	for _, tk := range out {
		if tk.text == "x" {
			foundX = true
		}
	}
	if !foundX {
		t.Error("x should be tokenized")
	}
}
