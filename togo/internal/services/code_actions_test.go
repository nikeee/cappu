package services

import (
	"sort"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/compiler"
)

// Port of src/services/codeActions.test.ts.

type actionCtx struct {
	program *compiler.Program
	checker *compiler.Checker
	text    string
}

func actionsSetup(text string, extra map[string]string) *actionCtx {
	program := compiler.NewProgram()
	compiler.LoadJdkStub(program)
	for uri, t := range extra {
		program.AddProjectFile(compiler.URI(uri), t)
	}
	program.SetOpenDocument("file:///T.java", text, 1)
	return &actionCtx{program: program, checker: compiler.NewChecker(program), text: text}
}

func apply(text string, action CodeActionResult) string {
	changes := append([]TextChange{}, action.Changes...)
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].Start > changes[j].Start })
	out := text
	for _, c := range changes {
		out = out[:c.Start] + c.NewText + out[c.End:]
	}
	return out
}

func (ctx *actionCtx) actionsAt(needle string, occ int) []CodeActionResult {
	offset := -1
	for i := 0; i < occ; i++ {
		offset = strings.Index(ctx.text[offset+1:], needle) + offset + 1
	}
	sf := ctx.program.GetSourceFile("file:///T.java")
	return GetCodeActions(ctx.program, ctx.checker, sf, offset, offset)
}

func filterKind(actions []CodeActionResult, kind string) []CodeActionResult {
	var out []CodeActionResult
	for _, a := range actions {
		if a.Kind == kind {
			out = append(out, a)
		}
	}
	return out
}

func findKind(actions []CodeActionResult, kind string) *CodeActionResult {
	for i := range actions {
		if actions[i].Kind == kind {
			return &actions[i]
		}
	}
	return nil
}

func findTitle(actions []CodeActionResult, title string) *CodeActionResult {
	for i := range actions {
		if actions[i].Title == title {
			return &actions[i]
		}
	}
	return nil
}

func TestRemoveUnusedImportDeletesLine(t *testing.T) {
	text := "import java.util.List;\nimport java.util.Map;\n\nclass T { Map<String, String> m; }\n"
	ctx := actionsSetup(text, nil)
	actions := filterKind(ctx.actionsAt("java.util.List", 1), "quickfix")
	if len(actions) != 1 || actions[0].Title != "Remove unused import 'java.util.List'" {
		t.Fatalf("actions = %+v", actions)
	}
	if got := apply(text, actions[0]); got != "import java.util.Map;\n\nclass T { Map<String, String> m; }\n" {
		t.Errorf("apply = %q", got)
	}
}

func TestNoRemovalForUsedImport(t *testing.T) {
	ctx := actionsSetup("import java.util.Map;\n\nclass T { Map<String, String> m; }\n", nil)
	if got := filterKind(ctx.actionsAt("java.util.Map", 1), "quickfix"); len(got) != 0 {
		t.Errorf("used import should offer no removal, got %+v", got)
	}
	ctx2 := actionsSetup("import java.util.List;\n\nclass T { int x; }\n", nil)
	if got := filterKind(ctx2.actionsAt("int x", 1), "quickfix"); len(got) != 0 {
		t.Errorf("offset outside import should offer nothing, got %+v", got)
	}
}

func TestOffersImportForUnresolvedType(t *testing.T) {
	ctx := actionsSetup("package app;\nclass C { java_unused; List<String> xs; }", nil)
	actions := filterKind(ctx.actionsAt("List", 1), "quickfix")
	found := false
	for _, a := range actions {
		if a.Title == "Import 'java.util.List'" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Import 'java.util.List', got %+v", actions)
	}
}

func TestInsertImportAfterExisting(t *testing.T) {
	ctx := actionsSetup("package app;\n\nimport java.util.Map;\n\nclass C { List<String> xs; }", nil)
	action := findTitle(ctx.actionsAt("List", 1), "Import 'java.util.List'")
	if action == nil {
		t.Fatal("no import action")
	}
	want := "package app;\n\nimport java.util.Map;\nimport java.util.List;\n\nclass C { List<String> xs; }"
	if got := apply(ctx.text, *action); got != want {
		t.Errorf("apply = %q", got)
	}
}

