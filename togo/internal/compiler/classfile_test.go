package compiler

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Port of src/compiler/classfile.test.ts.

var emitBaselinesDir = filepath.Join("..", "..", "..", "test-fixtures", "emitter", "emit-baselines")

// emitClassBytes emits with OUR compiler and returns the named class' bytes -
// a self-contained roundtrip with no JDK needed.
func emitClassBytes(t *testing.T, name, source string) []byte {
	t.Helper()
	program := NewProgram()
	LoadJdkStub(program)
	uri := URI("file:///" + name + ".java")
	program.SetOpenDocument(uri, source, 1)
	checker := NewChecker(program)
	classes := EmitSourceFile(program.GetSourceFile(uri), program, checker, true)
	for _, c := range classes {
		if len(c.Name) >= len(name) && c.Name[len(c.Name)-len(name):] == name {
			return c.Bytes
		}
	}
	t.Fatalf("class %s was not emitted", name)
	return nil
}

func TestReadClassFileHeaderAndMembers(t *testing.T) {
	bytes := emitClassBytes(t, "Greeter",
		"package lib; public class Greeter implements java.lang.Runnable { int factor; static long total;"+
			" public Greeter(int factor) { this.factor = factor; } public void run() {} }")
	classFile, err := ReadClassFile(bytes)
	if err != nil {
		t.Fatalf("ReadClassFile: %v", err)
	}
	if classFile.Major != 65 { // Java 21
		t.Errorf("major = %d, want 65", classFile.Major)
	}
	if classFile.ThisClass != "lib/Greeter" || classFile.SuperClass != "java/lang/Object" {
		t.Errorf("this/super = %q/%q", classFile.ThisClass, classFile.SuperClass)
	}
	if len(classFile.Interfaces) != 1 || classFile.Interfaces[0] != "java/lang/Runnable" {
		t.Errorf("interfaces = %v", classFile.Interfaces)
	}
	fields := []string{}
	for _, f := range classFile.Fields {
		fields = append(fields, f.Name+":"+f.Descriptor)
	}
	if len(fields) != 2 || fields[0] != "factor:I" || fields[1] != "total:J" {
		t.Errorf("fields = %v", fields)
	}
	methods := []string{}
	for _, m := range classFile.Methods {
		methods = append(methods, m.Name+m.Descriptor)
	}
	if len(methods) != 2 || methods[0] != "<init>(I)V" || methods[1] != "run()V" {
		t.Errorf("methods = %v", methods)
	}
	if got := SourceFileName(classFile); got != "Greeter.java" {
		t.Errorf("SourceFileName = %q", got)
	}
}

func TestReadCodeWithExceptionTable(t *testing.T) {
	bytes := emitClassBytes(t, "Guarded",
		"public class Guarded { int f(int n) { try { return 1 / n; } catch (ArithmeticException e) { return -1; } } }")
	classFile, err := ReadClassFile(bytes)
	if err != nil {
		t.Fatalf("ReadClassFile: %v", err)
	}
	var method Member
	for _, m := range classFile.Methods {
		if m.Name == "f" {
			method = m
		}
	}
	code, err := ReadCode(method, classFile.Pool)
	if err != nil || code == nil {
		t.Fatalf("ReadCode: %v", err)
	}
	if code.MaxStack == 0 || code.MaxLocals < 2 || len(code.Code) == 0 {
		t.Errorf("code = %+v", *code)
	}
	if len(code.Exceptions) != 1 || code.Exceptions[0].CatchType != "java/lang/ArithmeticException" {
		t.Errorf("exceptions = %+v", code.Exceptions)
	}
	if _, ok := FindAttribute(code.Attributes, "LineNumberTable"); !ok {
		t.Error("missing LineNumberTable")
	}
}

