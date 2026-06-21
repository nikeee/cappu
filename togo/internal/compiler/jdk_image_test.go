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