func TestInsertImportAfterPackage(t *testing.T) {
	ctx := actionsSetup("package app;\n\nclass C { List<String> xs; }", nil)
	action := findTitle(ctx.actionsAt("List", 1), "Import 'java.util.List'")
	if action == nil {
		t.Fatal("no import action")
	}
	want := "package app;\n\nimport java.util.List;\n\nclass C { List<String> xs; }"
	if got := apply(ctx.text, *action); got != want {
		t.Errorf("apply = %q", got)
	}
}

func TestNoImportForResolvedType(t *testing.T) {
	ctx := actionsSetup("package app;\nimport java.util.List;\nclass C { List<String> xs; }", nil)
	if got := filterKind(ctx.actionsAt("List", 2), "quickfix"); len(got) != 0 {
		t.Errorf("resolved type should offer no import, got %+v", got)
	}
}

func TestNoImportForSamePackage(t *testing.T) {
	ctx := actionsSetup("package app;\nclass C { Helper h; }", map[string]string{
		"file:///Helper.java": "package app;\npublic class Helper {}",
	})
	if got := filterKind(ctx.actionsAt("Helper", 1), "quickfix"); len(got) != 0 {
		t.Errorf("same-package type should offer no import, got %+v", got)
	}
}

func TestNoImportForJavaLang(t *testing.T) {
	ctx := actionsSetup("package app;\nclass C { String s; }", nil)
	if got := filterKind(ctx.actionsAt("String", 1), "quickfix"); len(got) != 0 {
		t.Errorf("java.lang type should offer no import, got %+v", got)
	}
}

func (ctx *actionCtx) organize() *CodeActionResult {
	return findKind(ctx.actionsAt("class", 1), "source.organizeImports")
}

func TestOrganizeRemovesUnused(t *testing.T) {
	ctx := actionsSetup("package app;\nimport java.util.List;\nimport java.util.Map;\nclass C { List<String> xs; }", nil)
	action := ctx.organize()
	if action == nil {
		t.Fatal("no organize action")
	}
	if got := apply(ctx.text, *action); got != "package app;\nimport java.util.List;\nclass C { List<String> xs; }" {
		t.Errorf("apply = %q", got)
	}
}

func TestOrganizeSortsKeepsOnDemandStatic(t *testing.T) {
	ctx := actionsSetup("package app;\nimport java.util.Map;\nimport static java.lang.Math.max;\nimport java.util.*;\nimport java.util.List;\nclass C { List<String> xs; Map<String,String> m; }", nil)
	action := ctx.organize()
	if action == nil {
		t.Fatal("no organize action")
	}
	want := "package app;\nimport java.util.*;\nimport java.util.List;\nimport java.util.Map;\nimport static java.lang.Math.max;\nclass C { List<String> xs; Map<String,String> m; }"
	if got := apply(ctx.text, *action); got != want {
		t.Errorf("apply = %q", got)
	}
}

func TestNoOrganizeWhenSorted(t *testing.T) {
	ctx := actionsSetup("package app;\nimport java.util.List;\nclass C { List<String> xs; }", nil)
	if ctx.organize() != nil {
		t.Error("already-sorted imports should offer no organize action")
	}
}

func (ctx *actionCtx) extractAction(exprText string, occ int) *CodeActionResult {
	start := -1
	for i := 0; i < occ; i++ {
		start = strings.Index(ctx.text[start+1:], exprText) + start + 1
	}
	sf := ctx.program.GetSourceFile("file:///T.java")
	return findKind(GetCodeActions(ctx.program, ctx.checker, sf, start, start+len(exprText)), "refactor.extract")
}

func TestExtractBinaryExpression(t *testing.T) {
	ctx := actionsSetup(strings.Join([]string{"class C {", "  int m() {", "    int b = compute() + 1;", "    return b;", "  }", "}"}, "\n"), nil)
	action := ctx.extractAction("compute() + 1", 1)
	if action == nil {
		t.Fatal("no extract action")
	}
	want := strings.Join([]string{"class C {", "  int m() {", "    var extracted = compute() + 1;", "    int b = extracted;", "    return b;", "  }", "}"}, "\n")
	if got := apply(ctx.text, *action); got != want {
		t.Errorf("apply = %q", got)
	}
}

