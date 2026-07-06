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

// expectEdit applies the proposed edit and asserts it is correct: the result
// equals want AND re-parses as syntactically valid Java (no parse diagnostics),
// so a rewrite can never silently emit broken code.
func expectEdit(t *testing.T, text string, action CodeActionResult, want string) {
	t.Helper()
	out := apply(text, action)
	program := compiler.NewProgram()
	compiler.LoadJdkStub(program)
	program.SetOpenDocument("file:///T.java", out, 1)
	if diags := program.GetSourceFile("file:///T.java").AsSourceFile().ParseDiagnostics; len(diags) > 0 {
		t.Errorf("edit produced invalid Java (%d parse diagnostics):\n%s", len(diags), out)
	}
	if out != want {
		t.Errorf("apply =\n%s", out)
	}
}

func (ctx *actionCtx) actionsAt(needle string, occ int, release ...*int) []CodeActionResult {
	offset := -1
	for i := 0; i < occ; i++ {
		offset = strings.Index(ctx.text[offset+1:], needle) + offset + 1
	}
	sf := ctx.program.GetSourceFile("file:///T.java")
	var r *int
	if len(release) > 0 {
		r = release[0]
	}
	return GetCodeActions(ctx.program, ctx.checker, sf, offset, offset, NewLanguageFeatures(r))
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
	return findKind(GetCodeActions(ctx.program, ctx.checker, sf, start, start+len(exprText), NewLanguageFeatures(nil)), "refactor.extract")
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
	return findKind(GetCodeActions(ctx.program, ctx.checker, sf, offset, offset, NewLanguageFeatures(nil)), "refactor.inline")
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
	return findKind(GetCodeActions(ctx.program, ctx.checker, sf, offset, offset, NewLanguageFeatures(nil)), "refactor.rewrite")
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

// Port of the make-field-final tests in src/services/codeActions.test.ts
// (nikeee/cappu#38).

func TestMakeFieldFinalWithInitializer(t *testing.T) {
	text := "class T {\n  private int x = 1;\n  int use() { return x; }\n}"
	ctx := actionsSetup(text, nil)
	actions := filterKind(ctx.actionsAt("x = 1", 1), "quickfix")
	if len(actions) != 1 || actions[0].Title != "Add 'final' modifier" {
		t.Fatalf("actions = %+v", actions)
	}
	want := "class T {\n  private final int x = 1;\n  int use() { return x; }\n}"
	if got := apply(text, actions[0]); got != want {
		t.Errorf("apply =\n%s", got)
	}
}

func TestMakeFieldFinalAfterAllModifiers(t *testing.T) {
	text := "class T {\n  @Deprecated private static int N = 1;\n}"
	ctx := actionsSetup(text, nil)
	actions := filterKind(ctx.actionsAt("N = 1", 1), "quickfix")
	if len(actions) != 1 || actions[0].Title != "Add 'final' modifier" {
		t.Fatalf("actions = %+v", actions)
	}
	want := "class T {\n  @Deprecated private static final int N = 1;\n}"
	if got := apply(text, actions[0]); got != want {
		t.Errorf("apply =\n%s", got)
	}
}

func TestMakeFieldFinalCtorAssigned(t *testing.T) {
	text := "class T {\n  private int y;\n  T(int v) { this.y = v; }\n}"
	ctx := actionsSetup(text, nil)
	actions := filterKind(ctx.actionsAt("int y", 1), "quickfix")
	if len(actions) != 1 || actions[0].Title != "Add 'final' modifier" {
		t.Fatalf("actions = %+v", actions)
	}
	want := "class T {\n  private final int y;\n  T(int v) { this.y = v; }\n}"
	if got := apply(text, actions[0]); got != want {
		t.Errorf("apply =\n%s", got)
	}
}

func TestMakeFieldFinalMultiDeclarator(t *testing.T) {
	text := "class T {\n  private int a = 1, b = 2;\n}"
	ctx := actionsSetup(text, nil)
	actions := filterKind(ctx.actionsAt("a = 1", 1), "quickfix")
	if len(actions) != 1 || actions[0].Title != "Add 'final' modifier" {
		t.Fatalf("actions = %+v", actions)
	}
	want := "class T {\n  private final int a = 1, b = 2;\n}"
	if got := apply(text, actions[0]); got != want {
		t.Errorf("apply =\n%s", got)
	}
}

func TestNoMakeFieldFinalWhenNotApplicable(t *testing.T) {
	reassigned := actionsSetup("class T {\n  private int x = 1;\n  void m() { x = 2; }\n}", nil)
	if a := filterKind(reassigned.actionsAt("x = 1", 1), "quickfix"); len(a) != 0 {
		t.Errorf("expected no quickfix on a reassigned field, got %+v", a)
	}
	alreadyFinal := actionsSetup("class T {\n  private final int x = 1;\n}", nil)
	if a := filterKind(alreadyFinal.actionsAt("x = 1", 1), "quickfix"); len(a) != 0 {
		t.Errorf("expected no quickfix on a final field, got %+v", a)
	}
	elsewhere := actionsSetup("class T {\n  private int x = 1;\n  void m() { int local = 2; }\n}", nil)
	if a := filterKind(elsewhere.actionsAt("local", 1), "quickfix"); len(a) != 0 {
		t.Errorf("expected no quickfix outside the field, got %+v", a)
	}
}

// --- convert class to record -------------------------------------------------------

const pointSrc = "class Point {\n" +
	"  private final int x;\n" +
	"  private final int y;\n" +
	"  Point(int x, int y) { this.x = x; this.y = y; }\n" +
	"  public int getX() { return x; }\n" +
	"  public int getY() { return this.y; }\n" +
	"}\n"

func recordAction(actions []CodeActionResult) *CodeActionResult {
	for i := range actions {
		if actions[i].Title == "Convert class to record" {
			return &actions[i]
		}
	}
	return nil
}

func TestConvertClassToRecordGatedOnRelease(t *testing.T) {
	ctx := actionsSetup(pointSrc, nil)
	fifteen, sixteen := 15, 16
	if got := recordAction(ctx.actionsAt("class Point", 1, &fifteen)); got != nil {
		t.Errorf("release 15: expected no action, got %+v", got)
	}
	if got := recordAction(ctx.actionsAt("class Point", 1, &sixteen)); got == nil {
		t.Error("release 16: expected a convert-to-record action")
	}
}

func TestConvertClassToRecord(t *testing.T) {
	ctx := actionsSetup(pointSrc, nil)
	action := recordAction(ctx.actionsAt("class Point", 1))
	if action == nil {
		t.Fatal("expected a convert-to-record action")
	}
	if action.Kind != "refactor.rewrite" {
		t.Errorf("kind = %q, want refactor.rewrite", action.Kind)
	}
	if got := apply(pointSrc, *action); got != "record Point(int x, int y) {\n}\n" {
		t.Errorf("apply = %q", got)
	}
}

func TestConvertClassToRecordPreservesHeader(t *testing.T) {
	text := "public class Box<T> implements java.io.Serializable {\n" +
		"  private final T v;\n" +
		"  public Box(T v) { this.v = v; }\n" +
		"  public T getV() { return v; }\n" +
		"}\n"
	ctx := actionsSetup(text, nil)
	action := recordAction(ctx.actionsAt("class Box", 1))
	if action == nil {
		t.Fatal("expected a convert-to-record action")
	}
	want := "public record Box<T>(T v) implements java.io.Serializable {\n}\n"
	if got := apply(text, *action); got != want {
		t.Errorf("apply = %q, want %q", got, want)
	}
}

func TestConvertClassToRecordBooleanIsAccessor(t *testing.T) {
	text := "class Flag {\n" +
		"  private final boolean on;\n" +
		"  Flag(boolean on) { this.on = on; }\n" +
		"  public boolean isOn() { return on; }\n" +
		"}\n"
	ctx := actionsSetup(text, nil)
	action := recordAction(ctx.actionsAt("class Flag", 1))
	if action == nil {
		t.Fatal("expected a convert-to-record action")
	}
	if got := apply(text, *action); got != "record Flag(boolean on) {\n}\n" {
		t.Errorf("apply = %q", got)
	}
}

func TestConvertClassToRecordRenamesCrossFile(t *testing.T) {
	other := "class U { int m(Point p) { return p.getX() + p.getY(); } }\n"
	ctx := actionsSetup(pointSrc, map[string]string{"file:///U.java": other})
	action := recordAction(ctx.actionsAt("class Point", 1))
	if action == nil {
		t.Fatal("expected a convert-to-record action")
	}
	edits := action.AdditionalEdits["file:///U.java"]
	if len(edits) == 0 {
		t.Fatal("expected cross-file edits for U.java")
	}
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].Start > edits[j].Start })
	out := other
	for _, c := range edits {
		out = out[:c.Start] + c.NewText + out[c.End:]
	}
	if want := "class U { int m(Point p) { return p.x() + p.y(); } }\n"; out != want {
		t.Errorf("renamed = %q, want %q", out, want)
	}
}

