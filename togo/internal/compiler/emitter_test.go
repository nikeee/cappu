package compiler

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// Port of src/compiler/emitter.test.ts (the binary-baseline tier). Each fixture
// is emitted with the Go backend and compared byte-for-byte against the .class
// baseline produced by the reference TS emitter (test-fixtures/emitter/
// emit-baselines). No JDK is needed: the baselines are committed bytes.

var emitFixtures = map[string]string{
	"Empty":              "class Empty {}",
	"Fields":             "class Fields { int a; java.lang.String b; long c; int[] d; boolean e; double[][] f; }",
	"ModifiedFields":     "public class ModifiedFields { public int x; private static final long y = 0; protected java.lang.String z; }",
	"Methods":            "public class Methods { void a() {} public int b(int p) { return p; } static long c(long x, int y) { return x; } java.lang.String d(java.lang.String s) { return s; } int[] e(int[] arr) { return arr; } }",
	"VarargsAndAbstract": "abstract class VarargsAndAbstract { abstract int f(int n); int g(int... xs) { return 0; } static double h(double a, double b) { return a; } }",
	"Hello":              `public class Hello { public static void main(String[] args) { System.out.println("Hello, world"); } }`,
	"ReturnLiterals":     `class ReturnLiterals { int i() { return 42; } long l() { return 7L; } boolean b() { return true; } java.lang.String s() { return "hi"; } int big() { return 1000000; } int echo(int p) { return p; } void v() {} }`,
	"Arithmetic":         "class Arithmetic { int add(int a, int b) { return a + b; } int poly(int a, int b, int c) { return a * b + c; } long mix(int a, long b) { return a + b; } double dm(double x, int y) { return x * y; } int shift(int a, int n) { return a << n; } int bits(int a, int b) { return (a & b) | (a ^ b); } int neg(int a) { return -a; } int not(int a) { return ~a; } int rem(int a, int b) { return a % b; } }",
	"Locals":             "class Locals { int compute(int n) { int x = n + 1; int y = x * 2; int z; z = x + y; return z; } long widen(int n) { long w = n; return w + 1; } int reassign(int n) { int t = n; t = t + t; return t; } }",
	"Fold":               "class Fold { int a() { return 6 * 7; } long b() { return 100L * 100L; } int c() { return 1 << 10; } boolean d() { return 3 < 5; } int e() { return 10 / 3 + 7 % 4; } int f() { return -(2 + 3); } int g() { return (1 + 2) * (3 + 4); } }",
	"Pt":                 "public class Pt { int x; int y; Pt(int x, int y) { this.x = x; this.y = y; } int sum() { return x + y; } }",
	"Compute":            "public class Compute { static int v() { return 42; } public static void main(String[] args) { int a = v(); int b = a - 2; System.out.println(b); } }",
	"FloatArith":         "class FloatArith { float mul(float a, float b) { return a * b; } float addc(float a) { return a + 0.1f; } double div(double a, double b) { return a / b; } float neg(float a) { return -a; } double mix(double a, float b) { return a + b; } float rem(float a, float b) { return a % b; } double poly(double x) { return x * x + x; } }",
	"FloatConv":          "class FloatConv { int toInt(double d) { return (int) d; } long toLong(double d) { return (long) d; } double widenLong(long x) { return x; } float fromLong(long x) { return x; } float fromInt(int n) { return n; } double widenFloat(float f) { return f; } int truncFloat(float f) { return (int) f; } float narrowDouble(double d) { return (float) d; } }",
	"FloatConst":         "class FloatConst { float a() { return 0.1f; } float b() { return 3.14159f; } float c() { return 1.0e10f; } float d() { return 0.3f; } float big() { return 16777217f; } double e() { return 0.1; } double pi() { return 3.141592653589793; } float fz() { return 0.0f; } float fo() { return 1.0f; } float ft() { return 2.0f; } }",
	"IntLiterals":        "class IntLiterals { int hexFf() { return 0xff; } int hexE() { return 0xe; } int hexD() { return 0xd; } int hex1e() { return 0x1e; } int cafe() { return 0xCafe; } int allOnes() { return 0xFFFFFFFF; } long hexL() { return 0xFFL; } int oct() { return 010; } int bin() { return 0b1010; } int big() { return 1000000; } }",
	"LongArith":          "class LongArith { long add(long a, long b) { return a + b; } long sub(long a, long b) { return a - b; } long mul(long a, long b) { return a * b; } long div(long a, long b) { return a / b; } long rem(long a, long b) { return a % b; } long neg(long a) { return -a; } long and(long a, long b) { return a & b; } long or(long a, long b) { return a | b; } long xor(long a, long b) { return a ^ b; } long shl(long a, int b) { return a << b; } long shr(long a, int b) { return a >> b; } long ushr(long a, int b) { return a >>> b; } long not(long a) { return ~a; } }",
	"IntConv":            "class IntConv { long toLong(int a) { return a; } float toFloat(int a) { return a; } double toDouble(int a) { return a; } byte toByte(int a) { return (byte) a; } char toChar(int a) { return (char) a; } short toShort(int a) { return (short) a; } }",
	"ArrayLoad":          "class ArrayLoad { int i(int[] a, int k) { return a[k]; } long l(long[] a, int k) { return a[k]; } float f(float[] a, int k) { return a[k]; } double d(double[] a, int k) { return a[k]; } byte b(byte[] a, int k) { return a[k]; } char c(char[] a, int k) { return a[k]; } short s(short[] a, int k) { return a[k]; } boolean z(boolean[] a, int k) { return a[k]; } Object o(Object[] a, int k) { return a[k]; } int len(int[] a) { return a.length; } }",
	"ArrayStore":         "class ArrayStore { void i(int[] a, int k, int v) { a[k] = v; } void l(long[] a, int k, long v) { a[k] = v; } void f(float[] a, int k, float v) { a[k] = v; } void d(double[] a, int k, double v) { a[k] = v; } void b(byte[] a, int k, byte v) { a[k] = v; } void c(char[] a, int k, char v) { a[k] = v; } void s(short[] a, int k, short v) { a[k] = v; } void o(Object[] a, int k, Object v) { a[k] = v; } }",
	"NewArray":           "class NewArray { int[] prim(int n) { return new int[n]; } String[] ref(int n) { return new String[n]; } boolean[] bools(int n) { return new boolean[n]; } long[] longs(int n) { return new long[n]; } int[][] multi(int m, int n) { return new int[m][n]; } }",
	"StaticFields":       "class StaticFields { static int counter; static long total; int x; long y; static int getC() { return counter; } static void setC(int v) { counter = v; } int getX() { return x; } void setX(int v) { x = v; } static long getT() { return total; } void setY(long v) { y = v; } }",
	"Boxing":             "class Boxing { Integer bi(int x) { return x; } Long bl(long x) { return x; } Double bd(double x) { return x; } Float bf(float x) { return x; } Boolean bz(boolean x) { return x; } Character bc(char x) { return x; } int ui(Integer x) { return x; } long ul(Long x) { return x; } double ud(Double x) { return x; } boolean uz(Boolean x) { return x; } }",
	"CastInstance":       "class CastInstance { String down(Object o) { return (String) o; } CharSequence up(String s) { return s; } boolean isStr(Object o) { return o instanceof String; } int[] arr(Object o) { return (int[]) o; } }",
	"Concat":             "class Concat { String si(String a, int b) { return a + b; } String is(int a, String b) { return a + b; } String ss(String a, String b) { return a + b; } String sl(String a, long b) { return a + b; } String sd(String a, double b) { return a + b; } String sb(String a, boolean b) { return a + b; } String sc(String a, char b) { return a + b; } }",
	"Invoke":             "class Invoke { static int stat() { return 1; } int inst() { return 2; } int callStat() { return stat(); } int callInst() { return inst(); } int strLen(String s) { return s.length(); } }",
	"PrivateCall":        "class PrivateCall { private int secret(int x) { return x * 2; } int use(int x) { return secret(x) + 1; } }",
	"BoundErasure":       "class BoundErasure<T extends CharSequence> { T v; T get() { return v; } int len() { return v.length(); } static <U extends Comparable<U>> int cmp(U a, U b) { return a.compareTo(b); } static <V> V id(V x) { return x; } }",
	"Constants":          `class Constants { int zero() { return 0; } int five() { return 5; } int m1() { return -1; } int bp() { return 100; } int sp() { return 1000; } int big() { return 100000; } long lone() { return 1L; } long lbig() { return 10000000000L; } float fz() { return 0f; } double dz() { return 0.0; } String s() { return "x"; } }`,
	"Returns":            "class Returns { int i() { return 7; } long l() { return 7L; } float f() { return 1.5f; } double d() { return 2.5; } boolean b() { return true; } char c() { return 'Z'; } byte by() { return 3; } short sh() { return 300; } String s() { return \"hi\"; } Object o() { return null; } void v() {} }",
	"VarargsPack":        `class VarargsPack { static int sum(int... xs) { return xs.length; } static String join(String sep, Object... ps) { return sep; } int callPrim() { return sum(1, 2, 3); } int callEmpty() { return sum(); } String callMixed() { return join("-", "a", "b"); } }`,

	// Multi-class fixtures (top-level + nested/anonymous): every emitted class
	// (including Outer$Inner) has its own baseline.
	"ClassLit":      "public class ClassLit {\n  Class<?> ref() { return ClassLit.class; }\n  Class<?> str() { return String.class; }\n  Class<?> prim() { return int.class; }\n  Class<?> arr() { return String[].class; }\n}",
	"QualifiedAnon": "public class QualifiedAnon {\n  int x = 7;\n  class Inner {\n    int v;\n    Inner(int a) { v = a; }\n    int get() { return v + x; }\n  }\n  static int use(QualifiedAnon outer) {\n    QualifiedAnon.Inner i = outer.new Inner(5) { int get() { return v + 100; } };\n    return i.get();\n  }\n}",
	"ICast":         "public class ICast {\n  interface A { int a(); }\n  interface B { int b(); }\n  static int use(Object o) {\n    A x = (A & B) o;\n    return x.a();\n  }\n}",
	"QualifiedNew":  "public class QualifiedNew {\n  int x = 7;\n  class Inner { int v; Inner(int a) { v = a; } int sum() { return v + x; } }\n  static int make(QualifiedNew outer) { return outer.new Inner(5).v; }\n}",
	"Nest":          "public class Nest {\n  static class Point { int x, y; Point(int x, int y){ this.x=x; this.y=y; } int sum(){ return x+y; } }\n  static class Counter { static int total; int n; void tick(){ n++; total++; } int get(){ return n; } }\n  static int helper(int a){ return a*2; }\n}",
}