func TestExtractCallArgument(t *testing.T) {
	ctx := actionsSetup(strings.Join([]string{"class C {", "  void m() {", "    use(a * b + c);", "  }", "}"}, "\n"), nil)
	action := ctx.extractAction("a * b + c", 1)
	if action == nil {
		t.Fatal("no extract action")
	}
	want := strings.Join([]string{"class C {", "  void m() {", "    var extracted = a * b + c;", "    use(extracted);", "  }", "}"}, "\n")
	if got := apply(ctx.text, *action); got != want {
		t.Errorf("apply = %q", got)
	}
}

func TestNoExtractForPartialExpression(t *testing.T) {
	ctx := actionsSetup(strings.Join([]string{"class C {", "  void m() {", "    use(a + b);", "  }", "}"}, "\n"), nil)
	if ctx.extractAction("a +", 1) != nil {
		t.Error("partial expression should offer no extract")
	}
}

func TestNoExtractOutsideBlock(t *testing.T) {
	ctx := actionsSetup("class C { int f = 1 + 2; }", nil)
	if ctx.extractAction("1 + 2", 1) != nil {
		t.Error("field initializer should offer no extract")
	}
}

func (ctx *actionCtx) inlineAt(needle string, occ int) *CodeActionResult {
	offset := -1
	for i := 0; i < occ; i++ {
		offset = strings.Index(ctx.text[offset+1:], needle) + offset + 1
	}
	sf := ctx.program.GetSourceFile("file:///T.java")
	return findKind(GetCodeActions(ctx.program, ctx.checker, sf, offset, offset), "refactor.inline")
}

func TestInlineSingleUse(t *testing.T) {
	ctx := actionsSetup(strings.Join([]string{"class C {", "  int m() {", "    int total = 1;", "    return total + 2;", "  }", "}"}, "\n"), nil)
	action := ctx.inlineAt("total", 1)
	if action == nil {
		t.Fatal("no inline action")
	}
	want := strings.Join([]string{"class C {", "  int m() {", "    return 1 + 2;", "  }", "}"}, "\n")
	if got := apply(ctx.text, *action); got != want {
		t.Errorf("apply = %q", got)
	}
}

func TestInlineMultipleUses(t *testing.T) {
	ctx := actionsSetup(strings.Join([]string{"class C {", "  void m() {", "    String msg = name();", "    use(msg, msg);", "  }", "}"}, "\n"), nil)
	action := ctx.inlineAt("msg", 1)
	if action == nil {
		t.Fatal("no inline action")
	}
	want := strings.Join([]string{"class C {", "  void m() {", "    use(name(), name());", "  }", "}"}, "\n")
	if got := apply(ctx.text, *action); got != want {
		t.Errorf("apply = %q", got)
	}
}

func TestInlineWrapsCompound(t *testing.T) {
	ctx := actionsSetup(strings.Join([]string{"class C {", "  int m() {", "    int sum = a + b;", "    return sum * 2;", "  }", "}"}, "\n"), nil)
	action := ctx.inlineAt("sum", 1)
	if action == nil {
		t.Fatal("no inline action")
	}
	want := strings.Join([]string{"class C {", "  int m() {", "    return (a + b) * 2;", "  }", "}"}, "\n")
	if got := apply(ctx.text, *action); got != want {
		t.Errorf("apply = %q", got)
	}
}

func TestNoInlineWhenReassigned(t *testing.T) {
	ctx := actionsSetup(strings.Join([]string{"class C {", "  void m() {", "    int n = 1;", "    n = 2;", "    use(n);", "  }", "}"}, "\n"), nil)
	if ctx.inlineAt("n ", 1) != nil {
		t.Error("reassigned local should offer no inline")
	}
}