func TestConvertClassToRecordNotOffered(t *testing.T) {
	cases := []string{
		"class C { private final int x = 5; C(int x) { this.x = x; } public int getX() { return x; } }\n",
		"class C { private int x; C(int x) { this.x = x; } public int getX() { return x; } }\n",
		"class C { final int x; C(int x) { this.x = x; } public int getX() { return x; } }\n",
		"class C { static int Z; private final int x; C(int x) { this.x = x; } public int getX() { return x; } }\n",
		"class C { private final int x; C(int x) { this.x = x; } public int getX() { return x; } public void run() {} }\n",
		"class C extends B { private final int x; C(int x) { this.x = x; } public int getX() { return x; } }\n",
		"abstract class C { private final int x; C(int x) { this.x = x; } public int getX() { return x; } }\n",
		"class C { private final int x; private final int y; C(int y, int x) { this.x = x; this.y = y; } }\n",
		"class C { private final int x; C(int x) throws Exception { this.x = x; } public int getX() { return x; } }\n",
		"class C { private final int x; C(int x) { this.x = x; run(); } public int getX() { return x; } }\n",
		"class C { private final int x; C(int x) { this.x = x; } public int getX() { return x + 1; } }\n",
		"class C { private final int x; public int getX() { return x; } }\n",
		// getter body returns a literal, not the field
		"class C { private final int x; C(int x) { this.x = x; } public int getX() { return 0; } }\n",
		// getter body has more than one statement
		"class C { private final int x; C(int x) { this.x = x; } public int getX() { log(); return x; } }\n",
		// isX accessor on a non-boolean field
		"class C { private final int x; C(int x) { this.x = x; } public int isX() { return x; } }\n",
		// getter maps to no declared field
		"class C { private final int x; C(int x) { this.x = x; } public int getZ() { return x; } }\n",
		// getter takes a parameter
		"class C { private final int x; C(int x) { this.x = x; } public int getX(int i) { return x; } }\n",
		// generic getter
		"class C { private final int x; C(int x) { this.x = x; } public <T> int getX() { return x; } }\n",
		// getter declares throws
		"class C { private final int x; C(int x) { this.x = x; } public int getX() throws Exception { return x; } }\n",
		// static getter
		"class C { private final int x; C(int x) { this.x = x; } public static int getX() { return x; } }\n",
		// field carrying an annotation
		"class C { @Deprecated private final int x; C(int x) { this.x = x; } public int getX() { return x; } }\n",
		// constructor parameter type differs from the field type
		"class C { private final int x; C(long x) { this.x = x; } public int getX() { return x; } }\n",
		// varargs constructor parameter
		"class C { private final int[] x; C(int... x) { this.x = x; } public int[] getX() { return x; } }\n",
		// more than one constructor
		"class C { private final int x; C(int x) { this.x = x; } C() { this.x = 0; } public int getX() { return x; } }\n",
		// a field assigned twice while another is never assigned
		"class C { private final int x; private final int y; C(int x, int y) { this.x = x; this.x = y; } }\n",
		// non-static inner class cannot be a record
		"class O { class C { private final int x; C(int x) { this.x = x; } public int getX() { return x; } } }\n",
	}
	for _, text := range cases {
		ctx := actionsSetup(text, nil)
		if a := recordAction(ctx.actionsAt("class C", 1)); a != nil {
			t.Errorf("unexpected action for %q", text)
		}
	}
}

