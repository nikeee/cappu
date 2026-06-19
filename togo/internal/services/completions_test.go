package services

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/compiler"
	"github.com/nikeee/cappu/internal/config"
)

// Port of src/services/completions.test.ts.

func complete(text string) []CompletionItem {
	const marker = "/*|*/"
	offset := strings.Index(text, marker)
	clean := strings.Replace(text, marker, "", 1)
	program := compiler.NewProgram()
	compiler.LoadJdkStub(program)
	program.SetOpenDocument("file:///T.java", clean, 1)
	checker := compiler.NewChecker(program)
	sf := program.GetSourceFile("file:///T.java")
	return GetCompletions(program, checker, sf, offset, nil)
}

func labelsOf(items []CompletionItem) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.Label)
	}
	return out
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func TestMemberCompletionListsMembers(t *testing.T) {
	items := complete("class P { int age; String name; } class U { void m(P p) { p./*|*/ } }")
	got := labelsOf(items)
	sort.Strings(got)
	want := []string{"age", "clone", "equals", "getClass", "hashCode", "name", "toString"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("members = %v, want %v", got, want)
	}
}

func TestMemberCompletionIncompleteCode(t *testing.T) {
	labels := labelsOf(complete("class U { void m(String s) { s./*|*/ } }"))
	if !contains(labels, "length") || !contains(labels, "substring") {
		t.Errorf("expected length+substring, got %v", labels)
	}
}

func TestMemberCompletionUnknownReceiver(t *testing.T) {
	if items := complete("class U { void m() { mystery./*|*/ } }"); len(items) != 0 {
		t.Errorf("unknown receiver should yield no completions, got %v", labelsOf(items))
	}
}

func TestMemberCompletionPartialName(t *testing.T) {
	if !contains(labelsOf(complete("class U { void m(String s) { s.sub/*|*/ } }")), "substring") {
		t.Error("partial member name should still complete substring")
	}
	if !contains(labelsOf(complete("class C { String name; void m() { name.len/*|*/ } }")), "length") {
		t.Error("bare field receiver mid-token should complete length")
	}
}

func TestScopeCompletion(t *testing.T) {
	labels := labelsOf(complete("class Box { int field; void m(int param) { int local = 0; /*|*/ } }"))
	for _, want := range []string{"local", "param", "field", "Box", "String"} {
		if !contains(labels, want) {
			t.Errorf("scope completion missing %q (got %v)", want, labels)
		}
	}
}

func TestScopeCompletionBrokenCode(t *testing.T) {
	labels := labelsOf(complete("class C { void m(int p) { int x = ; /*|*/ } }"))
	if !contains(labels, "p") || !contains(labels, "x") {
		t.Errorf("broken-code scope completion missing p/x, got %v", labels)
	}
}

func TestCompletionKindsClassified(t *testing.T) {
	items := complete("class P { int age; String greet() { return null; } } class U { void m(P p) { p./*|*/ } }")
	byLabel := map[string]CompletionItemKind{}
	for _, it := range items {
		byLabel[it.Label] = it.Kind
	}
	if byLabel["age"] != CompletionItemKindField {
		t.Errorf("age kind = %d, want Field", byLabel["age"])
	}
	if byLabel["greet"] != CompletionItemKindMethod {
		t.Errorf("greet kind = %d, want Method", byLabel["greet"])
	}
}

func TestClasspathResourceCompletion(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "cappu.json"), "{}\n")
	resDir := filepath.Join(dir, "src", "main", "resources")
	if err := os.MkdirAll(filepath.Join(resDir, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(resDir, "messages.properties"), "x=1")
	mustWrite(t, filepath.Join(resDir, "db", "schema.sql"), "create")
	cfg, err := config.Load("", dir)
	if err != nil {
		t.Fatal(err)
	}

	src := "class C { void m() throws Exception { getClass().getResourceAsStream(\"/*|*/\"); } }"
	offset := strings.Index(src, "/*|*/")
	program := compiler.NewProgram()
	program.SetOpenDocument("file:///C.java", strings.Replace(src, "/*|*/", "", 1), 1)
	checker := compiler.NewChecker(program)
	sf := program.GetSourceFile("file:///C.java")

	items := GetCompletions(program, checker, sf, offset, cfg)
	got := labelsOf(items)
	sort.Strings(got)
	want := []string{"/db/schema.sql", "/messages.properties"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("resources = %v, want %v", got, want)
	}
	if len(items) > 0 && items[0].Kind != CompletionItemKindFile {
		t.Errorf("resource kind = %d, want File", items[0].Kind)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