func TestPoolMemberRef(t *testing.T) {
	bytes := emitClassBytes(t, "Caller", `public class Caller { int len() { return "hi".length(); } }`)
	classFile, err := ReadClassFile(bytes)
	if err != nil {
		t.Fatalf("ReadClassFile: %v", err)
	}
	found := false
	for index, entry := range classFile.Pool {
		if entry == nil || entry.Tag != TagMethodref {
			continue
		}
		ref, ok := PoolMemberRef(classFile.Pool, uint16(index))
		if ok && ref.Owner+"."+ref.Name+ref.Descriptor == "java/lang/String.length()I" {
			found = true
		}
	}
	if !found {
		t.Error("java/lang/String.length()I not resolved from the constant pool")
	}
}

func TestSignatureAndThrows(t *testing.T) {
	bytes := emitClassBytes(t, "Boxy",
		"public class Boxy<T extends java.lang.CharSequence> {"+
			" T value; public T get() throws java.io.IOException { return value; } }")
	classFile, err := ReadClassFile(bytes)
	if err != nil {
		t.Fatalf("ReadClassFile: %v", err)
	}
	want := "<T::Ljava/lang/CharSequence;>Ljava/lang/Object;"
	if got := SignatureOf(classFile.Attributes, classFile.Pool); got != want {
		t.Errorf("class signature = %q, want %q", got, want)
	}
	// cappu's own emitter does not write the Exceptions attribute (javac does);
	// reading a real one is covered by the javac tier in disasm_test.go.
	var get Member
	for _, m := range classFile.Methods {
		if m.Name == "get" {
			get = m
		}
	}
	if thrown := ReadThrownExceptions(get, classFile.Pool); len(thrown) != 0 {
		t.Errorf("thrown = %v", thrown)
	}
}

func TestReadBootstrapMethods(t *testing.T) {
	bytes := emitClassBytes(t, "Joiner",
		"public class Joiner { String j(String a, int b) { return a + b; } }")
	classFile, err := ReadClassFile(bytes)
	if err != nil {
		t.Fatalf("ReadClassFile: %v", err)
	}
	bootstraps := ReadBootstrapMethods(classFile)
	if len(bootstraps) == 0 {
		t.Fatal("no bootstrap methods")
	}
	if entry := PoolAt(classFile.Pool, bootstraps[0].HandleIndex); entry == nil || entry.Tag != TagMethodHandle {
		t.Errorf("bootstrap handle = %+v", entry)
	}
}

func TestDecodeModifiedUTF8Constants(t *testing.T) {
	// In the constant pool a NUL is encoded as c0 80 and a supplementary
	// character as a surrogate pair of three-byte sequences.
	bytes := emitClassBytes(t, "Texts",
		`public class Texts { String s() { return "a\u0000b😀"; } }`)
	classFile, err := ReadClassFile(bytes)
	if err != nil {
		t.Fatalf("ReadClassFile: %v", err)
	}
	want := "a\x00b\U0001F600"
	found := false
	for _, entry := range classFile.Pool {
		if entry != nil && entry.Tag == TagString && PoolUtf8(classFile.Pool, entry.Index) == want {
			found = true
		}
	}
	if !found {
		t.Errorf("decoded string %q not found in the constant pool", want)
	}
}

func TestReadClassFileRejectsOtherInput(t *testing.T) {
	if _, err := ReadClassFile([]byte{1, 2, 3, 4}); !errors.Is(err, ErrNotClassFile) {
		t.Errorf("err = %v, want ErrNotClassFile", err)
	}
	if _, err := ReadClassFile([]byte{0xca, 0xfe, 0xba, 0xbe}); err == nil {
		t.Error("a truncated class file was accepted")
	}
}

func TestReadCommittedBaselines(t *testing.T) {
	for _, name := range []string{"Arithmetic", "EnumMixed", "AnnAll", "Concat", "QualifiedAnon$Inner"} {
		b, err := os.ReadFile(filepath.Join(emitBaselinesDir, name+".class"))
		if err != nil {
			t.Fatalf("read baseline: %v", err)
		}
		classFile, err := ReadClassFile(b)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if classFile.ThisClass != name {
			t.Errorf("thisClass = %q, want %q", classFile.ThisClass, name)
		}
		for _, method := range classFile.Methods {
			if _, err := ReadCode(method, classFile.Pool); err != nil {
				t.Errorf("%s.%s: %v", name, method.Name, err)
			}
		}
	}
}