func TestConvertClassToRecordFieldWithoutGetter(t *testing.T) {
	text := "class P {\n  private final int x;\n  private final int y;\n  P(int x, int y) { this.x = x; this.y = y; }\n  public int getX() { return x; }\n}\n"
	ctx := actionsSetup(text, nil)
	action := recordAction(ctx.actionsAt("class P", 1))
	if action == nil {
		t.Fatal("expected a convert-to-record action")
	}
	if got := apply(text, *action); got != "record P(int x, int y) {\n}\n" {
		t.Errorf("apply = %q", got)
	}
}

func TestConvertClassToRecordBareNameAssignment(t *testing.T) {
	text := "class P { private final int v; P(int v) { v = v; } public int getV() { return v; } }\n"
	ctx := actionsSetup(text, nil)
	action := recordAction(ctx.actionsAt("class P", 1))
	if action == nil {
		t.Fatal("expected a convert-to-record action")
	}
	if got := apply(text, *action); got != "record P(int v) {\n}\n" {
		t.Errorf("apply = %q", got)
	}
}

func TestConvertClassToRecordMultipleInterfaces(t *testing.T) {
	text := "class M implements A, B { private final int x; M(int x) { this.x = x; } public int getX() { return x; } }\n"
	ctx := actionsSetup(text, nil)
	action := recordAction(ctx.actionsAt("class M", 1))
	if action == nil {
		t.Fatal("expected a convert-to-record action")
	}
	if got := apply(text, *action); got != "record M(int x) implements A, B {\n}\n" {
		t.Errorf("apply = %q", got)
	}
}

