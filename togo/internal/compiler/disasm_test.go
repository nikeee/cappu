package compiler

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Port of src/compiler/disasm.test.ts. The fixture directories are shared with
// the TS side, so the two disassemblers are held to the same text.

var (
	javacBaselinesDir  = filepath.Join("..", "..", "..", "test-fixtures", "emitter", "javac-baselines")
	disasmBaselinesDir = filepath.Join("..", "..", "..", "test-fixtures", "decompiler", "disasm-baselines")
)

func disassembleBaseline(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(emitBaselinesDir, name+".class"))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	text, err := Disassemble(b)
	if err != nil {
		t.Fatalf("disassemble %s: %v", name, err)
	}
	return text
}

// --- tier 1: the committed javac disassembly, no JDK needed -------------------------

// Every javac baseline is the normalized `javap -c -p` of the class our emitter
// produced, so our own disassembler has to reproduce it exactly.
func TestDisasmMatchesJavacBaselines(t *testing.T) {
	files, err := os.ReadDir(javacBaselinesDir)
	if err != nil {
		t.Fatalf("read javac baselines: %v", err)
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		t.Run(file.Name(), func(t *testing.T) {
			reference := loadJavacBaseline(t, filepath.Join(javacBaselinesDir, file.Name()))
			for className, expected := range reference {
				ours := ParseJavapText(disassembleBaseline(t, className))[className]
				if ours == nil {
					t.Fatalf("%s: no disassembly", className)
				}
				compareDisasm(t, className, ours, expected)
			}
		})
	}
}

func compareDisasm(t *testing.T, className string, got, want *Disasm) {
	t.Helper()
	if strings.Join(got.Members, "\n") != strings.Join(want.Members, "\n") {
		t.Errorf("%s members:\n--- got ---\n%s\n--- want ---\n%s",
			className, strings.Join(got.Members, "\n"), strings.Join(want.Members, "\n"))
		return
	}
	if len(got.Code) != len(want.Code) {
		t.Errorf("%s: %d methods with code, want %d", className, len(got.Code), len(want.Code))
		return
	}
	for i, method := range got.Code {
		reference := want.Code[i]
		if method.Signature != reference.Signature {
			t.Errorf("%s: method %d is %q, want %q", className, i, method.Signature, reference.Signature)
			continue
		}
		if strings.Join(method.Instructions, "\n") != strings.Join(reference.Instructions, "\n") {
			t.Errorf("%s %s:\n--- got ---\n%s\n--- want ---\n%s", className, method.Signature,
				strings.Join(method.Instructions, "\n"), strings.Join(reference.Instructions, "\n"))
		}
	}
}

// --- tier 2: the text baselines the TS side writes ----------------------------------

// Classes whose full listing is pinned as text: enum bodies, annotations and
// sealed types, where tier 1 covers little or nothing (a javac baseline holds
// only the normalized instruction stream, and some of these have no baseline at
// all). The files are written by src/compiler/disasm.test.ts under
// UPDATE_BASELINES=1, so here they double as the TS/Go parity check.
var textBaselineClasses = []string{
	"AnnAll",
	"EnumAbstract",
	"EnumMixed",
	"EnumMixed$1",
	"EnumUnqualified",
	"ImplicitSealed",
	"QualifiedAnon$1",
	"Sealed",
}

func TestDisasmTextBaselines(t *testing.T) {
	for _, name := range textBaselineClasses {
		t.Run(name, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join(disasmBaselinesDir, name+".txt"))
			if err != nil {
				t.Fatalf("read baseline: %v", err)
			}
			if got := disassembleBaseline(t, name); got != string(want) {
				t.Errorf("%s:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
			}
		})
	}
}

func TestEveryBaselineClassDisassembles(t *testing.T) {
	files, err := os.ReadDir(emitBaselinesDir)
	if err != nil {
		t.Fatalf("read baselines: %v", err)
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".class") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(emitBaselinesDir, file.Name()))
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		text, err := Disassemble(b)
		if err != nil {
			t.Errorf("%s: %v", file.Name(), err)
			continue
		}
		if !strings.HasSuffix(text, "}\n") {
			t.Errorf("%s: unterminated listing", file.Name())
		}
	}
}

// --- tier 3: live javap, over constructs our emitter cannot produce ------------------

