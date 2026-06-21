package compiler

// Port of src/compiler/jdkImage.test.ts. Reads real classes out of a JDK's
// jmods/; the whole file skips when no JDK with jmods/ is found (so CI without a
// JDK still passes).

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- hermetic JDK image: synthesize a .jmod from emitted classes, no JDK -------

// emitClasses compiles Java to .class bytes with our own emitter (no JDK).
func emitClasses(t *testing.T, name, source string) []EmittedClass {
	t.Helper()
	program := NewProgram()
	LoadJdkStub(program)
	uri := URI("file:///" + name + ".java")
	program.SetOpenDocument(uri, source, 1)
	checker := NewChecker(program)
	return EmitSourceFile(program.GetSourceFile(uri), program, checker, false)
}

// makeJmod is the 4-byte magic "JM\x01\x00" followed by a zip whose class entries
// live under classes/ - exactly what jdk_image strips and reads.
func makeJmod(classes []EmittedClass) []byte {
	entries := make([]ZipEntryInput, 0, len(classes))
	for _, c := range classes {
		entries = append(entries, ZipEntryInput{Name: "classes/" + c.Name + ".class", Bytes: c.Bytes})
	}
	return append([]byte{0x4A, 0x4D, 0x01, 0x00}, WriteZip(entries)...)
}