func TestConvertClassToRecordStaticNested(t *testing.T) {
	text := "class Outer {\n  static class Inner { private final int x; Inner(int x) { this.x = x; } public int getX() { return x; } }\n}\n"
	ctx := actionsSetup(text, nil)
	action := recordAction(ctx.actionsAt("class Inner", 1))
	if action == nil {
		t.Fatal("expected a convert-to-record action")
	}
	want := "class Outer {\n  static record Inner(int x) {\n}\n}\n"
	if got := apply(text, *action); got != want {
		t.Errorf("apply = %q, want %q", got, want)
	}
}

func TestConvertClassToRecordRenamesSameFile(t *testing.T) {
	text := "class P { private final int x; P(int x) { this.x = x; } public int getX() { return x; } }\n" +
		"class Q { int m(P p) { return p.getX(); } }\n"
	ctx := actionsSetup(text, nil)
	action := recordAction(ctx.actionsAt("class P", 1))
	if action == nil {
		t.Fatal("expected a convert-to-record action")
	}
	want := "record P(int x) {\n}\nclass Q { int m(P p) { return p.x(); } }\n"
	if got := apply(text, *action); got != want {
		t.Errorf("apply = %q, want %q", got, want)
	}
}

func TestConvertClassToRecordNotOnRecordOrAway(t *testing.T) {
	rec := actionsSetup("record R(int x) {}\n", nil)
	if a := recordAction(rec.actionsAt("record R", 1)); a != nil {
		t.Error("unexpected action on a record")
	}
	imp := actionsSetup("import java.util.List;\nclass C { private final int x; C(int x){this.x=x;} }\n", nil)
	if a := recordAction(imp.actionsAt("import", 1)); a != nil {
		t.Error("unexpected action away from a class")
	}
}

func TestConvertClassToRecordNotWhenExtended(t *testing.T) {
	base := "class Base {\n  private final int x;\n  Base(int x) { this.x = x; }\n  public int getX() { return x; }\n}\n"
	ctx := actionsSetup(base, map[string]string{"file:///Sub.java": "class Sub extends Base {}\n"})
	if a := recordAction(ctx.actionsAt("class Base", 1)); a != nil {
		t.Error("unexpected action for an extended class")
	}
}

// --- use 'var' for a local variable ------------------------------------------

func varActions(actions []CodeActionResult) []CodeActionResult {
	var out []CodeActionResult
	for _, a := range actions {
		if a.Title == "Use 'var' for local variable" {
			out = append(out, a)
		}
	}
	return out
}

func TestVarOfferedForConstructorCall(t *testing.T) {
	text := "class T {\n  void m() {\n    java.util.ArrayList<String> xs = new java.util.ArrayList<String>();\n  }\n}"
	ctx := actionsSetup(text, nil)
	actions := varActions(ctx.actionsAt("xs =", 1))
	if len(actions) != 1 {
		t.Fatalf("actions = %+v", actions)
	}
	expectEdit(t, text, actions[0], "class T {\n  void m() {\n    var xs = new java.util.ArrayList<String>();\n  }\n}")
}

