package mcp

import (
	"sort"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/compiler"
)

// Port of src/services/mcp.test.ts.

func toolsFor(files map[string]string) *Tools {
	program := compiler.NewProgram()
	for uri, text := range files {
		program.AddProjectFile(compiler.URI(uri), text)
	}
	return NewTools(program, compiler.NewChecker(program))
}

func labels(matches []McpMatch) []string {
	out := []string{}
	for _, m := range matches {
		out = append(out, m.Label)
	}
	return out
}

func TestDiagnosticsSyntaxError(t *testing.T) {
	tools := toolsFor(map[string]string{"file:///Bad.java": "class Bad { void m( }"})
	diags := tools.Diagnostics(nil)
	if len(diags) == 0 {
		t.Fatal("expected at least one diagnostic")
	}
	d := diags[0]
	if d.File != "/Bad.java" || d.Severity != "error" || d.Line < 1 || d.Column < 1 {
		t.Errorf("diagnostic = %+v", d)
	}
}

func TestDiagnosticsValidFile(t *testing.T) {
	tools := toolsFor(map[string]string{"file:///Ok.java": "class Ok { int m() { return 1; } }"})
	if d := tools.Diagnostics(nil); len(d) != 0 {
		t.Errorf("expected no diagnostics, got %+v", d)
	}
}

func TestDiagnosticsFileFilter(t *testing.T) {
	tools := toolsFor(map[string]string{
		"file:///Bad.java": "class Bad { void m( }",
		"file:///Ok.java":  "class Ok {}",
	})
	if d := tools.Diagnostics([]string{"/Ok.java"}); len(d) != 0 {
		t.Errorf("expected no diagnostics for Ok.java, got %+v", d)
	}
}

func TestDeprecatedUses(t *testing.T) {
	tools := toolsFor(map[string]string{
		"file:///Api.java": "class Api {\n" +
			"  @Deprecated(since=\"2.0\", forRemoval=true) static int old() { return 1; }\n" +
			"  static int ok() { return 2; }\n" +
			"}\n" +
			"@Deprecated class Legacy {}\n" +
			"class Use {\n" +
			"  void m() { int a = Api.old(); int b = Api.ok(); Legacy x = null; }\n" +
			"}",
	})
	uses := tools.DeprecatedUses(nil)
	byName := map[string]McpDeprecatedUse{}
	for _, u := range uses {
		byName[u.Name] = u
	}
	if len(byName) != 2 {
		t.Fatalf("expected uses of old + Legacy, got %+v", uses)
	}
	if o := byName["old"]; o.Kind != "method" || o.Since != "2.0" || !o.ForRemoval || !strings.Contains(o.Message, "marked for removal") {
		t.Errorf("old: %+v", o)
	}
	if l := byName["Legacy"]; l.Kind != "type" || l.ForRemoval {
		t.Errorf("Legacy: %+v", l)
	}
}

func TestDeprecatedUsesEmpty(t *testing.T) {
	tools := toolsFor(map[string]string{"file:///Ok.java": "class Ok { int m() { return 1; } }"})
	if u := tools.DeprecatedUses(nil); len(u) != 0 {
		t.Errorf("expected no deprecated uses, got %+v", u)
	}
}

func TestOutlineTopLevel(t *testing.T) {
	tools := toolsFor(map[string]string{"file:///Foo.java": "class Foo { int x; void m() {} }"})
	symbols := tools.Outline("/Foo.java")
	if len(symbols) != 1 || symbols[0].Name != "Foo" {
		t.Errorf("symbols = %+v", symbols)
	}
}

func TestOutlineUnknownFile(t *testing.T) {
	tools := toolsFor(map[string]string{"file:///Foo.java": "class Foo {}"})
	if s := tools.Outline("/Missing.java"); len(s) != 0 {
		t.Errorf("expected empty, got %+v", s)
	}
}

func TestSearchSymbols(t *testing.T) {
	tools := toolsFor(map[string]string{
		"file:///UserService.java": "package app; class UserService {}",
		"file:///Repo.java":        "package app; class Repo {}",
	})
	matches := tools.SearchSymbols("service")
	if len(matches) != 1 || matches[0] != "app.UserService" {
		t.Errorf("matches = %v", matches)
	}
}

func TestDescribeSymbolType(t *testing.T) {
	tools := toolsFor(map[string]string{"file:///Foo.java": "package a; class Foo {}"})
	matches := tools.DescribeSymbol("a.Foo")
	if len(matches) != 1 || matches[0].Kind != "class" || matches[0].Label != "class Foo" {
		t.Fatalf("matches = %+v", matches)
	}
	if matches[0].Definition == nil || matches[0].Definition.File != "/Foo.java" {
		t.Errorf("definition = %+v", matches[0].Definition)
	}
}

func TestDescribeSymbolMethod(t *testing.T) {
	tools := toolsFor(map[string]string{"file:///Foo.java": "package a; class Foo { int add(int x) { return x; } }"})
	matches := tools.DescribeSymbol("a.Foo#add")
	if len(matches) != 1 || matches[0].Kind != "method" || !contains(matches[0].Signature, "add") {
		t.Errorf("matches = %+v", matches)
	}
}

func TestFindDefinition(t *testing.T) {
	tools := toolsFor(map[string]string{"file:///Foo.java": "package a; class Foo {}"})
	defs := tools.FindDefinition("a.Foo")
	if len(defs) != 1 || defs[0].File != "/Foo.java" || defs[0].Line != 1 {
		t.Errorf("definitions = %+v", defs)
	}
}