func TestNoInlineWithoutInitializer(t *testing.T) {
	ctx := actionsSetup(strings.Join([]string{"class C {", "  void m() {", "    int x;", "    use(x);", "  }", "}"}, "\n"), nil)
	if ctx.inlineAt("x", 1) != nil {
		t.Error("local without initializer should offer no inline")
	}
}

func (ctx *actionCtx) rewriteAt(needle string, occ int) *CodeActionResult {
	offset := -1
	for i := 0; i < occ; i++ {
		offset = strings.Index(ctx.text[offset+1:], needle) + offset + 1
	}
	sf := ctx.program.GetSourceFile("file:///T.java")
	return findKind(GetCodeActions(ctx.program, ctx.checker, sf, offset, offset), "refactor.rewrite")
}

func TestRemoveUnusedMiddleParameter(t *testing.T) {
	ctx := actionsSetup("class C { void m(int aa, int bb, int cc) { use(aa, cc); } void caller() { m(1, 2, 3); } }", nil)
	action := ctx.rewriteAt("bb", 1)
	if action == nil || action.Title != "Remove unused parameter 'bb'" {
		t.Fatalf("action = %+v", action)
	}
	want := "class C { void m(int aa, int cc) { use(aa, cc); } void caller() { m(1, 3); } }"
	if got := apply(ctx.text, *action); got != want {
		t.Errorf("apply = %q", got)
	}
}

func TestRemoveUnusedLastParameter(t *testing.T) {
	ctx := actionsSetup("class C { void m(int aa, int bb) { use(aa); } void caller() { m(1, 2); } }", nil)
	action := ctx.rewriteAt("bb", 1)
	if action == nil {
		t.Fatal("no rewrite action")
	}
	if got := apply(ctx.text, *action); got != "class C { void m(int aa) { use(aa); } void caller() { m(1); } }" {
		t.Errorf("apply = %q", got)
	}
}

func TestRemoveOnlyParameter(t *testing.T) {
	ctx := actionsSetup("class C { void m(int aa) {} void caller() { m(1); } }", nil)
	action := ctx.rewriteAt("aa", 1)
	if action == nil {
		t.Fatal("no rewrite action")
	}
	if got := apply(ctx.text, *action); got != "class C { void m() {} void caller() { m(); } }" {
		t.Errorf("apply = %q", got)
	}
}

func TestNoRemoveWhenParameterUsed(t *testing.T) {
	ctx := actionsSetup("class C { void m(int aa) { use(aa); } }", nil)
	if ctx.rewriteAt("aa", 1) != nil {
		t.Error("used parameter should offer no remove")
	}
}

func TestNoRemoveForOverloadedMethod(t *testing.T) {
	ctx := actionsSetup("class C { void m(int aa) {} void m(int aa, int bb) {} }", nil)
	if ctx.rewriteAt("aa", 1) != nil {
		t.Error("overloaded method should offer no remove-parameter")
	}
}

func TestRemoveRedundantOverride(t *testing.T) {
	text := "class Base { void real() {} }\n" +
		"class T extends Base {\n" +
		"  @Override void notThere() {}\n" +
		"  @Override void real() {}\n" +
		"}"
	ctx := actionsSetup(text, nil)
	actions := filterKind(ctx.actionsAt("notThere", 1), "quickfix")
	if len(actions) != 1 || actions[0].Title != "Remove redundant '@Override'" {
		t.Fatalf("actions = %+v", actions)
	}
	want := "class Base { void real() {} }\n" +
		"class T extends Base {\n" +
		"  void notThere() {}\n" +
		"  @Override void real() {}\n" +
		"}"
	if got := apply(text, actions[0]); got != want {
		t.Errorf("apply =\n%s", got)
	}
}

func TestNoRemoveOverrideOnRealOverride(t *testing.T) {
	text := "class Base { void real() {} }\nclass T extends Base { @Override void real() {} }"
	ctx := actionsSetup(text, nil)
	if a := filterKind(ctx.actionsAt("real", 2), "quickfix"); len(a) != 0 {
		t.Errorf("expected no quickfix on a real override, got %+v", a)
	}
}