func TestVarOfferedForCast(t *testing.T) {
	text := "class T {\n  void m(Object o) {\n    String s = (String) o;\n  }\n}"
	ctx := actionsSetup(text, nil)
	actions := varActions(ctx.actionsAt("s =", 1))
	if len(actions) != 1 {
		t.Fatalf("actions = %+v", actions)
	}
	expectEdit(t, text, actions[0], "class T {\n  void m(Object o) {\n    var s = (String) o;\n  }\n}")
}

func TestVarOfferedForLiteralKeepsFinal(t *testing.T) {
	text := "class T {\n  void m() {\n    final int n = 42;\n  }\n}"
	ctx := actionsSetup(text, nil)
	actions := varActions(ctx.actionsAt("n =", 1))
	if len(actions) != 1 {
		t.Fatalf("actions = %+v", actions)
	}
	expectEdit(t, text, actions[0], "class T {\n  void m() {\n    final var n = 42;\n  }\n}")
}

func TestVarNotOfferedForDiamond(t *testing.T) {
	text := "class T {\n  void m() {\n    java.util.List<String> xs = new java.util.ArrayList<>();\n  }\n}"
	ctx := actionsSetup(text, nil)
	if got := varActions(ctx.actionsAt("xs =", 1)); len(got) != 0 {
		t.Errorf("expected no action, got %+v", got)
	}
}

func TestVarNotOfferedWhenAlreadyVar(t *testing.T) {
	text := "class T {\n  void m() {\n    var s = (String) null;\n  }\n}"
	ctx := actionsSetup(text, nil)
	if got := varActions(ctx.actionsAt("s =", 1)); len(got) != 0 {
		t.Errorf("expected no action, got %+v", got)
	}
}

func TestVarNotOfferedForMethodCall(t *testing.T) {
	text := "class T {\n  int f() { return 1; }\n  void m() {\n    int n = f();\n  }\n}"
	ctx := actionsSetup(text, nil)
	if got := varActions(ctx.actionsAt("n =", 1)); len(got) != 0 {
		t.Errorf("expected no action, got %+v", got)
	}
}

func TestVarGatedOnRelease(t *testing.T) {
	text := "class T {\n  void m() {\n    String s = (String) null;\n  }\n}"
	ctx := actionsSetup(text, nil)
	nine, ten := 9, 10
	if got := varActions(ctx.actionsAt("s =", 1, &nine)); len(got) != 0 {
		t.Errorf("release 9: expected no action, got %+v", got)
	}
	if got := varActions(ctx.actionsAt("s =", 1, &ten)); len(got) != 1 {
		t.Errorf("release 10: expected 1 action, got %+v", got)
	}
}

// --- convert anonymous class to lambda ---------------------------------------

func lambdaActions(actions []CodeActionResult) []CodeActionResult {
	var out []CodeActionResult
	for _, a := range actions {
		if a.Title == "Convert anonymous class to lambda" {
			out = append(out, a)
		}
	}
	return out
}

func TestLambdaConvertsRunnable(t *testing.T) {
	text := "class T {\n  Runnable r = new Runnable() {\n    public void run() { System.out.println(\"hi\"); }\n  };\n}"
	ctx := actionsSetup(text, nil)
	actions := lambdaActions(ctx.actionsAt("new Runnable", 1))
	if len(actions) != 1 {
		t.Fatalf("actions = %+v", actions)
	}
	expectEdit(t, text, actions[0], "class T {\n  Runnable r = () -> { System.out.println(\"hi\"); };\n}")
}

func TestLambdaConvertsComparatorIgnoringDefaultAndStatic(t *testing.T) {
	text := "class T {\n  java.util.Comparator<String> c = new java.util.Comparator<String>() {\n    public int compare(String a, String b) { return 0; }\n  };\n}"
	ctx := actionsSetup(text, nil)
	actions := lambdaActions(ctx.actionsAt("new java.util.Comparator", 1))
	if len(actions) != 1 {
		t.Fatalf("actions = %+v", actions)
	}
	expectEdit(t, text, actions[0], "class T {\n  java.util.Comparator<String> c = (a, b) -> { return 0; };\n}")
}

func TestLambdaNotOfferedForNonFunctionalInterface(t *testing.T) {
	text := "class T {\n  interface Two { void a(); void b(); }\n  Two t = new Two() { public void a() {} };\n}"
	ctx := actionsSetup(text, nil)
	if got := lambdaActions(ctx.actionsAt("new Two", 1)); len(got) != 0 {
		t.Errorf("expected no action, got %+v", got)
	}
}

