package compiler

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Port of src/compiler/classfileReader.test.ts. The TS test emits its inputs
// with the (not-yet-ported) bytecode emitter; here the same classes are
// pre-compiled with javac and committed under testdata/classfiles, so the test
// needs no JDK at runtime. Where the TS test re-emits a consumer to prove the
// stub resolves, we instead check the stub registers in the global index and
// the consumer type-checks without diagnostics.

func fixtureBytes(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "classfiles", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func consumerResolves(t *testing.T, stubURI URI, stubSource, consumer string) {
	t.Helper()
	program := NewProgram()
	LoadJdkStub(program)
	program.AddProjectFile(stubURI, stubSource)
	program.SetOpenDocument("file:///App.java", consumer, 1)
	checker := NewChecker(program)
	if diags := checker.GetSemanticDiagnostics(program.GetSourceFile("file:///App.java")); len(diags) != 0 {
		t.Errorf("consumer should type-check cleanly, got %d diagnostics: %+v", len(diags), diags)
	}
}

func TestClassFileReadsBackAsResolvableStub(t *testing.T) {
	stub, ok := ClassFileToStub(fixtureBytes(t, "Greeter.class"))
	if !ok {
		t.Fatal("Greeter should produce a stub")
	}
	if stub.Name != "lib/Greeter" {
		t.Errorf("stub.Name = %q, want lib/Greeter", stub.Name)
	}
	for _, want := range []string{
		"package lib;",
		"public class Greeter",
		"public int factor;",
		"public Greeter(int p0)",
		"static java.lang.String greet(java.lang.String p0)",
		"public int scale(int p0)",
	} {
		if !strings.Contains(stub.Source, want) {
			t.Errorf("stub source missing %q:\n%s", want, stub.Source)
		}
	}
	if strings.Contains(stub.Source, "hidden") {
		t.Errorf("private members should be omitted:\n%s", stub.Source)
	}
	consumerResolves(t, "classpath:///lib/Greeter.java", stub.Source,
		"import lib.Greeter;\nclass App { String m() { return Greeter.greet(\"x\"); } }")
}

func TestClassFileInterfacesAndEnums(t *testing.T) {
	iface, ok := ClassFileToStub(fixtureBytes(t, "Speaker.class"))
	if !ok {
		t.Fatal("Speaker should produce a stub")
	}
	for _, want := range []string{
		"public interface Speaker",
		"java.lang.String speak();", // abstract: no body
		"default int volume()",      // default keeps a body
	} {
		if !strings.Contains(iface.Source, want) {
			t.Errorf("Speaker stub missing %q:\n%s", want, iface.Source)
		}
	}

	e, ok := ClassFileToStub(fixtureBytes(t, "Color.class"))
	if !ok {
		t.Fatal("Color should produce a stub")
	}
	for _, want := range []string{"public enum Color", "RED, GREEN, BLUE;"} {
		if !strings.Contains(e.Source, want) {
			t.Errorf("Color stub missing %q:\n%s", want, e.Source)
		}
	}
	if strings.Contains(e.Source, "valueOf") {
		t.Errorf("valueOf collides with synthesized statics:\n%s", e.Source)
	}
}

func TestClassFileNestedClassesSkipped(t *testing.T) {
	if _, ok := ClassFileToStub(fixtureBytes(t, "OuterIn.class")); ok {
		t.Error("a nested class is not expressible as a top-level stub")
	}
}

func TestClassFileJarClasspathEntry(t *testing.T) {
	program := NewProgram()
	LoadJdkStub(program)
	loaded := LoadClassPath(program, []string{filepath.Join("testdata", "classfiles", "util.jar")})
	if loaded != 1 {
		t.Errorf("loaded %d types, want 1", loaded)
	}
	if program.GetGlobalIndex().GetType("lib.Util") == nil {
		t.Error("lib.Util should resolve after loading the jar")
	}
}

func TestClassFileGenericSignaturesSurvive(t *testing.T) {
	stub, ok := ClassFileToStub(fixtureBytes(t, "Box.class"))
	if !ok {
		t.Fatal("Box should produce a stub")
	}
	for _, want := range []string{
		"class Box<T extends java.lang.CharSequence>",
		"implements java.lang.Comparable<lib.Box<T>>",
		"public T value;",
		"public T get()",
		"<U extends java.lang.Comparable<U>> U pick(U p0, java.util.List<? extends U> p1)",
	} {
		if !strings.Contains(stub.Source, want) {
			t.Errorf("Box stub missing %q:\n%s", want, stub.Source)
		}
	}
	consumerResolves(t, "classpath:///lib/Box.java", stub.Source,
		"import lib.Box;\nclass App { int m(Box<String> b) { return b.get().length(); } }")
}

func TestClassFileNestedGroupIntoOuterStub(t *testing.T) {
	program := NewProgram()
	LoadJdkStub(program)
	dir := filepath.Join("testdata", "classfiles", "nested")
	if loaded := LoadClassPath(program, []string{dir}); loaded != 1 {
		t.Errorf("loaded %d top-level stubs, want 1", loaded)
	}
	sf := program.GetSourceFile("classpath:///lib/Outer.java")
	if sf == nil {
		t.Fatal("lib/Outer stub not registered")
	}
	stub := sf.AsSourceFile().Text
	if !strings.Contains(stub, "public static class Builder") {
		t.Errorf("Outer stub missing nested Builder:\n%s", stub)
	}
	if strings.Contains(stub, "$1") {
		t.Errorf("anonymous class should never appear:\n%s", stub)
	}
	program.SetOpenDocument("file:///App.java",
		"import lib.Outer;\nclass App { int m() { return new Outer.Builder().set(3).knobs; } }", 1)
	checker := NewChecker(program)
	if diags := checker.GetSemanticDiagnostics(program.GetSourceFile("file:///App.java")); len(diags) != 0 {
		t.Errorf("consumer should resolve nested Builder, got: %+v", diags)
	}
}

// nikeee/cappu#70-hunt: a hostile or truncated jar entry must never hang the
// reader. Both corruptions below previously looped (the descriptor scan until a
// 2^32 RangeError, the signature scan forever).
func TestClassFileCorruptedDescriptorsTerminate(t *testing.T) {
	original := fixtureBytes(t, "G.class")

	// descriptor "(I)V" -> "(IIV": the ')' the parameter scan looks for is gone
	desc := bytes.Clone(original)
	descAt := bytes.Index(desc, []byte("(I)V"))
	if descAt <= 0 {
		t.Fatalf("(I)V not found in G.class")
	}
	copy(desc[descAt:], []byte("(IIV"))
	if _, ok := ClassFileToStub(desc); !ok {
		t.Error("corrupted descriptor should still stub")
	}

	// class signature -> same-length colon soup with no closing '>'
	sig := bytes.Clone(original)
	sigAt := bytes.Index(sig, []byte("<T::Ljava/lang/Comparable<TT;>;>"))
	if sigAt <= 0 {
		t.Fatalf("class signature not found in G.class")
	}
	copy(sig[sigAt:], []byte("<T:<T:<T:<T:<T:<T:<T:<T:<T:<T:<T"))
	if _, ok := ClassFileToStub(sig); !ok {
		t.Error("corrupted class signature should still stub (and terminate)")
	}

	// method signature "(TT;)TT;" -> truncated T-refs and unclosed type args
	methodSig := bytes.Clone(original)
	methodSigAt := bytes.Index(methodSig, []byte("(TT;)TT;"))
	if methodSigAt <= 0 {
		t.Fatalf("method signature not found in G.class")
	}
	copy(methodSig[methodSigAt:], []byte("(TT;)LC<"))
	if _, ok := ClassFileToStub(methodSig); !ok {
		t.Error("corrupted method signature should still stub (and terminate)")
	}
}
