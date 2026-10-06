package compiler

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/baselines"
)

// Two class-file attributes that `javap -c -p` does not print, so the
// byte-match tier in emitter_test.go cannot see them: InnerClasses (JVMS 4.7.6)
// and LocalVariableTable (JVMS 4.7.13). Each fixture's normalized section is
// pinned against javac's, stored under test-fixtures/emitter. Port of the two
// tests at the end of src/compiler/emitter.test.ts, whose baselines were left
// behind unread when that file was deleted.
//
// At test time only `javap` runs, over our own output; the javac reference is
// read from disk. UPDATE_BASELINES=1 regenerates it from a live javac (and a
// missing baseline is regenerated too, as everywhere else).

var (
	innerClassesBaselineFile = filepath.Join(
		"..", "..", "..", "test-fixtures", "emitter", "innerclasses-baselines.json")
	lvtBaselineFile = filepath.Join(
		"..", "..", "..", "test-fixtures", "emitter", "localvariabletable-baselines.json")
)

// attrFixture is one source file plus the name it is compiled under. The order
// of the slice is the key order of the committed JSON, which is written in
// fixture order (not sorted), so it has to be preserved to regenerate it.
type attrFixture struct {
	name   string
	source string
}

var innerClassFixtures = []attrFixture{
	// Every nested form in one file: static/inner/interface/enum/record members,
	// a member-of-member (Deep), an anonymous class and a local class.
	{"IcAll", `class IcAll {
  static class S { int v; }
  class Inner { void g(){ new Deep(); } class Deep {} }
  interface I { void h(); }
  enum E { A, B }
  record R(int x) {}
  void m() {
    new S();
    Object o = new Object() { public String toString(){ return "x"; } };
    class Local {}
    new Local();
  }
}`},
	// Breadth-first member ordering: a member-of-member (Y) is listed after all of
	// the direct members, and the lambda/concat pull in MethodHandles$Lookup.
	{"IcMany", `class IcMany {
  interface A {} interface B {} interface C {}
  static class X { static class Y {} }
  Runnable r = () -> {};
  String c(int n){ return "v=" + n; }
  void m(){ Object o1 = new Object(){}; Object o2 = new Object(){}; new X(); }
}`},
	// Enclosing-first when a class references another branch's nested class: B's
	// own entry follows A and A$Deep, which its body references.
	{"IcCross", `class IcCross {
  static class A { static class Deep {} }
  static class B { Object x = new A.Deep(); }
  void m(){ new B(); }
}`},
	// Types nested in an interface are implicitly public + static (JLS 9.5).
	{"IcIface", `interface IcIface {
  class Impl implements IcIface { }
  static class K {}
}`},
	// Enum constant bodies become anonymous-style Outer$N subclasses (ACC_FINAL in
	// InnerClasses); only the constants with a body are numbered.
	{"IcEnumBody", `enum IcEnumBody {
  A { int v(){ return 1; } },
  B,
  C { int v(){ return 3; } };
  int v(){ return 0; }
}`},
}

var lvtFixtures = []attrFixture{
	// Blocks, a for-loop, slot reuse (y and i share slot 4) and out-of-scope
	// ordering (inner scopes close first; ties by slot).
	{"LvtScopes", `class LvtScopes {
  static int f(int a, long b) {
    int x = a + 1;
    { int y = x * 2; x = y; }
    for (int i = 0; i < a; i++) { int z = i; x += z; }
    return x;
  }
}`},
	// `this` plus parameters span the whole method; a local enters scope after its
	// store.
	{"LvtParams", `class LvtParams {
  int add(int a, int b) { int s = a + b; return s; }
  static long pick(long p, long q) { long r = p; return r; }
}`},
}

var poolIndexRe = regexp.MustCompile(`#\d+=?`)