func TestLambdaNotOfferedWithExtraMember(t *testing.T) {
	text := "class T {\n  Runnable r = new Runnable() {\n    int x = 1;\n    public void run() {}\n  };\n}"
	ctx := actionsSetup(text, nil)
	if got := lambdaActions(ctx.actionsAt("new Runnable", 1)); len(got) != 0 {
		t.Errorf("expected no action, got %+v", got)
	}
}

func TestLambdaNotOfferedWhenBodyUsesThis(t *testing.T) {
	text := "class T {\n  Runnable r = new Runnable() {\n    public void run() { this.hashCode(); }\n  };\n}"
	ctx := actionsSetup(text, nil)
	if got := lambdaActions(ctx.actionsAt("new Runnable", 1)); len(got) != 0 {
		t.Errorf("expected no action, got %+v", got)
	}
}

func TestLambdaNotOfferedForNonInterface(t *testing.T) {
	text := "class T {\n  abstract static class A { abstract void go(); }\n  A a = new A() { void go() {} };\n}"
	ctx := actionsSetup(text, nil)
	if got := lambdaActions(ctx.actionsAt("new A", 1)); len(got) != 0 {
		t.Errorf("expected no action, got %+v", got)
	}
}

func TestLambdaGatedOnRelease(t *testing.T) {
	text := "class T {\n  Runnable r = new Runnable() {\n    public void run() {}\n  };\n}"
	ctx := actionsSetup(text, nil)
	seven, eight := 7, 8
	if got := lambdaActions(ctx.actionsAt("new Runnable", 1, &seven)); len(got) != 0 {
		t.Errorf("release 7: expected no action, got %+v", got)
	}
	if got := lambdaActions(ctx.actionsAt("new Runnable", 1, &eight)); len(got) != 1 {
		t.Errorf("release 8: expected 1 action, got %+v", got)
	}
}

// --- convert instanceof + cast to a pattern binding --------------------------

func patternActions(actions []CodeActionResult) []CodeActionResult {
	var out []CodeActionResult
	for _, a := range actions {
		if a.Title == "Replace cast with pattern binding" {
			out = append(out, a)
		}
	}
	return out
}

func TestPatternFoldsInstanceofAndCast(t *testing.T) {
	text := "class T {\n  void m(Object o) {\n    if (o instanceof String) {\n      String s = (String) o;\n      System.out.println(s);\n    }\n  }\n}"
	ctx := actionsSetup(text, nil)
	actions := patternActions(ctx.actionsAt("instanceof", 1))
	if len(actions) != 1 {
		t.Fatalf("actions = %+v", actions)
	}
	expectEdit(t, text, actions[0], "class T {\n  void m(Object o) {\n    if (o instanceof String s) {\n      System.out.println(s);\n    }\n  }\n}")
}

func TestPatternNotOfferedWhenTypeDiffers(t *testing.T) {
	text := "class T {\n  void m(Object o) {\n    if (o instanceof CharSequence) {\n      String s = (String) o;\n    }\n  }\n}"
	ctx := actionsSetup(text, nil)
	if got := patternActions(ctx.actionsAt("instanceof", 1)); len(got) != 0 {
		t.Errorf("expected no action, got %+v", got)
	}
}

func TestPatternNotOfferedWhenOperandDiffers(t *testing.T) {
	text := "class T {\n  void m(Object o, Object p) {\n    if (o instanceof String) {\n      String s = (String) p;\n    }\n  }\n}"
	ctx := actionsSetup(text, nil)
	if got := patternActions(ctx.actionsAt("instanceof", 1)); len(got) != 0 {
		t.Errorf("expected no action, got %+v", got)
	}
}

func TestPatternNotOfferedWhenThenNotBlock(t *testing.T) {
	text := "class T {\n  void m(Object o) {\n    if (o instanceof String) return;\n  }\n}"
	ctx := actionsSetup(text, nil)
	if got := patternActions(ctx.actionsAt("instanceof", 1)); len(got) != 0 {
		t.Errorf("expected no action, got %+v", got)
	}
}