// Straight from javac, so these cover switches, exception tables, invokedynamic,
// the wide prefix, `throws` clauses and older class-file versions. Kept in sync
// with JAVAC_FIXTURES in src/compiler/disasm.test.ts.
var javacFixtures = map[string]string{
	"Switches": `public class Switches {
  int table(int x) { switch (x) { case 1: return 10; case 2: return 20; case 3: return 30; default: return 0; } }
  int lookup(int x) { switch (x) { case 1: return 1; case 100: return 2; case 10000: return 3; default: return 4; } }
  int strings(String s) { switch (s) { case "a": return 1; case "b": return 2; default: return 3; } }
}`,
	"Guards": `import java.io.*;
public class Guards {
  String read(String s) throws IOException {
    try { return s.trim(); } catch (RuntimeException e) { return "x"; } finally { System.out.print(""); }
  }
  void nested() { synchronized (this) { try { throw new IllegalStateException(); } catch (Exception e) {} } }
}`,
	"Dynamic": `import java.util.function.*;
public class Dynamic {
  Supplier<String> lambda() { return () -> "hi"; }
  Runnable ref() { return this::run; }
  void run() {}
  String concat(String a, int b) { return a + b + "!"; }
}`,
	"Numbers": `public class Numbers {
  double tiny() { return 4.9E-324; }
  double big() { return 1.5e300; }
  float subnormal() { return 1.4E-45f; }
  float exact() { return 359427.125f; }
  long l() { return 123456789012345L; }
  int[][] multi() { return new int[2][3]; }
  char[] chars() { return new char[5]; }
  String quoted() { return "tab\tnul\u0000 \u00a0 end"; }
}`,
	"Wide": `public class Wide {
  int wide(int n) {
    int a0=n,a1=n,a2=n,a3=n,a4=n,a5=n,a6=n,a7=n,a8=n,a9=n;
    long b0=n,b1=n,b2=n,b3=n,b4=n,b5=n,b6=n,b7=n,b8=n,b9=n;
    long c0=n,c1=n,c2=n,c3=n,c4=n,c5=n,c6=n,c7=n,c8=n,c9=n;
    long d0=n,d1=n,d2=n,d3=n,d4=n,d5=n,d6=n,d7=n,d8=n,d9=n;
    long e0=n,e1=n,e2=n,e3=n,e4=n,e5=n,e6=n,e7=n,e8=n,e9=n;
    long f0=n,f1=n,f2=n,f3=n,f4=n,f5=n,f6=n,f7=n,f8=n,f9=n;
    long g0=n,g1=n,g2=n,g3=n,g4=n,g5=n,g6=n,g7=n,g8=n,g9=n;
    long h0=n,h1=n,h2=n,h3=n,h4=n,h5=n,h6=n,h7=n,h8=n,h9=n;
    long i0=n,i1=n,i2=n,i3=n,i4=n,i5=n,i6=n,i7=n,i8=n,i9=n;
    long j0=n,j1=n,j2=n,j3=n,j4=n,j5=n,j6=n,j7=n,j8=n,j9=n;
    long k0=n,k1=n,k2=n,k3=n,k4=n,k5=n,k6=n,k7=n,k8=n,k9=n;
    long l0=n,l1=n,l2=n,l3=n,l4=n,l5=n,l6=n,l7=n,l8=n,l9=n;
    long m0=n,m1=n,m2=n,m3=n,m4=n,m5=n,m6=n,m7=n,m8=n,m9=n;
    k9 += 100000;
    return (int) (a0 + b9 + k9 + m9);
  }
}`,
	"Unicode": `public class Unicode {
  int caf\u00e9 = 1;
  int m\u00fcnchen() { return caf\u00e9; }
  static String \u03c0() { return "\\u00a0"; }
}`,
	"Shapes": `import java.util.*;
public class Shapes<T extends Number & Comparable<T>> implements Cloneable, java.io.Serializable {
  public static final int CONST = 7;
  private volatile transient List<? super String> sink;
  static { System.out.print(""); }
  public synchronized <X> X pick(List<? extends X> in, X... more) throws java.io.IOException { return null; }
  interface Inner { default int d() { return 1; } static int s() { return 2; } private int p() { return 3; } }
  record Pair(int a, String b) {}
  enum Kind { A, B }
  abstract static class Base { abstract void go(); }
}`,
}

// Older class files exercise the pre-condy constant pool and pre-Java-9 layout.
// Only the fixtures that are valid Java 8 are compiled for the older release
// (`Shapes` uses records and private interface methods).
const oldRelease = "8"

var oldReleaseFixtures = map[string]bool{
	"Switches": true, "Guards": true, "Dynamic": true, "Numbers": true, "Wide": true,
	"Unicode": true,
}

func hasTool(name string) bool {
	return exec.Command(name, "-version").Run() == nil
}