func TestMcpFindReferences(t *testing.T) {
	tools := toolsFor(map[string]string{"file:///Foo.java": "package a; class Foo { int f; void m() { f = f + 1; } }"})
	if r := tools.FindReferences("a.Foo#f"); len(r.References) != 3 {
		t.Errorf("references = %d, want 3", len(r.References))
	}
}

func TestMcpFindReferencesAmbiguous(t *testing.T) {
	tools := toolsFor(map[string]string{
		"file:///a/Foo.java": "package a; class Foo {}",
		"file:///b/Foo.java": "package b; class Foo {}",
	})
	r := tools.FindReferences("Foo")
	if !r.Ambiguous || r.Candidates != 2 || len(r.References) != 0 {
		t.Errorf("result = %+v", r)
	}
}

func TestFindImplementationsInterface(t *testing.T) {
	tools := toolsFor(map[string]string{
		"file:///Shape.java":  "package a; interface Shape {}",
		"file:///Circle.java": "package a; class Circle implements Shape {}",
		"file:///Square.java": "package a; class Square implements Shape {}",
	})
	impls := tools.FindImplementations("a.Shape")
	got := labels(impls.Implementations)
	sort.Strings(got)
	if len(got) != 2 || got[0] != "class Circle" || got[1] != "class Square" {
		t.Errorf("implementations = %v", got)
	}
}

func TestFindImplementationsMethodOverrides(t *testing.T) {
	tools := toolsFor(map[string]string{
		"file:///Animal.java": "package a; abstract class Animal { abstract String sound(); }",
		"file:///Dog.java":    "package a; class Dog extends Animal { String sound() { return \"woof\"; } }",
	})
	impls := tools.FindImplementations("a.Animal#sound")
	if len(impls.Implementations) != 1 || !contains(impls.Implementations[0].Label, "sound") {
		t.Fatalf("implementations = %+v", impls.Implementations)
	}
	if impls.Implementations[0].Definition == nil || impls.Implementations[0].Definition.File != "/Dog.java" {
		t.Errorf("definition = %+v", impls.Implementations[0].Definition)
	}
}

func TestFindImplementationsAmbiguous(t *testing.T) {
	tools := toolsFor(map[string]string{
		"file:///a/Shape.java": "package a; interface Shape {}",
		"file:///b/Shape.java": "package b; interface Shape {}",
	})
	r := tools.FindImplementations("Shape")
	if !r.Ambiguous || r.Candidates != 2 {
		t.Errorf("result = %+v", r)
	}
}

func TestListMembers(t *testing.T) {
	tools := toolsFor(map[string]string{
		"file:///Base.java": "package a; class Base { int b() { return 0; } }",
		"file:///Sub.java":  "package a; class Sub extends Base { int s; }",
	})
	members := tools.ListMembers("a.Sub").Members
	var field, method *McpMember
	for i := range members {
		switch members[i].Kind {
		case "field":
			field = &members[i]
		case "method":
			method = &members[i]
		}
	}
	if field == nil || field.Inherited {
		t.Errorf("field = %+v, want inherited=false", field)
	}
	if method == nil || !method.Inherited {
		t.Errorf("method = %+v, want inherited=true", method)
	}
}

func TestFindCallers(t *testing.T) {
	tools := toolsFor(map[string]string{
		"file:///A.java": "package a; class A { void run() { helper(); helper(); } void helper() {} }",
	})
	if c := tools.FindCallers("a.A#helper"); len(c.Callers) != 2 {
		t.Errorf("callers = %d, want 2", len(c.Callers))
	}
}

func TestTypeHierarchy(t *testing.T) {
	tools := toolsFor(map[string]string{
		"file:///I.java": "package a; interface I {}",
		"file:///M.java": "package a; class M implements I {}",
		"file:///N.java": "package a; class N extends M {}",
	})
	h := tools.TypeHierarchy("a.M")
	if sup := labels(h.Supertypes); len(sup) != 1 || sup[0] != "interface I" {
		t.Errorf("supertypes = %v", sup)
	}
	if sub := labels(h.Subtypes); len(sub) != 1 || sub[0] != "class N" {
		t.Errorf("subtypes = %v", sub)
	}
}

func TestResolveImport(t *testing.T) {
	tools := toolsFor(map[string]string{
		"file:///MyList.java": "package a.util; class MyList {}",
		"file:///Other.java":  "package b; class MyList {}",
	})
	imports := tools.ResolveImport("MyList")
	if len(imports) != 2 || imports[0] != "a.util.MyList" || imports[1] != "b.MyList" {
		t.Errorf("imports = %v", imports)
	}
}

func TestMcpRenameSymbol(t *testing.T) {
	tools := toolsFor(map[string]string{
		"file:///A.java": "package a; class A { int x; void m() { x = x + 1; } }",
	})
	r := tools.RenameSymbol("a.A#x", "y")
	if len(r.Edits) != 3 {
		t.Fatalf("edits = %d, want 3", len(r.Edits))
	}
	for _, e := range r.Edits {
		if e.NewText != "y" {
			t.Errorf("edit newText = %q, want y", e.NewText)
		}
	}
	if r.Edits[0].File != "/A.java" {
		t.Errorf("edit file = %q", r.Edits[0].File)
	}
}

func TestMcpRenameInvalidIdentifier(t *testing.T) {
	tools := toolsFor(map[string]string{"file:///A.java": "package a; class A { int x; }"})
	r := tools.RenameSymbol("a.A#x", "1bad")
	if !contains(r.Error, "valid Java identifier") || len(r.Edits) != 0 {
		t.Errorf("result = %+v", r)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