func TestPatternNotOfferedWhenPartialCondition(t *testing.T) {
	text := "class T {\n  void m(Object o, boolean b) {\n    if (o instanceof String && b) {\n      String s = (String) o;\n    }\n  }\n}"
	ctx := actionsSetup(text, nil)
	if got := patternActions(ctx.actionsAt("instanceof", 1)); len(got) != 0 {
		t.Errorf("expected no action, got %+v", got)
	}
}

func TestPatternNotOfferedWhenAlreadyPattern(t *testing.T) {
	text := "class T {\n  void m(Object o) {\n    if (o instanceof String s) {\n      System.out.println(s);\n    }\n  }\n}"
	ctx := actionsSetup(text, nil)
	if got := patternActions(ctx.actionsAt("instanceof", 1)); len(got) != 0 {
		t.Errorf("expected no action, got %+v", got)
	}
}

func TestPatternGatedOnRelease(t *testing.T) {
	text := "class T {\n  void m(Object o) {\n    if (o instanceof String) {\n      String s = (String) o;\n    }\n  }\n}"
	ctx := actionsSetup(text, nil)
	fifteen, sixteen := 15, 16
	if got := patternActions(ctx.actionsAt("instanceof", 1, &fifteen)); len(got) != 0 {
		t.Errorf("release 15: expected no action, got %+v", got)
	}
	if got := patternActions(ctx.actionsAt("instanceof", 1, &sixteen)); len(got) != 1 {
		t.Errorf("release 16: expected 1 action, got %+v", got)
	}
}

// --- use the diamond operator ------------------------------------------------

func diamondActions(actions []CodeActionResult) []CodeActionResult {
	var out []CodeActionResult
	for _, a := range actions {
		if a.Title == "Use diamond operator" {
			out = append(out, a)
		}
	}
	return out
}

func TestDiamondOfferedOnTypedLocal(t *testing.T) {
	text := "class T {\n  void m() {\n    java.util.List<String> xs = new java.util.ArrayList<String>();\n  }\n}"
	ctx := actionsSetup(text, nil)
	actions := diamondActions(ctx.actionsAt("xs =", 1))
	if len(actions) != 1 {
		t.Fatalf("actions = %+v", actions)
	}
	expectEdit(t, text, actions[0], "class T {\n  void m() {\n    java.util.List<String> xs = new java.util.ArrayList<>();\n  }\n}")
}

func TestDiamondOfferedOnField(t *testing.T) {
	text := "class T {\n  java.util.Map<String, Integer> m = new java.util.HashMap<String, Integer>();\n}"
	ctx := actionsSetup(text, nil)
	actions := diamondActions(ctx.actionsAt("m =", 1))
	if len(actions) != 1 {
		t.Fatalf("actions = %+v", actions)
	}
	expectEdit(t, text, actions[0], "class T {\n  java.util.Map<String, Integer> m = new java.util.HashMap<>();\n}")
}

func TestDiamondNotOfferedWhenRhsHasNoArgs(t *testing.T) {
	text := "class T {\n  void m() {\n    java.util.List<String> xs = new java.util.ArrayList();\n  }\n}"
	ctx := actionsSetup(text, nil)
	if got := diamondActions(ctx.actionsAt("xs =", 1)); len(got) != 0 {
		t.Errorf("expected no action, got %+v", got)
	}
}

func TestDiamondNotOfferedWhenAlreadyDiamond(t *testing.T) {
	text := "class T {\n  void m() {\n    java.util.List<String> xs = new java.util.ArrayList<>();\n  }\n}"
	ctx := actionsSetup(text, nil)
	if got := diamondActions(ctx.actionsAt("xs =", 1)); len(got) != 0 {
		t.Errorf("expected no action, got %+v", got)
	}
}

func TestDiamondNotOfferedForVar(t *testing.T) {
	text := "class T {\n  void m() {\n    var xs = new java.util.ArrayList<String>();\n  }\n}"
	ctx := actionsSetup(text, nil)
	if got := diamondActions(ctx.actionsAt("xs =", 1)); len(got) != 0 {
		t.Errorf("expected no action, got %+v", got)
	}
}

func TestDiamondNotOfferedWhenArgsDiffer(t *testing.T) {
	text := "class T {\n  void m() {\n    Object o = new java.util.ArrayList<String>();\n  }\n}"
	ctx := actionsSetup(text, nil)
	if got := diamondActions(ctx.actionsAt("o =", 1)); len(got) != 0 {
		t.Errorf("expected no action, got %+v", got)
	}
}