// innerClassesSection is one class's InnerClasses section, pool indices
// stripped, entries kept in order: "<flags> <inner=...of...>" - stable across
// compilers, like the disassembly baselines.
func innerClassesSection(t *testing.T, dir, className string) []string {
	t.Helper()
	out, err := exec.Command("javap", "-v", "-p", "-cp", dir, className).Output()
	if err != nil {
		t.Fatalf("javap -v %s: %v", className, err)
	}
	lines := strings.Split(string(out), "\n")
	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "InnerClasses:" {
			start = i
			break
		}
	}
	result := []string{}
	if start < 0 {
		return result
	}
	for _, line := range lines[start+1:] {
		at := strings.Index(line, "//")
		if at < 0 {
			break // the section ends at the first non-entry line
		}
		flags := poolIndexRe.ReplaceAllString(line[:at], "")
		flags = strings.TrimSuffix(strings.TrimRight(flags, " \t"), "of")
		if semi := strings.Index(flags, ";"); semi >= 0 {
			flags = flags[:semi]
		}
		flags = strings.Join(strings.Fields(flags), " ")
		comment := strings.TrimSpace(line[at+2:])
		if flags != "" {
			result = append(result, flags+" "+comment)
		} else {
			result = append(result, comment)
		}
	}
	return result
}

// localVarTable is one class's LocalVariableTable rows, whitespace collapsed,
// in javap's order.
func localVarTable(t *testing.T, dir, className string) []string {
	t.Helper()
	out, err := exec.Command("javap", "-c", "-p", "-l", "-cp", dir, className).Output()
	if err != nil {
		t.Fatalf("javap -l %s: %v", className, err)
	}
	rows := []string{}
	inTable := false
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "LocalVariableTable:" {
			inTable = true
			continue
		}
		if !inTable {
			continue
		}
		if strings.HasPrefix(trimmed, "Start") {
			continue
		}
		if trimmed == "" {
			inTable = false
			continue
		}
		if trimmed[0] >= '0' && trimmed[0] <= '9' {
			rows = append(rows, strings.Join(strings.Fields(trimmed), " "))
		} else {
			inTable = false // a non-row line ends this method's table
		}
	}
	return rows
}

// sectionsOfDir reads one attribute section from every named class in dir,
// keyed by binary class name (sorted, as the committed JSON is).
func sectionsOfDir(t *testing.T, dir string, classNames []string, read func(*testing.T, string, string) []string) map[string][]string {
	t.Helper()
	sort.Strings(classNames)
	out := map[string][]string{}
	for _, name := range classNames {
		out[name] = read(t, dir, name)
	}
	return out
}

// javacSections compiles every fixture with javac and reads the attribute
// section out of each class it produced. extraArgs is "-g" for the
// LocalVariableTable, which javac only emits under it.
func javacSections(t *testing.T, fixtures []attrFixture, extraArgs []string, read func(*testing.T, string, string) []string) map[string]map[string][]string {
	t.Helper()
	out := map[string]map[string][]string{}
	for _, fixture := range fixtures {
		dir := t.TempDir()
		javaFile := filepath.Join(dir, fixture.name+".java")
		if err := os.WriteFile(javaFile, []byte(fixture.source), 0o644); err != nil {
			t.Fatalf("write %s: %v", javaFile, err)
		}
		args := append(append([]string{}, extraArgs...), "--release", "21", "-d", dir, javaFile)
		if cmd := exec.Command("javac", args...); cmd.Run() != nil {
			t.Fatalf("javac %s failed", fixture.name)
		}
		out[fixture.name] = sectionsOfDir(t, dir, classNamesIn(t, dir), read)
	}
	return out
}

func classNamesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".class") {
			names = append(names, strings.TrimSuffix(e.Name(), ".class"))
		}
	}
	return names
}

// marshalAttrBaseline reproduces the committed files byte for byte:
// JSON.stringify(x, null, 2) + "\n", with the fixtures in declaration order
// (encoding/json would sort them) and each fixture's classes sorted.
func marshalAttrBaseline(t *testing.T, fixtures []attrFixture, sections map[string]map[string][]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.WriteString("{\n")
	for i, fixture := range fixtures {
		var inner bytes.Buffer
		enc := json.NewEncoder(&inner)
		enc.SetEscapeHTML(false)
		enc.SetIndent("  ", "  ")
		if err := enc.Encode(sections[fixture.name]); err != nil {
			t.Fatalf("marshal %s: %v", fixture.name, err)
		}
		buf.WriteString(`  "` + fixture.name + `": `)
		buf.WriteString(strings.TrimRight(inner.String(), "\n"))
		if i < len(fixtures)-1 {
			buf.WriteString(",")
		}
		buf.WriteString("\n")
	}
	buf.WriteString("}\n")
	return buf.Bytes()
}