func TestDisasmMatchesJavap(t *testing.T) {
	if !hasTool("javac") || !hasTool("javap") {
		t.Skip("no JDK (javac/javap)")
	}
	for _, release := range []string{"21", oldRelease} {
		for name, source := range javacFixtures {
			if release == oldRelease && !oldReleaseFixtures[name] {
				continue
			}
			t.Run(release+"/"+name, func(t *testing.T) {
				dir := t.TempDir()
				javaFile := filepath.Join(dir, name+".java")
				if err := os.WriteFile(javaFile, []byte(source), 0o644); err != nil {
					t.Fatalf("write source: %v", err)
				}
				out, err := exec.Command("javac", "--release", release, "-d", dir, javaFile).CombinedOutput()
				if err != nil {
					t.Fatalf("javac: %v\n%s", err, out)
				}
				entries, err := os.ReadDir(dir)
				if err != nil {
					t.Fatalf("read dir: %v", err)
				}
				for _, entry := range entries {
					if !strings.HasSuffix(entry.Name(), ".class") {
						continue
					}
					classFile := filepath.Join(dir, entry.Name())
					theirs, err := exec.Command("javap", "-c", "-p", classFile).Output()
					if err != nil {
						t.Fatalf("javap: %v", err)
					}
					b, err := os.ReadFile(classFile)
					if err != nil {
						t.Fatalf("read class: %v", err)
					}
					ours, err := Disassemble(b)
					if err != nil {
						t.Fatalf("disassemble: %v", err)
					}
					if ours != string(theirs) {
						t.Errorf("%s:\n--- got ---\n%s\n--- want ---\n%s", entry.Name(), ours, theirs)
					}
				}
			})
		}
	}
}

// --- malformed and unsupported input --------------------------------------------------

func TestDecodeInstructionsRejectsTruncatedCode(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(emitBaselinesDir, "Concat.class"))
	if err != nil {
		t.Fatal(err)
	}
	classFile, err := ReadClassFile(b)
	if err != nil {
		t.Fatal(err)
	}
	// ldc without its index, invokevirtual with half an index, a tableswitch whose
	// padding alone runs off the end, iinc without operands, a dangling wide.
	for _, code := range [][]byte{{0x12}, {0xb6, 0x00}, {0xaa, 0x00, 0x00}, {0x84}, {0xc4, 0x15}} {
		if _, err := DecodeInstructions(classFile, code); err == nil {
			t.Errorf("DecodeInstructions(%v) = nil error, want a failure", code)
		}
	}
}

func TestDisassembleRejectsTruncatedClass(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(emitBaselinesDir, "Pt.class"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Disassemble(b[:len(b)-12]); err == nil {
		t.Error("a truncated class file produced a listing")
	}
}

func TestDisassembleRefusesModuleInfo(t *testing.T) {
	if !hasTool("javac") {
		t.Skip("no JDK (javac)")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "module-info.java")
	if err := os.WriteFile(source, []byte("module cappu.test.mod { requires java.base; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("javac", "-d", dir, source).CombinedOutput(); err != nil {
		t.Fatalf("javac: %v\n%s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(dir, "module-info.class"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Disassemble(b)
	if err == nil || !strings.Contains(err.Error(), "module descriptors are not supported yet") {
		t.Errorf("err = %v, want the module-descriptor refusal", err)
	}
}

func TestEscapeStringRendersUnpairedSurrogateLikeJavap(t *testing.T) {
	// The pool holds a lone high surrogate; it has no encoding, and javap prints "?".
	lone := encodeUTF16([]uint16{'a', 0xd800, 'b'})
	if got := escapeString(lone); got != "a?b" {
		t.Errorf("escapeString(lone surrogate) = %q, want %q", got, "a?b")
	}
	if got := escapeString("ok"); got != "ok" {
		t.Errorf("escapeString(%q) = %q", "ok", got)
	}
}

// --- Java's own number formatting ----------------------------------------------------

func TestJavaDoubleText(t *testing.T) {
	cases := []struct {
		value float64
		want  string
	}{
		{0, "0.0"},
		{math.Copysign(0, -1), "-0.0"},
		{1, "1.0"},
		{100, "100.0"},
		{0.001, "0.001"},
		{0.0001, "1.0E-4"},
		{9999999, "9999999.0"},
		{1e7, "1.0E7"},
		{1.5e300, "1.5E300"},
		{math.Pi, "3.141592653589793"},
		{5.9604644775390625e-8, "5.960464477539063E-8"},
		{4.9e-324, "4.9E-324"},
		{math.Inf(1), "Infinity"},
		{math.Inf(-1), "-Infinity"},
		{math.NaN(), "NaN"},
	}
	for _, c := range cases {
		if got := JavaDoubleText(c.value); got != c.want {
			t.Errorf("JavaDoubleText(%v) = %q, want %q", c.value, got, c.want)
		}
	}
}

func TestJavaFloatText(t *testing.T) {
	cases := []struct {
		value float32
		want  string
	}{
		{0.1, "0.1"},
		{0.3, "0.3"},
		{3.14159, "3.14159"},
		{1e10, "1.0E10"},
		{16777216, "1.6777216E7"},
		{359427.125, "359427.12"},
		{math.SmallestNonzeroFloat32, "1.4E-45"},
		{2 * math.SmallestNonzeroFloat32, "2.8E-45"},
		{3 * math.SmallestNonzeroFloat32, "4.2E-45"},
		{0, "0.0"},
	}
	for _, c := range cases {
		if got := JavaFloatText(c.value); got != c.want {
			t.Errorf("JavaFloatText(%v) = %q, want %q", c.value, got, c.want)
		}
	}
}