func TestDiamondNotOfferedForAnonymousBody(t *testing.T) {
	text := "class T {\n  void m() {\n    java.util.List<String> xs = new java.util.ArrayList<String>() {};\n  }\n}"
	ctx := actionsSetup(text, nil)
	if got := diamondActions(ctx.actionsAt("xs =", 1)); len(got) != 0 {
		t.Errorf("expected no action, got %+v", got)
	}
}

func TestDiamondGatedOnRelease(t *testing.T) {
	text := "class T {\n  void m() {\n    java.util.List<String> xs = new java.util.ArrayList<String>();\n  }\n}"
	ctx := actionsSetup(text, nil)
	six, seven := 6, 7
	if got := diamondActions(ctx.actionsAt("xs =", 1, &six)); len(got) != 0 {
		t.Errorf("release 6: expected no action, got %+v", got)
	}
	if got := diamondActions(ctx.actionsAt("xs =", 1, &seven)); len(got) != 1 {
		t.Errorf("release 7: expected 1 action, got %+v", got)
	}
}

// --- convert a string accumulation to StringBuilder --------------------------

func sbActions(actions []CodeActionResult) []CodeActionResult {
	var out []CodeActionResult
	for _, a := range actions {
		if a.Title == "Convert to StringBuilder" {
			out = append(out, a)
		}
	}
	return out
}

func TestStringBuilderConvertsLoopAccumulation(t *testing.T) {
	text := "class T {\n  String m(java.util.List<String> xs) {\n    String s = \"\";\n    for (String x : xs) {\n      s += x;\n    }\n    return s;\n  }\n}"
	ctx := actionsSetup(text, nil)
	actions := sbActions(ctx.actionsAt("s =", 1))
	if len(actions) != 1 {
		t.Fatalf("actions = %+v", actions)
	}
	expectEdit(t, text, actions[0], "class T {\n  String m(java.util.List<String> xs) {\n    StringBuilder s = new StringBuilder();\n    for (String x : xs) {\n      s.append(x);\n    }\n    return s.toString();\n  }\n}")
}

func TestStringBuilderNotOfferedWithoutLoop(t *testing.T) {
	text := "class T {\n  String m() {\n    String s = \"\";\n    s += \"a\";\n    return s;\n  }\n}"
	ctx := actionsSetup(text, nil)
	if got := sbActions(ctx.actionsAt("s =", 1)); len(got) != 0 {
		t.Errorf("expected no action, got %+v", got)
	}
}

func TestStringBuilderNotOfferedWhenReset(t *testing.T) {
	text := "class T {\n  String m(java.util.List<String> xs) {\n    String s = \"\";\n    for (String x : xs) s += x;\n    s = \"reset\";\n    return s;\n  }\n}"
	ctx := actionsSetup(text, nil)
	if got := sbActions(ctx.actionsAt("s =", 1)); len(got) != 0 {
		t.Errorf("expected no action, got %+v", got)
	}
}

func TestStringBuilderNotOfferedWhenIdentityCompared(t *testing.T) {
	text := "class T {\n  String m(java.util.List<String> xs) {\n    String s = \"\";\n    for (String x : xs) s += x;\n    if (s == null) return \"\";\n    return s;\n  }\n}"
	ctx := actionsSetup(text, nil)
	if got := sbActions(ctx.actionsAt("s =", 1)); len(got) != 0 {
		t.Errorf("expected no action, got %+v", got)
	}
}

func TestStringBuilderNotOfferedForNonEmptyInit(t *testing.T) {
	text := "class T {\n  String m(java.util.List<String> xs) {\n    String s = \"x\";\n    for (String x : xs) s += x;\n    return s;\n  }\n}"
	ctx := actionsSetup(text, nil)
	if got := sbActions(ctx.actionsAt("s =", 1)); len(got) != 0 {
		t.Errorf("expected no action, got %+v", got)
	}
}

func TestStringBuilderNotOfferedWhenAppendReadsVariable(t *testing.T) {
	text := "class T {\n  String m(java.util.List<String> xs) {\n    String s = \"\";\n    for (String x : xs) s += s;\n    return s;\n  }\n}"
	ctx := actionsSetup(text, nil)
	if got := sbActions(ctx.actionsAt("s =", 1)); len(got) != 0 {
		t.Errorf("expected no action, got %+v", got)
	}
}