// loadAttrBaseline reads the committed reference, regenerating it from javac
// first when UPDATE_BASELINES=1 or the file is missing. It returns nil when
// there is neither a baseline nor a javac to build one with.
func loadAttrBaseline(t *testing.T, path string, fixtures []attrFixture, extraArgs []string, read func(*testing.T, string, string) []string) map[string]map[string][]string {
	t.Helper()
	_, missing := os.Stat(path)
	if baselines.Update || missing != nil {
		if !hasTool("javac") {
			if missing != nil {
				t.Skip("no baseline and no javac to generate one")
			}
			t.Log("UPDATE_BASELINES=1 but no javac: asserting the committed baseline")
		} else {
			content := marshalAttrBaseline(t, fixtures, javacSections(t, fixtures, extraArgs, read))
			if _, err := baselines.Write(path, content); err != nil {
				t.Fatalf("write baseline %s: %v", path, err)
			}
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read baseline %s: %v", path, err)
	}
	var out map[string]map[string][]string
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return out
}

// emitFixtureToDir emits a fixture with our own backend and returns the temp
// dir it wrote the classes to, plus their names.
func emitFixtureToDir(t *testing.T, fixture attrFixture, debugInfo bool) (string, []string) {
	t.Helper()
	program := NewProgram()
	LoadJdkStub(program)
	uri := URI("file:///" + fixture.name + ".java")
	program.SetOpenDocument(uri, fixture.source, 1)
	checker := NewChecker(program)
	dir := t.TempDir()
	var names []string
	for _, cls := range EmitSourceFile(program.GetSourceFile(uri), program, checker, debugInfo) {
		at := filepath.Join(dir, cls.Name+".class")
		if err := os.WriteFile(at, cls.Bytes, 0o644); err != nil {
			t.Fatalf("write %s: %v", cls.Name, err)
		}
		names = append(names, cls.Name)
	}
	return dir, names
}

func assertAttrSections(t *testing.T, fixtures []attrFixture, baseline map[string]map[string][]string, debugInfo bool, read func(*testing.T, string, string) []string) {
	t.Helper()
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			dir, names := emitFixtureToDir(t, fixture, debugInfo)
			got := sectionsOfDir(t, dir, names, read)
			want := baseline[fixture.name]
			for name, wantRows := range want {
				gotRows, ok := got[name]
				if !ok {
					t.Errorf("%s: not emitted", name)
					continue
				}
				if strings.Join(gotRows, "\n") != strings.Join(wantRows, "\n") {
					t.Errorf("%s:\n--- got ---\n%s\n--- want (javac) ---\n%s",
						name, strings.Join(gotRows, "\n"), strings.Join(wantRows, "\n"))
				}
			}
			for name := range got {
				if _, ok := want[name]; !ok {
					t.Errorf("%s: emitted, but javac produced no such class", name)
				}
			}
		})
	}
}

// TestInnerClassesMatchJavac pins the InnerClasses attribute's exact contents
// AND order against javac.
func TestInnerClassesMatchJavac(t *testing.T) {
	if !hasTool("javap") {
		t.Skip("no JDK (javap)")
	}
	baseline := loadAttrBaseline(t, innerClassesBaselineFile, innerClassFixtures, nil, innerClassesSection)
	assertAttrSections(t, innerClassFixtures, baseline, false, innerClassesSection)
}

// TestLocalVariableTableMatchesJavacG pins the LocalVariableTable against
// `javac -g`. It is debug info, so the emitter omits it by default (keeping
// byte-equivalence with default-flags javac) and emits it when debugInfo is on;
// these fixtures avoid constructs whose codegen diverges from javac, so the
// comparison is meaningful.
func TestLocalVariableTableMatchesJavacG(t *testing.T) {
	if !hasTool("javap") {
		t.Skip("no JDK (javap)")
	}
	baseline := loadAttrBaseline(t, lvtBaselineFile, lvtFixtures, []string{"-g"}, localVarTable)
	assertAttrSections(t, lvtFixtures, baseline, true, localVarTable)
}