func TestEmitterBaselines(t *testing.T) {
	baseDir := filepath.Join("..", "..", "..", "test-fixtures", "emitter", "emit-baselines")
	for name, source := range emitFixtures {
		t.Run(name, func(t *testing.T) {
			program := NewProgram()
			LoadJdkStub(program)
			uri := URI("file:///" + name + ".java")
			program.SetOpenDocument(uri, source, 1)
			checker := NewChecker(program)
			classes := EmitSourceFile(program.GetSourceFile(uri), program, checker, false)
			if len(classes) == 0 {
				t.Fatalf("%s emitted no classes", name)
			}
			for _, cls := range classes {
				want, err := os.ReadFile(filepath.Join(baseDir, cls.Name+".class"))
				if err != nil {
					t.Errorf("no baseline for emitted class %s: %v", cls.Name, err)
					continue
				}
				if !bytes.Equal(cls.Bytes, want) {
					t.Errorf("%s: emitted bytes differ from baseline (%d vs %d bytes)%s", cls.Name, len(cls.Bytes), len(want), firstDiff(cls.Bytes, want))
				}
			}
		})
	}
}

func firstDiff(got, want []byte) string {
	n := len(got)
	if len(want) < n {
		n = len(want)
	}
	for i := 0; i < n; i++ {
		if got[i] != want[i] {
			return " first diff at offset " + itoaDiff(i)
		}
	}
	return ""
}

func itoaDiff(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