// makeJdkHome writes a throwaway JDK home <tmp>/jmods/<each file>.
func makeJdkHome(t *testing.T, jmods map[string][]byte) string {
	t.Helper()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "jmods"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, bytes := range jmods {
		if err := os.WriteFile(filepath.Join(home, "jmods", name), bytes, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// makeJmodEntry builds one synthetic jmod holding a single emitted class.
func makeJmodEntry(t *testing.T, binaryName, source string) (string, []byte) {
	t.Helper()
	simple := binaryName[strings.LastIndexByte(binaryName, '/')+1:]
	return simple + ".jmod", makeJmod(emitClasses(t, simple, source))
}

// findJdkHomeWithJmods returns a JDK home containing jmods/, best-effort:
// JAVA_HOME, then javac resolved through PATH. Empty when none is found.
func findJdkHomeWithJmods() string {
	var candidates []string
	if h := os.Getenv("JAVA_HOME"); h != "" {
		candidates = append(candidates, h)
	}
	if javac, err := exec.LookPath("javac"); err == nil {
		if real, err := filepath.EvalSymlinks(javac); err == nil {
			candidates = append(candidates, filepath.Dir(filepath.Dir(real)))
		}
	}
	for _, home := range candidates {
		if _, err := os.Stat(filepath.Join(home, "jmods")); err == nil {
			return home
		}
	}
	return ""
}

func TestJdkImageReadsRealClass(t *testing.T) {
	home := findJdkHomeWithJmods()
	if home == "" {
		t.Skip("no JDK with jmods/ on this machine")
	}
	image := NewJdkImage(home)
	if image == nil {
		t.Fatal("NewJdkImage returned nil for a JDK with jmods/")
	}

	family := image.ReadClassFamily("java/util/List")
	if family == nil {
		t.Fatal("java/util/List not found in image")
	}
	stub, ok := ClassFilesToStub(family)
	if !ok {
		t.Fatal("ClassFilesToStub failed for java/util/List")
	}
	if stub.Name != "java/util/List" {
		t.Errorf("stub name = %q, want java/util/List", stub.Name)
	}
	if !strings.Contains(stub.Source, "package java.util;") || !strings.Contains(stub.Source, "interface List") {
		t.Errorf("unexpected stub source:\n%s", stub.Source)
	}

	// Map has a nested type (Map.Entry) in a sibling .class; the family folds it in.
	mapFamily := image.ReadClassFamily("java/util/Map")
	if len(mapFamily) <= 1 {
		t.Errorf("java/util/Map family = %d classes, want > 1 (nested Entry)", len(mapFamily))
	}
	mapStub, _ := ClassFilesToStub(mapFamily)
	if !strings.Contains(mapStub.Source, "Entry") {
		t.Error("Map stub should contain nested Entry")
	}

	// A type that does not exist is nil (no crash, no false positive).
	if image.ReadClassFamily("java/util/NotARealType") != nil {
		t.Error("a missing type should read back as nil")
	}
}

func TestConsumerResolvesStubOmittedJdkTypes(t *testing.T) {
	home := findJdkHomeWithJmods()
	if home == "" {
		t.Skip("no JDK with jmods/ on this machine")
	}
	image := NewJdkImage(home)
	program := NewProgram()
	program.SetJdkTypeResolver(createJdkTypeResolver(image))
	index := program.GetGlobalIndex()

	// Streams and java.time are absent from jdkstub.go but present in the image.
	for _, fqn := range []Fqn{
		"java.util.stream.Stream", "java.time.LocalDate", "java.util.List", "java.lang.String",
	} {
		if index.GetType(fqn) == nil {
			t.Errorf("GetType(%q) = nil, want resolved from the image", fqn)
		}
	}

	// End to end: a source referencing a stub-omitted type type-checks with no
	// unresolved-type diagnostic.
	program.SetOpenDocument("file:///App.java",
		"import java.time.LocalDate;\nclass App { LocalDate today() { return null; } }", 1)
	checker := NewChecker(program)
	for _, d := range checker.GetSemanticDiagnostics(program.GetSourceFile("file:///App.java")) {
		if strings.Contains(strings.ToLower(d.MessageText), "resolve") ||
			strings.Contains(strings.ToLower(d.MessageText), "cannot find") {
			t.Errorf("unexpected unresolved diagnostic: %s", d.MessageText)
		}
	}
}

// --- hermetic edge cases (synthetic jmods, no JDK needed) ----------------------

func TestNewJdkImageNilWhenNothingToRead(t *testing.T) {
	// No jmods/ directory at all.
	if NewJdkImage(t.TempDir()) != nil {
		t.Error("a home without jmods/ should give a nil image")
	}
	// A jmods/ directory with no .jmod files in it.
	if NewJdkImage(makeJdkHome(t, nil)) != nil {
		t.Error("an empty jmods/ should give a nil image")
	}
}

func TestNonJmodFileIsSkipped(t *testing.T) {
	// The file ends in .jmod (so NewJdkImage sees a candidate) but lacks the JM
	// magic, so reading it yields nothing rather than panicking.
	home := makeJdkHome(t, map[string][]byte{"junk.jmod": {1, 2, 3, 4, 5}})
	image := NewJdkImage(home)
	if image == nil {
		t.Fatal("a candidate .jmod file should still yield an image")
	}
	if image.ReadClassFamily("lib/Whatever") != nil {
		t.Error("a junk jmod should read back as nil")
	}
}

func TestReadClassFamilyResolvesMissesAndDefaultPackage(t *testing.T) {
	wName, wBytes := makeJmodEntry(t, "lib/Widget", "package lib;\npublic class Widget { public int size() { return 0; } }")
	rName, rBytes := makeJmodEntry(t, "Root", "public class Root { public int v() { return 0; } }")
	image := NewJdkImage(makeJdkHome(t, map[string][]byte{wName: wBytes, rName: rBytes}))

	widget := image.ReadClassFamily("lib/Widget")
	if widget == nil {
		t.Fatal("lib/Widget should resolve")
	}
	if stub, ok := ClassFilesToStub(widget); !ok || stub.Name != "lib/Widget" {
		t.Errorf("stub = %q (ok=%v), want lib/Widget", stub.Name, ok)
	}
	// Default-package class (no slash in the binary name).
	if stub, ok := ClassFilesToStub(image.ReadClassFamily("Root")); !ok || stub.Name != "Root" {
		t.Errorf("default-package stub = %q (ok=%v), want Root", stub.Name, ok)
	}
	// A class that is not present.
	if image.ReadClassFamily("lib/Missing") != nil {
		t.Error("a missing class should read back as nil")
	}
}

func TestNestedClassFoldsIntoOuterFamily(t *testing.T) {
	classes := emitClasses(t, "Outer", strings.Join([]string{
		"package lib;",
		"public class Outer {",
		"  public static class Builder { public int knobs; }",
		"}",
	}, "\n"))
	if len(classes) != 2 {
		t.Fatalf("emitter produced %d classes, want 2 (outer + nested)", len(classes))
	}
	image := NewJdkImage(makeJdkHome(t, map[string][]byte{"lib.jmod": makeJmod(classes)}))
	family := image.ReadClassFamily("lib/Outer")
	if len(family) != 2 {
		t.Fatalf("family = %d classes, want 2", len(family))
	}
	stub, _ := ClassFilesToStub(family)
	if !strings.Contains(stub.Source, "class Builder") {
		t.Error("outer stub should fold in the nested Builder")
	}
}

func TestProjectTypeShadowsJdkType(t *testing.T) {
	// The image carries lib.Thing with imageOnly(); the project declares its own
	// lib.Thing with projectOnly(). GetType must return the project's.
	name, bytes := makeJmodEntry(t, "lib/Thing", "package lib;\npublic class Thing { public int imageOnly() { return 0; } }")
	image := NewJdkImage(makeJdkHome(t, map[string][]byte{name: bytes}))
	program := NewProgram()
	program.SetJdkTypeResolver(createJdkTypeResolver(image))
	program.AddProjectFile("file:///lib/Thing.java",
		"package lib;\npublic class Thing { public int projectOnly() { return 1; } }")
	thing := program.GetGlobalIndex().GetType("lib.Thing")
	if thing == nil || thing.Members["projectOnly"] == nil {
		t.Fatal("GetType should return the project's lib.Thing")
	}
	if thing.Members["imageOnly"] != nil {
		t.Error("the image's lib.Thing must not shadow the project's")
	}
}

func TestJdkTypeMissIsIdempotent(t *testing.T) {
	name, bytes := makeJmodEntry(t, "lib/Only", "package lib;\npublic class Only {}")
	resolve := createJdkTypeResolver(NewJdkImage(makeJdkHome(t, map[string][]byte{name: bytes})))
	if resolve("lib.Absent") != nil {
		t.Error("absent type should be nil")
	}
	if resolve("lib.Absent") != nil {
		t.Error("absent type should stay nil on the cached second call")
	}
	if resolve("lib.Only") == nil {
		t.Error("present type should resolve")
	}
}

func TestInstallJdkTypesFallsBackToStub(t *testing.T) {
	// No config (the LSP can run without one): the stub must still resolve.
	program := NewProgram()
	InstallJdkTypes(program, nil)
	if program.GetGlobalIndex().GetType("java.lang.String") == nil {
		t.Error("with no JDK, java.lang.String should resolve from the stub")
	}
}
