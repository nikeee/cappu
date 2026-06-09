import { test } from "node:test";
import { expect } from "expect";
import { execFileSync } from "node:child_process";
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { createChecker } from "./checker.ts";
import { emitSourceFile } from "./emitter.ts";
import { type Disasm, disasmFiles } from "./javapNormalize.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { createProgram } from "./program.ts";

const here = dirname(fileURLToPath(import.meta.url));
const baselinesDir = join(here, "..", "test-fixtures", "emitter", "emit-baselines");
// Normalized javac disassembly, checked in as plain-text JSON so the byte-match
// tests do not have to run javac on every test run (only when regenerating).
const javacRefDir = join(here, "..", "test-fixtures", "emitter", "javac-baselines");
const shouldUpdate = process.env.UPDATE_BASELINES === "1";

function hasTool(name: string): boolean {
  try {
    execFileSync(name, ["-version"], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
}
const HAS_JAVA = hasTool("java") && hasTool("javap");
const HAS_JAVAC = hasTool("javac");

// Each fixture is a single class named after its key; the source defines `class
// <name>` so the .java/.class file names line up for javac comparison.
const FIXTURES: Record<string, string> = {
  Empty: "class Empty {}",
  Fields: "class Fields { int a; java.lang.String b; long c; int[] d; boolean e; double[][] f; }",
  ModifiedFields:
    "public class ModifiedFields { public int x; private static final long y = 0; protected java.lang.String z; }",
  Methods:
    "public class Methods { void a() {} public int b(int p) { return p; } static long c(long x, int y) { return x; } java.lang.String d(java.lang.String s) { return s; } int[] e(int[] arr) { return arr; } }",
  VarargsAndAbstract:
    "abstract class VarargsAndAbstract { abstract int f(int n); int g(int... xs) { return 0; } static double h(double a, double b) { return a; } }",
  Hello:
    'public class Hello { public static void main(String[] args) { System.out.println("Hello, world"); } }',
  ReturnLiterals:
    'class ReturnLiterals { int i() { return 42; } long l() { return 7L; } boolean b() { return true; } java.lang.String s() { return "hi"; } int big() { return 1000000; } int echo(int p) { return p; } void v() {} }',
  Arithmetic:
    "class Arithmetic { int add(int a, int b) { return a + b; } int poly(int a, int b, int c) { return a * b + c; } long mix(int a, long b) { return a + b; } double dm(double x, int y) { return x * y; } int shift(int a, int n) { return a << n; } int bits(int a, int b) { return (a & b) | (a ^ b); } int neg(int a) { return -a; } int not(int a) { return ~a; } int rem(int a, int b) { return a % b; } }",
  Locals:
    "class Locals { int compute(int n) { int x = n + 1; int y = x * 2; int z; z = x + y; return z; } long widen(int n) { long w = n; return w + 1; } int reassign(int n) { int t = n; t = t + t; return t; } }",
  Fold: "class Fold { int a() { return 6 * 7; } long b() { return 100L * 100L; } int c() { return 1 << 10; } boolean d() { return 3 < 5; } int e() { return 10 / 3 + 7 % 4; } int f() { return -(2 + 3); } int g() { return (1 + 2) * (3 + 4); } }",
  Pt: "public class Pt { int x; int y; Pt(int x, int y) { this.x = x; this.y = y; } int sum() { return x + y; } }",
  // Uses a method call (not a constant expression) so javac does not fold it,
  // keeping the comparison honest until constant folding (JLS 15.28) is added.
  Compute:
    "public class Compute { static int v() { return 42; } public static void main(String[] args) { int a = v(); int b = a - 2; System.out.println(b); } }",
  // float/double arithmetic over parameters (not constant-foldable), so the
  // instruction stream is byte-comparable to javac: fadd/fmul/ddiv/fneg/frem,
  // and the f2d widening for the mixed double+float operand.
  FloatArith:
    "class FloatArith { float mul(float a, float b) { return a * b; } float addc(float a) { return a + 0.1f; } double div(double a, double b) { return a / b; } float neg(float a) { return -a; } double mix(double a, float b) { return a + b; } float rem(float a, float b) { return a % b; } double poly(double x) { return x * x + x; } }",
  // Every primitive numeric conversion: i2f, l2d, l2f, f2d, d2i, d2l, d2f, f2i.
  FloatConv:
    "class FloatConv { int toInt(double d) { return (int) d; } long toLong(double d) { return (long) d; } double widenLong(long x) { return x; } float fromLong(long x) { return x; } float fromInt(int n) { return n; } double widenFloat(float f) { return f; } int truncFloat(float f) { return (int) f; } float narrowDouble(double d) { return (float) d; } }",
  // Float/double constant literals: exercises the JS parseFloat -> setFloat32
  // path (32-bit rounding must match javac's decimal-to-float) and fconst_0/1/2.
  FloatConst:
    "class FloatConst { float a() { return 0.1f; } float b() { return 3.14159f; } float c() { return 1.0e10f; } float d() { return 0.3f; } float big() { return 16777217f; } double e() { return 0.1; } double pi() { return 3.141592653589793; } float fz() { return 0.0f; } float fo() { return 1.0f; } float ft() { return 2.0f; } }",
  // Integer literal forms, including hex digits a-f that must NOT be read as
  // float/double suffixes (0xff, 0xe, 0xd), octal, binary, and 32-bit wrap.
  IntLiterals:
    "class IntLiterals { int hexFf() { return 0xff; } int hexE() { return 0xe; } int hexD() { return 0xd; } int hex1e() { return 0x1e; } int cafe() { return 0xCafe; } int allOnes() { return 0xFFFFFFFF; } long hexL() { return 0xFFL; } int oct() { return 010; } int bin() { return 0b1010; } int big() { return 1000000; } }",
  // Every long ALU op over parameters (ladd/lsub/lmul/ldiv/lrem/lneg/land/lor/lxor
  // and the int-shift-amount lshl/lshr/lushr), plus lcmp-free bitwise complement.
  LongArith:
    "class LongArith { long add(long a, long b) { return a + b; } long sub(long a, long b) { return a - b; } long mul(long a, long b) { return a * b; } long div(long a, long b) { return a / b; } long rem(long a, long b) { return a % b; } long neg(long a) { return -a; } long and(long a, long b) { return a & b; } long or(long a, long b) { return a | b; } long xor(long a, long b) { return a ^ b; } long shl(long a, int b) { return a << b; } long shr(long a, int b) { return a >> b; } long ushr(long a, int b) { return a >>> b; } long not(long a) { return ~a; } }",
  // Widening/narrowing from int: i2l/i2f/i2d and the narrowing i2b/i2c/i2s casts.
  IntConv:
    "class IntConv { long toLong(int a) { return a; } float toFloat(int a) { return a; } double toDouble(int a) { return a; } byte toByte(int a) { return (byte) a; } char toChar(int a) { return (char) a; } short toShort(int a) { return (short) a; } }",
  // Array element loads for every type (iaload/laload/faload/daload/baload/caload/
  // saload/aaload), boolean (baload) and arraylength.
  ArrayLoad:
    "class ArrayLoad { int i(int[] a, int k) { return a[k]; } long l(long[] a, int k) { return a[k]; } float f(float[] a, int k) { return a[k]; } double d(double[] a, int k) { return a[k]; } byte b(byte[] a, int k) { return a[k]; } char c(char[] a, int k) { return a[k]; } short s(short[] a, int k) { return a[k]; } boolean z(boolean[] a, int k) { return a[k]; } Object o(Object[] a, int k) { return a[k]; } int len(int[] a) { return a.length; } }",
  // Array element stores for every type (iastore/lastore/.../aastore).
  ArrayStore:
    "class ArrayStore { void i(int[] a, int k, int v) { a[k] = v; } void l(long[] a, int k, long v) { a[k] = v; } void f(float[] a, int k, float v) { a[k] = v; } void d(double[] a, int k, double v) { a[k] = v; } void b(byte[] a, int k, byte v) { a[k] = v; } void c(char[] a, int k, char v) { a[k] = v; } void s(short[] a, int k, short v) { a[k] = v; } void o(Object[] a, int k, Object v) { a[k] = v; } }",
  // Array allocation: newarray (primitive), anewarray (reference), multianewarray.
  NewArray:
    "class NewArray { int[] prim(int n) { return new int[n]; } String[] ref(int n) { return new String[n]; } boolean[] bools(int n) { return new boolean[n]; } long[] longs(int n) { return new long[n]; } int[][] multi(int m, int n) { return new int[m][n]; } }",
  // Static and instance field access: get/putstatic and get/putfield over int/long.
  StaticFields:
    "class StaticFields { static int counter; static long total; int x; long y; static int getC() { return counter; } static void setC(int v) { counter = v; } int getX() { return x; } void setX(int v) { x = v; } static long getT() { return total; } void setY(long v) { y = v; } }",
  // Autoboxing (valueOf) and unboxing (xxxValue) for the wrapper types (JLS 5.1.7/8).
  Boxing:
    "class Boxing { Integer bi(int x) { return x; } Long bl(long x) { return x; } Double bd(double x) { return x; } Float bf(float x) { return x; } Boolean bz(boolean x) { return x; } Character bc(char x) { return x; } int ui(Integer x) { return x; } long ul(Long x) { return x; } double ud(Double x) { return x; } boolean uz(Boolean x) { return x; } }",
  // checkcast (narrowing + array), upcast (no instruction), instanceof as a value.
  CastInstance:
    "class CastInstance { String down(Object o) { return (String) o; } CharSequence up(String s) { return s; } boolean isStr(Object o) { return o instanceof String; } int[] arr(Object o) { return (int[]) o; } }",
  // invokedynamic StringConcatFactory for `+` with each operand type (JLS 15.18.1).
  Concat:
    "class Concat { String si(String a, int b) { return a + b; } String is(int a, String b) { return a + b; } String ss(String a, String b) { return a + b; } String sl(String a, long b) { return a + b; } String sd(String a, double b) { return a + b; } String sb(String a, boolean b) { return a + b; } String sc(String a, char b) { return a + b; } }",
  // invokestatic / invokevirtual, and an invokevirtual on a JDK-stub type (String).
  Invoke:
    "class Invoke { static int stat() { return 1; } int inst() { return 2; } int callStat() { return stat(); } int callInst() { return inst(); } int strLen(String s) { return s.length(); } }",
  // A private instance method is invoked with invokespecial, not invokevirtual
  // (JLS 15.12.4.4), so it byte-matches javac.
  PrivateCall:
    "class PrivateCall { private int secret(int x) { return x * 2; } int use(int x) { return secret(x) + 1; } }",
  // Constant loads: iconst_m1.._5, bipush, sipush, ldc (int/long/float/double/String).
  Constants:
    "class Constants { int zero() { return 0; } int five() { return 5; } int m1() { return -1; } int bp() { return 100; } int sp() { return 1000; } int big() { return 100000; } long lone() { return 1L; } long lbig() { return 10000000000L; } float fz() { return 0f; } double dz() { return 0.0; } String s() { return \"x\"; } }",
  // The return instruction for each type (ireturn/lreturn/freturn/dreturn/areturn/return).
  Returns:
    "class Returns { int i() { return 7; } long l() { return 7L; } float f() { return 1.5f; } double d() { return 2.5; } boolean b() { return true; } char c() { return 'Z'; } byte by() { return 3; } short sh() { return 300; } String s() { return \"hi\"; } Object o() { return null; } void v() {} }",
  // Varargs call packing: the trailing args become a fresh array (matching javac's
  // anewarray/dup/iconst/astore sequence exactly), JLS 15.12.4.2.
  VarargsPack:
    "class VarargsPack { static int sum(int... xs) { return xs.length; } static String join(String sep, Object... ps) { return sep; } int callPrim() { return sum(1, 2, 3); } int callEmpty() { return sum(); } String callMixed() { return join(\"-\", \"a\", \"b\"); } }",
};

// Fixtures with a runnable main and the output they must print.
const RUNS: Record<string, string> = {
  Hello: "Hello, world\n",
  Compute: "40\n",
};

// Multi-class fixtures (a top-level class with static nested classes). Each
// emitted class - including the Outer$Inner ones - gets a binary baseline and is
// byte-matched against javac. Straight-line bodies only, so the instruction
// streams compare exactly (field ++ lowers to getfield/iconst_1/iadd/putfield,
// matching javac).
const MULTI_FIXTURES: Record<string, string> = {
  Nest: [
    "public class Nest {",
    "  static class Point { int x, y; Point(int x, int y){ this.x=x; this.y=y; } int sum(){ return x+y; } }",
    "  static class Counter { static int total; int n; void tick(){ n++; total++; } int get(){ return n; } }",
    "  static int helper(int a){ return a*2; }",
    "}",
  ].join("\n"),
};

// Control-flow fixtures: verified by the JVM (our StackMapTable must be accepted)
// and run for their output. Not byte-matched to javac (we emit full_frame frames
// and may allocate slots differently).
const CONTROL: Record<string, { source: string; stdout: string }> = {
  ControlFlow: {
    source: [
      "public class ControlFlow {",
      "  static int sum(int n) { int s = 0; for (int i = 0; i < n; i++) { s = s + i; } return s; }",
      "  static int absish(int x) { if (x < 0) { return -x; } else { return x; } }",
      "  static int countdown(int n) { int c = 0; while (n > 0) { c = c + 1; n = n - 1; } return c; }",
      "  static int firstSet(int x) { int i = 0; do { i = i + 1; } while (i < x); return i; }",
      "  static boolean inRange(int x, int lo, int hi) { return x >= lo && x <= hi; }",
      "  public static void main(String[] args) {",
      "    System.out.println(sum(5));",
      "    System.out.println(absish(-7));",
      "    System.out.println(countdown(3));",
      "    System.out.println(firstSet(4));",
      "    System.out.println(inRange(5, 1, 10));",
      "  }",
      "}",
    ].join("\n"),
    stdout: "10\n7\n3\n4\ntrue\n",
  },
};

// Compare our disassembly of a class against the javac reference.
function expectMatchesJavac(ours: Disasm | undefined, reference: Disasm): void {
  expect(ours).toBeDefined();
  expect(ours!.members).toEqual(reference.members);
  const ourCode = new Map(ours!.code);
  const refCode = new Map(reference.code);
  expect([...ourCode.keys()].sort()).toEqual([...refCode.keys()].sort());
  for (const [sig, instrs] of refCode) expect(ourCode.get(sig)).toEqual(instrs);
}

function emit(name: string, source: string): Uint8Array {
  const program = createProgram();
  loadJdkStub(program);
  const uri = `file:///${name}.java`;
  program.setOpenDocument(uri, source, 1);
  const classes = emitSourceFile(program.getSourceFile(uri)!, program, createChecker(program));
  const cls = classes.find(c => c.name === name);
  if (!cls) throw new Error(`no emitted class named ${name}`);
  return cls.bytes;
}

// Emit every class declared in `source` (top-level and, later, nested/lambda
// synthetics), keyed by internal name.
function emitClasses(mainClass: string, source: string): { name: string; bytes: Uint8Array }[] {
  const program = createProgram();
  loadJdkStub(program);
  const uri = `file:///${mainClass}.java`;
  program.setOpenDocument(uri, source, 1);
  return emitSourceFile(program.getSourceFile(uri)!, program, createChecker(program));
}

// Run our emitted main class under `java` and assert its stdout equals the
// expected text. The expected text IS the javac reference (verified once when the
// baseline was written); to re-confirm it against a live javac, run with
// UPDATE_BASELINES=1, which recompiles and asserts javac still prints it.
function runsLikeJavac(mainClass: string, source: string, expectedStdout: string): void {
  if (shouldUpdate && HAS_JAVAC && HAS_JAVA) {
    const ref = mkdtempSync(join(tmpdir(), "emit-ref-"));
    writeFileSync(join(ref, `${mainClass}.java`), source);
    execFileSync("javac", ["--release", "21", "-d", ref, join(ref, `${mainClass}.java`)]);
    expect(execFileSync("java", ["-cp", ref, mainClass], { encoding: "utf8" })).toBe(expectedStdout);
  }

  const ours = mkdtempSync(join(tmpdir(), "emit-ours-"));
  for (const c of emitClasses(mainClass, source)) {
    const out = join(ours, `${c.name}.class`);
    mkdirSync(dirname(out), { recursive: true });
    writeFileSync(out, c.bytes);
  }
  expect(execFileSync("java", ["-cp", ours, mainClass], { encoding: "utf8" })).toBe(expectedStdout);
}

// javac's normalized disassembly for a fixture's classes, keyed by class name.
// Reads the checked-in JSON; regenerates it from javac when UPDATE_BASELINES=1 or
// the file is missing and a JDK is present. Returns undefined when neither a
// checked-in baseline nor javac is available (the test then skips the comparison).
function loadJavacRef(fixtureName: string, source: string): Map<string, Disasm> | undefined {
  const file = join(javacRefDir, `${fixtureName}.json`);
  if (!shouldUpdate && existsSync(file)) {
    return new Map(Object.entries(JSON.parse(readFileSync(file, "utf8")) as Record<string, Disasm>));
  }
  if (!(HAS_JAVAC && HAS_JAVA)) return undefined;
  const dir = mkdtempSync(join(tmpdir(), "javac-ref-"));
  writeFileSync(join(dir, `${fixtureName}.java`), source);
  execFileSync("javac", ["--release", "21", "-d", dir, join(dir, `${fixtureName}.java`)]);
  const classFiles = readdirSync(dir)
    .filter(f => f.endsWith(".class"))
    .map(f => join(dir, f));
  const map = disasmFiles(classFiles);
  mkdirSync(javacRefDir, { recursive: true });
  writeFileSync(file, `${JSON.stringify(Object.fromEntries(map), null, 2)}\n`);
  return map;
}

// Disassembly of every fixture class we emit, computed once in a single javap
// invocation and memoized, so the per-fixture byte-match tests share one launch.
let oursDisasmCache: Map<string, Disasm> | undefined;
function oursDisasm(): Map<string, Disasm> {
  if (oursDisasmCache) return oursDisasmCache;
  const dir = mkdtempSync(join(tmpdir(), "emit-ours-all-"));
  for (const [name, src] of [...Object.entries(FIXTURES), ...Object.entries(MULTI_FIXTURES)]) {
    for (const c of emitClasses(name, src)) writeFileSync(join(dir, `${c.name}.class`), c.bytes);
  }
  const files = readdirSync(dir)
    .filter(f => f.endsWith(".class"))
    .map(f => join(dir, f));
  oursDisasmCache = disasmFiles(files);
  return oursDisasmCache;
}

for (const [name, source] of Object.entries(FIXTURES)) {
  test(`emit binary baseline: ${name}`, () => {
    const bytes = emit(name, source);
    const baseline = join(baselinesDir, `${name}.class`);
    if (shouldUpdate || !existsSync(baseline)) {
      mkdirSync(baselinesDir, { recursive: true });
      writeFileSync(baseline, bytes);
    }
    expect(Buffer.from(bytes).equals(readFileSync(baseline))).toBe(true);
  });

  // Members + instruction stream must match javac, compared against the checked-in
  // normalized disassembly (no javac at test time). These straight-line fixtures
  // have no branches, so matching javac's instructions implies the class verifies;
  // control-flow validity is covered by the CONTROL and runsLikeJavac tests.
  test(`bytecode matches javac: ${name}`, { skip: HAS_JAVA ? false : "no JDK (javap)" }, () => {
    const ref = loadJavacRef(name, source);
    if (!ref) return; // no baseline and no javac to generate one
    expectMatchesJavac(oursDisasm().get(name), ref.get(name)!);
  });
}

for (const [name, expected] of Object.entries(RUNS)) {
  test(`runs and prints: ${name}`, { skip: HAS_JAVA ? false : "no JDK" }, () => {
    const dir = mkdtempSync(join(tmpdir(), "emit-run-"));
    writeFileSync(join(dir, `${name}.class`), emit(name, source(name)));
    const out = execFileSync("java", ["-cp", dir, name], { encoding: "utf8" });
    expect(out).toBe(expected);
  });
}

for (const [name, source] of Object.entries(MULTI_FIXTURES)) {
  test(`multi-class binary baseline: ${name}`, () => {
    for (const c of emitClasses(name, source)) {
      const baseline = join(baselinesDir, `${c.name}.class`);
      if (shouldUpdate || !existsSync(baseline)) {
        mkdirSync(baselinesDir, { recursive: true });
        writeFileSync(baseline, c.bytes);
      }
      expect(Buffer.from(c.bytes).equals(readFileSync(baseline))).toBe(true);
    }
  });

  test(
    `multi-class bytecode matches javac: ${name}`,
    { skip: HAS_JAVA ? false : "no JDK (javap)" },
    () => {
      const ref = loadJavacRef(name, source);
      if (!ref) return;
      // We must emit exactly the classes javac does (each nested class as its own
      // Outer$Inner.class), and each must match javac's disassembly.
      const emitted = emitClasses(name, source).map(c => c.name).sort();
      expect(emitted).toEqual([...ref.keys()].sort());
      for (const cn of emitted) expectMatchesJavac(oursDisasm().get(cn), ref.get(cn)!);
    },
  );
}

function source(name: string): string {
  return FIXTURES[name]!;
}

test(
  "folded overflow constants run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Overflow",
      [
        "public class Overflow {",
        "  public static void main(String[] args) {",
        "    System.out.println(2147483647 + 1);",
        "    System.out.println(-8 >>> 1);",
        "    System.out.println(9223372036854775807L + 1L);",
        "    System.out.println(1 << 33);",
        "    System.out.println(2147483647 * 2);",
        "  }",
        "}",
      ].join("\n"),
      "-2147483648\n2147483644\n-9223372036854775808\n2\n-2\n",
    );
  },
);

test(
  "casts and instanceof run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    const name = "Cast";
    const src = [
      "public class Cast {",
      "  public static void main(String[] args) {",
      "    long big = 300L;",
      "    int i = (int) big;",
      "    byte b = (byte) i;",
      "    int t = (int) (i * 2);",
      "    System.out.println(i);",
      "    System.out.println(b);",
      "    System.out.println(t);",
      '    Object o = "hello";',
      "    String s = (String) o;",
      "    System.out.println(s);",
      "    System.out.println(o instanceof String);",
      "    System.out.println(o instanceof Integer);",
      "  }",
      "}",
    ].join("\n");
    const ref = mkdtempSync(join(tmpdir(), "emit-ref-"));
    writeFileSync(join(ref, `${name}.java`), src);
    execFileSync("javac", ["--release", "21", "-d", ref, join(ref, `${name}.java`)]);
    const refOut = execFileSync("java", ["-cp", ref, name], { encoding: "utf8" });

    const ours = mkdtempSync(join(tmpdir(), "emit-ours-"));
    writeFileSync(join(ours, `${name}.class`), emit(name, src));
    const ourOut = execFileSync("java", ["-cp", ours, name], { encoding: "utf8" });
    expect(ourOut).toBe(refOut);
    expect(refOut).toBe("300\n44\n600\nhello\ntrue\nfalse\n");
  },
);

test(
  "inheritance, interfaces and packages run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    const sources: Record<string, string> = {
      "Animal.java": 'package com.app;\npublic class Animal { String sound() { return "?"; } }',
      "Dog.java":
        'package com.app;\npublic class Dog extends Animal implements Runnable { public void run() {} String sound() { return "woof"; } }',
      "Main.java":
        "package com.app;\npublic class Main { public static void main(String[] args) { Animal a = new Dog(); System.out.println(a.sound()); } }",
    };

    // javac reference.
    const ref = mkdtempSync(join(tmpdir(), "emit-ref-"));
    for (const [file, text] of Object.entries(sources)) writeFileSync(join(ref, file), text);
    execFileSync("javac", [
      "--release",
      "21",
      "-d",
      ref,
      ...Object.keys(sources).map(f => join(ref, f)),
    ]);
    const refOut = execFileSync("java", ["-cp", ref, "com.app.Main"], { encoding: "utf8" });

    // Ours: one program over all sources; write each class to its package path.
    const program = createProgram();
    loadJdkStub(program);
    for (const [file, text] of Object.entries(sources)) {
      program.addProjectFile(`file:///${file}`, text);
    }
    const checker = createChecker(program);
    const ours = mkdtempSync(join(tmpdir(), "emit-ours-"));
    for (const file of Object.keys(sources)) {
      for (const cls of emitSourceFile(
        program.getSourceFile(`file:///${file}`)!,
        program,
        checker,
      )) {
        const out = join(ours, `${cls.name}.class`);
        mkdirSync(dirname(out), { recursive: true });
        writeFileSync(out, cls.bytes);
      }
    }
    const ourOut = execFileSync("java", ["-cp", ours, "com.app.Main"], { encoding: "utf8" });

    expect(ourOut).toBe(refOut);
    expect(refOut).toBe("woof\n");
  },
);

test(
  "string concatenation (invokedynamic) runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    const name = "Sc";
    const src = [
      "public class Sc {",
      "  public static void main(String[] args) {",
      '    String who = "world";',
      "    int n = 42;",
      '    System.out.println("Hello, " + who + "!");',
      '    System.out.println("n = " + n + ", twice = " + (n * 2));',
      '    System.out.println("char: " + \'X\' + " bool: " + true);',
      '    System.out.println(1 + 2 + " items");',
      "  }",
      "}",
    ].join("\n");
    const ref = mkdtempSync(join(tmpdir(), "emit-ref-"));
    writeFileSync(join(ref, `${name}.java`), src);
    execFileSync("javac", ["--release", "21", "-d", ref, join(ref, `${name}.java`)]);
    const refOut = execFileSync("java", ["-cp", ref, name], { encoding: "utf8" });

    const ours = mkdtempSync(join(tmpdir(), "emit-ours-"));
    writeFileSync(join(ours, `${name}.class`), emit(name, src));
    const ourOut = execFileSync("java", ["-cp", ours, name], { encoding: "utf8" });
    expect(ourOut).toBe(refOut);
    expect(refOut).toBe("Hello, world!\nn = 42, twice = 84\nchar: X bool: true\n3 items\n");
  },
);

test(
  "field initializers and static constants run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    const name = "Ini";
    const src = [
      "public class Ini {",
      "  int a = 5;",
      "  int b = a + 10;",
      "  static int s = 100;",
      "  static final int K = 42;",
      "  static final long BIG = 1000L;",
      "  int getA() { return a; }",
      "  int getB() { return b; }",
      "  public static void main(String[] args) {",
      "    Ini o = new Ini();",
      "    System.out.println(o.getA());",
      "    System.out.println(o.getB());",
      "    System.out.println(s);",
      "    System.out.println(K);",
      "    System.out.println(BIG);",
      "  }",
      "}",
    ].join("\n");
    const ref = mkdtempSync(join(tmpdir(), "emit-ref-"));
    writeFileSync(join(ref, `${name}.java`), src);
    execFileSync("javac", ["--release", "21", "-d", ref, join(ref, `${name}.java`)]);
    const refOut = execFileSync("java", ["-cp", ref, name], { encoding: "utf8" });

    const ours = mkdtempSync(join(tmpdir(), "emit-ours-"));
    writeFileSync(join(ours, `${name}.class`), emit(name, src));
    const ourOut = execFileSync("java", ["-cp", ours, name], { encoding: "utf8" });
    expect(ourOut).toBe(refOut);
    expect(refOut).toBe("5\n15\n100\n42\n1000\n");
  },
);

test(
  "object creation and field access run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    const name = "Obj";
    const src = [
      "public class Obj {",
      "  int x;",
      "  static int count;",
      "  void set(int v) { this.x = v; }",
      "  int get() { return x; }",
      "  static void bump() { count = count + 1; }",
      "  public static void main(String[] args) {",
      "    Obj o = new Obj();",
      "    o.set(42);",
      "    System.out.println(o.get());",
      "    o.x = 7;",
      "    System.out.println(o.x);",
      "    bump(); bump();",
      "    System.out.println(count);",
      "  }",
      "}",
    ].join("\n");
    const ref = mkdtempSync(join(tmpdir(), "emit-ref-"));
    writeFileSync(join(ref, `${name}.java`), src);
    execFileSync("javac", ["--release", "21", "-d", ref, join(ref, `${name}.java`)]);
    const refOut = execFileSync("java", ["-cp", ref, name], { encoding: "utf8" });

    const ours = mkdtempSync(join(tmpdir(), "emit-ours-"));
    writeFileSync(join(ours, `${name}.class`), emit(name, src));
    const ourOut = execFileSync("java", ["-cp", ours, name], { encoding: "utf8" });

    expect(ourOut).toBe(refOut);
    expect(refOut).toBe("42\n7\n2\n");
  },
);

test(
  "lambda expressions (invokedynamic / LambdaMetafactory) run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Lam",
      [
        "import java.util.function.Supplier;",
        "import java.util.function.Predicate;",
        "import java.util.function.Consumer;",
        "public class Lam {",
        "  public static void main(String[] a){",
        '    Runnable r = () -> System.out.println("ran"); r.run();', // no params, void
        '    Supplier<String> s = () -> "hello"; System.out.println(s.get());', // no params, returns String
        '    String msg = "cap"; Supplier<String> s2 = () -> msg + "!"; System.out.println(s2.get());', // captures msg
        "    Predicate<String> p = str -> str.isEmpty();", // one param, calls a method on it
        '    System.out.println(p.test("")); System.out.println(p.test("x"));',
        '    Consumer<String> c = x -> System.out.println("got:" + x); c.accept("z");', // void SAM with a param
        "  }",
        "}",
      ].join("\n"),
      "ran\nhello\ncap!\ntrue\nfalse\ngot:z\n",
    );
  },
);

test(
  "try/finally (return, catch, rethrow, ordering) runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Fy",
      [
        "public class Fy {",
        "  static StringBuilder log = new StringBuilder();",
        '  static int a(int n){ try { if(n<0) throw new RuntimeException(); return n*2; } finally { log.append("a"); } }', // return runs finally
        '  static int b(int n){ try { return n; } catch (RuntimeException e) { return -1; } finally { log.append("b"); } }',
        '  static int c(int n){ int r=0; try { r=10/n; } catch (ArithmeticException e) { r=-1; } finally { log.append("c"); r+=100; } return r; }',
        '  static String d(int n){ try { if(n==0) throw new RuntimeException("z"); return "ok"; } finally { log.append("d"); } }',
        "  public static void main(String[] x){",
        "    System.out.println(a(5));",
        "    System.out.println(b(7));",
        "    System.out.println(c(2)); System.out.println(c(0));",
        '    try { a(-1); } catch (RuntimeException e) { System.out.println("rethrown"); }', // exception runs finally, then rethrows
        "    System.out.println(d(3));",
        "    System.out.println(log.toString());",
        "  }",
        "}",
      ].join("\n"),
      "10\n7\n105\n99\nrethrown\nok\nabccad\n",
    );
  },
);

test(
  "try/catch (multi-catch, exception flow) runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Tc",
      [
        "public class Tc {",
        "  static int parse(String s){ try { return Integer.parseInt(s); } catch (NumberFormatException e) { return -1; } }",
        "  static String classify(int n){",
        '    try { if (n<0) throw new IllegalArgumentException("neg"); if (n==0) throw new RuntimeException("zero"); return "pos"; }',
        '    catch (IllegalArgumentException e) { return "iae:" + e.getMessage(); }', // method call on catch param
        '    catch (RuntimeException e) { return "rte:" + e.getMessage(); }', // second handler
        "  }",
        "  static int withFlow(int[] a, int i){ int r=0; try { r = a[i]; } catch (ArrayIndexOutOfBoundsException e) { r = -1; } return r + 1; }",
        "  public static void main(String[] x){",
        '    System.out.println(parse("42")); System.out.println(parse("zz"));',
        "    System.out.println(classify(5)); System.out.println(classify(-1)); System.out.println(classify(0));",
        "    System.out.println(withFlow(new int[]{7,8}, 1)); System.out.println(withFlow(new int[]{7}, 5));",
        "  }",
        "}",
      ].join("\n"),
      "42\n-1\npos\niae:neg\nrte:zero\n9\n0\n",
    );
  },
);

test(
  "throw statements run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Tw",
      [
        "public class Tw {",
        '  static int checked(int n){ if (n < 0) throw new IllegalArgumentException("neg"); return n * 2; }',
        "  static int half(int n){ if (n == 0) throw new RuntimeException(); return 100 / n; }",
        "  public static void main(String[] a){",
        "    System.out.println(checked(5));",
        "    System.out.println(half(4));",
        "  }",
        "}",
      ].join("\n"),
      "10\n25\n",
    );
  },
);

test(
  "explicit super(args)/this(args) constructor invocations run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Ctor",
      [
        "public class Ctor {",
        "  static class Base {",
        "    int b;",
        "    Base(int b){ this.b = b; }",
        "  }",
        "  static class Derived extends Base {",
        "    int d = 100;", // field init runs after super(), before the rest of the body
        "    Derived(int x){ super(x); d = d + x; }",
        "    Derived(){ this(7); }", // this(...) delegates; must not re-run the d initializer twice
        "    int sum(){ return b + d; }",
        "  }",
        "  public static void main(String[] a){",
        "    Derived p = new Derived(5);",
        "    System.out.println(p.b + \" \" + p.d + \" \" + p.sum());",
        "    Derived q = new Derived();",
        "    System.out.println(q.b + \" \" + q.d + \" \" + q.sum());",
        "  }",
        "}",
      ].join("\n"),
      "5 105 110\n7 107 114\n",
    );
  },
);

test(
  "instanceof type-pattern binding runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Iof",
      [
        "public class Iof {",
        "  static String describe(Object o){",
        '    if (o instanceof String s) { return "str:" + s.length(); }',
        '    if (o instanceof Integer i && i.intValue() > 0) { return "posint:" + i; }',
        '    return "other";',
        "  }",
        "  public static void main(String[] a){",
        '    System.out.println(describe("hello"));',
        "    System.out.println(describe(Integer.valueOf(42)));",
        "    System.out.println(describe(Integer.valueOf(-1)));",
        "    System.out.println(describe(new Object()));",
        "  }",
        "}",
      ].join("\n"),
      "str:5\nposint:42\nother\nother\n",
    );
  },
);

test(
  "assert statement: disabled by default, throws AssertionError under -ea",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    const src = [
      "public class Asrt {",
      "  static int checked(int n){ assert n > 0 : \"bad \" + n; return n; }",
      "  public static void main(String[] a){",
      "    try { System.out.println(checked(-1)); }",
      "    catch (AssertionError e) { System.out.println(\"caught \" + e.getMessage()); }",
      "  }",
      "}",
    ].join("\n");
    const dir = mkdtempSync(join(tmpdir(), "emit-assert-"));
    for (const c of emitClasses("Asrt", src)) writeFileSync(join(dir, `${c.name}.class`), c.bytes);
    // Assertions disabled (default): the assert is a no-op, checked(-1) returns -1.
    expect(execFileSync("java", ["-cp", dir, "Asrt"], { encoding: "utf8" })).toBe("-1\n");
    // Assertions enabled (-ea): the false assert throws AssertionError with the message.
    expect(execFileSync("java", ["-ea", "-cp", dir, "Asrt"], { encoding: "utf8" })).toBe(
      "caught bad -1\n",
    );
  },
);

test(
  "synchronized statement runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Sync",
      [
        "public class Sync {",
        "  static final Object lock = new Object();",
        "  static int counter;",
        "  static void inc(){ synchronized (lock) { counter = counter + 1; } }",
        "  static int withReturn(){ synchronized (lock) { return counter; } }",
        "  static int viaException(){",
        "    try { synchronized (lock) { throw new RuntimeException(); } }",
        "    catch (RuntimeException e) { return -1; }",
        "  }",
        "  public static void main(String[] a){",
        "    inc(); inc(); inc();",
        "    System.out.println(counter);",
        "    System.out.println(withReturn());",
        "    System.out.println(viaException());",
        "    System.out.println(Thread.holdsLock(lock));", // monitor released on every path
        "  }",
        "}",
      ].join("\n"),
      "3\n3\n-1\nfalse\n",
    );
  },
);

test(
  "try-with-resources runs identically to javac (order, return, suppression)",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Twr",
      [
        "public class Twr {",
        "  static class R implements AutoCloseable {",
        "    String n; boolean failClose;",
        '    R(String n){ this.n = n; System.out.println("open " + n); }',
        '    R(String n, boolean f){ this.n = n; this.failClose = f; System.out.println("open " + n); }',
        "    public void close(){",
        '      System.out.println("close " + n);',
        '      if (failClose) throw new RuntimeException("close-" + n);',
        "    }",
        "  }",
        "  static void normal(){",
        // references the resource variable in the body (r.n), exercising the
        // binder/checker binding of the resource.
        '    try (R r = new R("a")) { System.out.println("body " + r.n); }',
        "  }",
        "  static int withReturn(){",
        '    try (R r = new R("b")) { return 7; }',
        "  }",
        "  static void multi(){",
        '    try (R x = new R("x"); R y = new R("y")) { System.out.println("body"); }',
        "  }",
        // close() throws while the body is already throwing: the body exception is
        // the primary (caught), the close exception is suppressed.
        "  static void suppressed(){",
        "    try {",
        '      try (R r = new R("s", true)) { throw new IllegalStateException("body-boom"); }',
        "    } catch (Exception e) {",
        '      System.out.println("caught " + e.getMessage());',
        "    }",
        "  }",
        "  public static void main(String[] a){",
        "    normal();",
        '    System.out.println("ret " + withReturn());',
        "    multi();",
        "    suppressed();",
        "  }",
        "}",
      ].join("\n"),
      [
        "open a",
        "body a",
        "close a",
        "open b",
        "close b",
        "ret 7",
        "open x",
        "open y",
        "body",
        "close y",
        "close x",
        "open s",
        "close s",
        "caught body-boom",
        "",
      ].join("\n"),
    );
  },
);

test(
  "try-with-resources null resource skips close (JLS 14.20.3.1)",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "TwrNull",
      [
        "public class TwrNull {",
        "  static class R implements AutoCloseable {",
        '    public void close(){ System.out.println("close"); }',
        "  }",
        "  static R nothing(){ return null; }",
        "  public static void main(String[] a){",
        // resource is null: the body runs and close() must be guarded (not NPE).
        '    try (R r = nothing()) { System.out.println("body"); }',
        '    System.out.println("done");',
        "  }",
        "}",
      ].join("\n"),
      "body\ndone\n",
    );
  },
);

test(
  "try-with-resources variable-access form runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "TwrVar",
      [
        "public class TwrVar {",
        "  static class R implements AutoCloseable {",
        "    String n; R(String n){ this.n = n; }",
        '    public void close(){ System.out.println("close " + n); }',
        "  }",
        "  static void useExisting(){",
        '    R r = new R("v");',
        // SE9 variable-access form: the resource is an already-declared variable.
        '    try (r) { System.out.println("body"); }',
        "  }",
        "  public static void main(String[] a){ useExisting(); }",
        "}",
      ].join("\n"),
      "body\nclose v\n",
    );
  },
);

test(
  "labeled break and continue run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Lb",
      [
        "public class Lb {",
        "  static int firstPair(int[][] g, int t){",
        "    int found = -1;",
        "    outer:",
        "    for (int i = 0; i < g.length; i++) {",
        "      for (int j = 0; j < g[i].length; j++) {",
        "        if (g[i][j] == t) { found = i * 10 + j; break outer; }",
        "      }",
        "    }",
        "    return found;",
        "  }",
        "  static int skipRows(int n){",
        "    int sum = 0;",
        "    next:",
        "    for (int i = 0; i < n; i++) {",
        "      for (int j = 0; j < n; j++) {",
        "        if (j == 1) continue next;",
        "        sum += i * 100 + j;",
        "      }",
        "    }",
        "    return sum;",
        "  }",
        "  static int labeledBlock(int n){",
        "    int r = 0;",
        "    done:",
        "    {",
        "      r = 1;",
        "      if (n > 0) break done;",
        "      r = 2;",
        "    }",
        "    return r;",
        "  }",
        "  public static void main(String[] a){",
        "    int[][] grid = {{1,2,3},{4,5,6},{7,8,9}};",
        "    System.out.println(firstPair(grid, 5));",
        "    System.out.println(firstPair(grid, 42));",
        "    System.out.println(skipRows(3));",
        "    System.out.println(labeledBlock(1));",
        "    System.out.println(labeledBlock(-1));",
        "  }",
        "}",
      ].join("\n"),
      "11\n-1\n300\n1\n2\n",
    );
  },
);

test(
  "enum switch (statement and exhaustive expression) runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Es",
      [
        "public class Es {",
        "  enum Color { RED, GREEN, BLUE }",
        '  static String name(Color c){ switch (c) { case RED: return "r"; case GREEN: return "g"; default: return "?"; } }',
        "  static int code(Color c){ return switch (c) { case RED -> 1; case GREEN -> 2; case BLUE -> 3; }; }", // exhaustive, no default
        "  static int viaStmt(Color c){ int r=0; switch(c){ case RED: r=1; break; case BLUE: r=3; break; default: r=-1; } return r; }",
        "  public static void main(String[] a){",
        "    System.out.println(name(Color.RED)); System.out.println(name(Color.BLUE));",
        "    System.out.println(code(Color.GREEN)); System.out.println(code(Color.BLUE));",
        "    System.out.println(viaStmt(Color.RED)); System.out.println(viaStmt(Color.GREEN));",
        "  }",
        "}",
      ].join("\n"),
      "r\n?\n2\n3\n1\n-1\n",
    );
  },
);

test(
  "switch expressions (arrow, yield, block, string) run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Sx",
      [
        "public class Sx {",
        "  static int arrow(int n){ return switch(n){ case 1 -> 10; case 2,3 -> 20; default -> 0; }; }",
        '  static String yld(int n){ return switch(n){ case 0: yield "zero"; default: yield "many"; }; }', // colon + yield
        "  static int blk(int n){ return switch(n){ case 1 -> { int t = n*100; yield t+1; } default -> -1; }; }", // arrow block + yield
        '  static String strsw(String s){ return switch(s){ case "a" -> "A"; case "b" -> "B"; default -> "?"; }; }',
        "  public static void main(String[] a){",
        "    System.out.println(arrow(1)); System.out.println(arrow(3)); System.out.println(arrow(9));",
        "    System.out.println(yld(0)); System.out.println(yld(5));",
        "    System.out.println(blk(1)); System.out.println(blk(2));",
        '    System.out.println(strsw("b")); System.out.println(strsw("z"));',
        "  }",
        "}",
      ].join("\n"),
      "10\n20\n0\nzero\nmany\n101\n-1\nB\n?\n",
    );
  },
);

test(
  "for-each over a collection (Iterator) runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Fe",
      [
        "import java.util.ArrayList;",
        "import java.util.List;",
        "public class Fe {",
        "  static int total(List<Integer> xs){ int s=0; for(int v : xs) s+=v; return s; }", // unboxing element
        "  public static void main(String[] args){",
        "    List<String> names = new ArrayList<String>();",
        '    names.add("alice"); names.add("bob");', // inherited Collection.add(E), not List.add(int,E)
        "    for (String n : names) System.out.println(n);",
        "    List<Integer> nums = new ArrayList<Integer>();",
        "    nums.add(10); nums.add(20); nums.add(12);", // autoboxed argument
        "    System.out.println(total(nums));",
        "    for (Object o : names) System.out.println(o);",
        "  }",
        "}",
      ].join("\n"),
      "alice\nbob\n42\nalice\nbob\n",
    );
  },
);

test(
  "arrays (creation, access, length, store, foreach, multidim) run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Arr",
      [
        "public class Arr {",
        "  static int sum(int[] a){ int s=0; for(int i=0;i<a.length;i++) s+=a[i]; return s; }",
        "  static int sumEach(int[] a){ int s=0; for(int x : a) s+=x; return s; }",
        "  public static void main(String[] args){",
        "    int[] a = new int[]{3,4,5};",
        "    System.out.println(a.length);",
        "    System.out.println(a[1]);",
        "    a[1] = 40; System.out.println(a[1]);",
        "    a[2] += 100; System.out.println(a[2]);", // compound store
        "    a[0]++; System.out.println(a[0]);", // element increment
        "    System.out.println(sum(a));",
        "    System.out.println(sumEach(a));",
        '    int[] b = new int[3]; b[0]=7; System.out.println(b[0] + " " + b[2]);', // sized + default
        '    String[] s = {"x","y"}; System.out.println(s[0] + s[1] + " " + s.length);', // bare init, ref array
        "    int[][] m = new int[2][3]; m[1][2] = 9;", // multidim
        '    System.out.println(m[1][2] + " " + m.length + " " + m[0].length);',
        "  }",
        "}",
      ].join("\n"),
      "3\n4\n40\n105\n4\n149\n149\n7 0\nxy 2\n9 2 3\n",
    );
  },
);

test(
  "enum declarations run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "En",
      [
        "public class En {",
        "  enum Color { RED, GREEN, BLUE }",
        "  enum Planet {",
        "    EARTH(5.976e24), MARS(6.421e23);",
        "    private final double mass;",
        "    Planet(double mass){ this.mass = mass; }",
        "    double getMass(){ return mass; }",
        "  }",
        "  public static void main(String[] a){",
        '    System.out.println(Color.RED.name() + " " + Color.RED.ordinal());',
        "    System.out.println(Color.BLUE.ordinal());",
        '    System.out.println(Color.valueOf("GREEN").name());', // synthesized valueOf + inherited name()
        "    System.out.println(Planet.EARTH.getMass());", // constant with constructor arg
        "    System.out.println(Planet.MARS.name());",
        "    System.out.println(Color.RED == Color.RED);", // identity
        "    System.out.println(Color.RED == Color.BLUE);",
        "  }",
        "}",
      ].join("\n"),
      "RED 0\n2\nGREEN\n5.976E24\nMARS\ntrue\nfalse\n",
    );
  },
);

test(
  "autoboxing and unboxing run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Box",
      [
        "import java.util.function.Supplier;",
        "public class Box {",
        "  static int unboxAdd(Integer a, int b){ return a + b; }", // unbox a
        "  static Integer boxRet(int x){ return x; }", // box return value
        "  static double widenUnbox(Integer a){ return a + 0.5; }", // Integer -> int -> double
        "  static boolean cmp(Integer a, int b){ return a < b; }", // unbox in a relational
        "  static boolean eqMixed(Integer a, int b){ return a == b; }", // unbox in == (one primitive)
        "  static Supplier<Integer> sup(int base){ return () -> base + 1; }", // box the lambda body result
        "  static String show(Object o){ return o.toString(); }", // box int -> Object argument
        "  public static void main(String[] a){",
        "    System.out.println(unboxAdd(40, 2));",
        "    Integer r = boxRet(7); System.out.println(r);",
        "    System.out.println(widenUnbox(3));",
        "    System.out.println(cmp(3, 5)); System.out.println(cmp(9, 5));",
        "    System.out.println(eqMixed(5, 5)); System.out.println(eqMixed(5, 6));",
        "    System.out.println(sup(10).get());",
        "    System.out.println(show(42));",
        "  }",
        "}",
      ].join("\n"),
      "42\n7\n3.5\ntrue\nfalse\ntrue\nfalse\n11\n42\n",
    );
  },
);

test(
  "method references (static/bound/unbound/constructor) run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Mr",
      [
        "import java.util.function.Supplier;",
        "import java.util.function.Predicate;",
        "import java.util.function.Function;",
        "import java.util.function.Consumer;",
        "public class Mr {",
        "  public static void main(String[] a){",
        "    Predicate<String> empty = String::isEmpty;", // unbound instance
        '    System.out.println(empty.test("")); System.out.println(empty.test("x"));',
        "    Consumer<String> pr = System.out::println;", // bound instance
        '    pr.accept("bound!");',
        "    Function<String,Integer> len = String::length;", // unbound; int->Integer adapted by the metafactory
        '    System.out.println(len.apply("hello"));',
        "    Supplier<Mr> ctor = Mr::new;", // constructor reference
        "    System.out.println(ctor.get() != null);",
        "  }",
        "}",
      ].join("\n"),
      "true\nfalse\nbound!\n5\ntrue\n",
    );
  },
);

test(
  "this-capturing lambdas (instance context) run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Th",
      [
        "import java.util.function.Supplier;",
        "public class Th {",
        '  String label = "L";',
        "  String suffix(String s){ return label + s; }",
        '  Supplier<String> labeler(){ return () -> label + "!"; }', // captures this (field)
        "  Supplier<String> withLocal(String x){ return () -> label + x; }", // this + local
        "  Supplier<String> viaMethod(String y){ return () -> suffix(y); }", // this (instance method) + local
        "  public static void main(String[] a){",
        "    Th t = new Th();",
        "    System.out.println(t.labeler().get());",
        '    System.out.println(t.withLocal("X").get());',
        '    System.out.println(t.viaMethod("Y").get());',
        "  }",
        "}",
      ].join("\n"),
      "L!\nLX\nLY\n",
    );
  },
);

test(
  "static nested classes and field ++ run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Nest",
      [
        "public class Nest {",
        "  static class Point { int x, y; Point(int x, int y){ this.x=x; this.y=y; } int sum(){ return x+y; } }",
        "  static class Counter { static int total; int n; void tick(){ n++; total++; } int get(){ return n; } }",
        "  static int helper(int a){ return a*2; }",
        "  public static void main(String[] a){",
        "    Point p = new Point(3,4); System.out.println(p.sum());",
        "    Counter c = new Counter(); c.tick(); c.tick();",
        "    System.out.println(c.get()); System.out.println(Counter.total);",
        "    System.out.println(helper(21));",
        "  }",
        "}",
      ].join("\n"),
      "7\n2\n2\n42\n",
    );
  },
);

test(
  "definite assignment: uninitialized locals across branches verify like javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Da",
      [
        "public class Da {",
        "  static int ifElse(int c){ int r; if(c>0){ r=1; } else { r=2; } return r; }",
        "  static int viaLoop(int n){ int r; if(n<0){ r=-1; } else { r=0; for(int i=0;i<n;i++){ r=r+i; } } return r; }",
        "  public static void main(String[] a){",
        "    System.out.println(ifElse(5)); System.out.println(ifElse(-3));",
        "    System.out.println(viaLoop(4)); System.out.println(viaLoop(-1));",
        "  }",
        "}",
      ].join("\n"),
      "1\n2\n6\n-1\n",
    );
  },
);

test(
  "arrow and string switch run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Sw2",
      [
        "public class Sw2 {",
        "  static int arrowI(int n){ int r; switch(n){ case 1 -> r=10; case 2,3 -> r=20; default -> r=99; } return r; }",
        '  static String arrowStr(int n){ switch(n){ case 0 -> { return "zero"; } case 1 -> { return "one"; } default -> { return "many"; } } }',
        '  static String colonStr(String s){ switch(s){ case "a": return "A"; case "b": case "c": return "BC"; default: return "?"; } }',
        '  static String arrowStrSel(String s){ switch(s){ case "x" -> { return "X"; } default -> { return "Z"; } } }',
        "  public static void main(String[] a){",
        "    System.out.println(arrowI(1)); System.out.println(arrowI(3)); System.out.println(arrowI(7));",
        "    System.out.println(arrowStr(0)); System.out.println(arrowStr(5));",
        '    System.out.println(colonStr("a")); System.out.println(colonStr("c")); System.out.println(colonStr("z"));',
        '    System.out.println(arrowStrSel("x")); System.out.println(arrowStrSel("q"));',
        "  }",
        "}",
      ].join("\n"),
      "10\n20\n99\nzero\nmany\nA\nBC\n?\nX\nZ\n",
    );
  },
);

test(
  "compound assignment (+=, bitwise, narrowing, fields, string) runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Ca",
      [
        "public class Ca {",
        "  static int f; int g;",
        "  static int locals(int n){ int s=0; for(int i=0;i<n;i++){ s+=i; s*=2; s-=1; } return s; }",
        "  static int bits(int x){ x<<=2; x|=1; x^=3; x&=0xFE; x>>=1; return x; }",
        "  static int narrow(){ byte b=10; b+=300; return b; }", // implicit narrowing back to byte
        "  static double dbl(double d, int i){ d+=i; d*=1.5; return d; }",
        "  static int idivd(int i){ i+=2.7; return i; }", // int += double -> d2i narrowing
        "  static int statics(int n){ f=5; f+=n; return f; }",
        "  int inst(int n){ g=1; g+=n; g*=3; return g; }",
        '  static String str(){ String s="a"; s+="b"; s+=1; s+=true; return s; }',
        "  public static void main(String[] a){",
        "    System.out.println(locals(4)); System.out.println(bits(255)); System.out.println(narrow());",
        "    System.out.println(dbl(2.0,3)); System.out.println(idivd(5)); System.out.println(statics(10));",
        "    System.out.println(new Ca().inst(4)); System.out.println(str());",
        "  }",
        "}",
      ].join("\n"),
      "7\n127\n54\n7.5\n7\n15\n15\nab1true\n",
    );
  },
);

test(
  "break and continue in loops run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Lp",
      [
        "public class Lp {",
        "  static int sumEven(int n){ int s=0; for(int i=0;i<n;i++){ if(i%2==1) continue; s=s+i; } return s; }",
        "  static int firstGt(int n, int t){ int r=-1; for(int i=0;i<n;i++){ if(i>t){ r=i; break; } } return r; }",
        "  static int whileBreak(int n){ int c=0; while(true){ c=c+1; if(c>=n) break; } return c; }",
        "  static int doCont(int n){ int s=0,i=0; do { i=i+1; if(i==3) continue; s=s+i; } while(i<n); return s; }",
        "  static int nested(int n){ int c=0; for(int i=0;i<n;i++){ for(int j=0;j<n;j++){ if(j==2) break; c=c+1; } } return c; }",
        "  public static void main(String[] a){",
        "    System.out.println(sumEven(6)); System.out.println(firstGt(10,4));",
        "    System.out.println(whileBreak(5)); System.out.println(doCont(5)); System.out.println(nested(4));",
        "  }",
        "}",
      ].join("\n"),
      "6\n5\n5\n12\n8\n",
    );
  },
);

test(
  "switch statements (table + lookup, fall-through, break) run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    const name = "Sw";
    const src = [
      "public class Sw {",
      "  static String day(int n) {",
      "    switch (n) {",
      '      case 1: return "Mon";',
      '      case 2: return "Tue";',
      '      case 3: case 4: return "midweek";', // shared labels
      '      case 7: return "Sun";',
      '      default: return "other";',
      "    }",
      "  }",
      "  static int classify(int x) {",
      "    int r = 0;",
      "    switch (x) {",
      "      case 0: r = 100; break;",
      "      case 10: r = 200;", // falls through to case 11
      "      case 11: r = r + 5; break;",
      "      default: r = -1;",
      "    }",
      "    return r;",
      "  }",
      "  static int sparse(int x) {", // sparse -> lookupswitch
      "    switch (x) { case 1: return 1; case 1000: return 2; case 1000000: return 3; default: return 0; }",
      "  }",
      "  public static void main(String[] args) {",
      "    System.out.println(day(1)); System.out.println(day(3)); System.out.println(day(4));",
      "    System.out.println(day(7)); System.out.println(day(9));",
      "    System.out.println(classify(0)); System.out.println(classify(10));",
      "    System.out.println(classify(11)); System.out.println(classify(99));",
      "    System.out.println(sparse(1)); System.out.println(sparse(1000));",
      "    System.out.println(sparse(1000000)); System.out.println(sparse(5));",
      "  }",
      "}",
    ].join("\n");
    const ref = mkdtempSync(join(tmpdir(), "emit-ref-"));
    writeFileSync(join(ref, `${name}.java`), src);
    execFileSync("javac", ["--release", "21", "-d", ref, join(ref, `${name}.java`)]);
    const refOut = execFileSync("java", ["-cp", ref, name], { encoding: "utf8" });

    const ours = mkdtempSync(join(tmpdir(), "emit-ours-"));
    writeFileSync(join(ours, `${name}.class`), emit(name, src));
    const ourOut = execFileSync("java", ["-cp", ours, name], { encoding: "utf8" });

    expect(ourOut).toBe(refOut);
    expect(refOut).toBe("Mon\nmidweek\nmidweek\nSun\nother\n100\n205\n5\n-1\n1\n2\n3\n0\n");
  },
);

test(
  "float and double arithmetic run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    const name = "Fp";
    const src = [
      "public class Fp {",
      "  static double area(double r) { return 3.14159 * r * r; }",
      "  static boolean closeEnough(double a, double b) { double diff = a - b; if (diff < 0.0) { diff = -diff; } return diff < 0.001; }",
      "  public static void main(String[] args) {",
      "    double d = 2.5;",
      "    float f = 1.5f;",
      "    double sum = d + f;",
      "    System.out.println(sum);",
      "    System.out.println(area(2.0));",
      "    System.out.println(d > f);",
      "    System.out.println(closeEnough(1.0, 1.0005));",
      "    int n = (int) 9.99;",
      "    System.out.println(n);",
      "  }",
      "}",
    ].join("\n");
    const ref = mkdtempSync(join(tmpdir(), "emit-ref-"));
    writeFileSync(join(ref, `${name}.java`), src);
    execFileSync("javac", ["--release", "21", "-d", ref, join(ref, `${name}.java`)]);
    const refOut = execFileSync("java", ["-cp", ref, name], { encoding: "utf8" });

    const ours = mkdtempSync(join(tmpdir(), "emit-ours-"));
    writeFileSync(join(ours, `${name}.class`), emit(name, src));
    const ourOut = execFileSync("java", ["-cp", ours, name], { encoding: "utf8" });

    expect(ourOut).toBe(refOut);
    expect(refOut).toBe("4.0\n12.56636\ntrue\ntrue\n9\n");
  },
);

test(
  "conditional (ternary) expressions run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    const name = "Tern";
    const src = [
      "public class Tern {",
      "  static int max(int a, int b) { return a > b ? a : b; }",
      "  static double pick(boolean f, int i, double d) { return f ? i : d; }", // int arm promoted to double
      '  static String sign(int n) { return n < 0 ? "neg" : n == 0 ? "zero" : "pos"; }', // nested
      "  static int abs(int n) { return n < 0 ? -n : n; }",
      "  public static void main(String[] args) {",
      "    System.out.println(max(3, 7));",
      "    System.out.println(pick(true, 5, 2.5));",
      "    System.out.println(pick(false, 5, 2.5));",
      "    System.out.println(sign(-4));",
      "    System.out.println(sign(0));",
      "    System.out.println(sign(9));",
      "    System.out.println(abs(-8));",
      "  }",
      "}",
    ].join("\n");
    const ref = mkdtempSync(join(tmpdir(), "emit-ref-"));
    writeFileSync(join(ref, `${name}.java`), src);
    execFileSync("javac", ["--release", "21", "-d", ref, join(ref, `${name}.java`)]);
    const refOut = execFileSync("java", ["-cp", ref, name], { encoding: "utf8" });

    const ours = mkdtempSync(join(tmpdir(), "emit-ours-"));
    writeFileSync(join(ours, `${name}.class`), emit(name, src));
    const ourOut = execFileSync("java", ["-cp", ours, name], { encoding: "utf8" });

    expect(ourOut).toBe(refOut);
    expect(refOut).toBe("7\n5.0\n2.5\nneg\nzero\npos\n8\n");
  },
);

for (const [name, { source: src, stdout }] of Object.entries(CONTROL)) {
  test(`control flow binary baseline: ${name}`, () => {
    const bytes = emit(name, src);
    const baseline = join(baselinesDir, `${name}.class`);
    if (shouldUpdate || !existsSync(baseline)) {
      mkdirSync(baselinesDir, { recursive: true });
      writeFileSync(baseline, bytes);
    }
    expect(Buffer.from(bytes).equals(readFileSync(baseline))).toBe(true);
  });

  test(`control flow verifies and runs: ${name}`, { skip: HAS_JAVA ? false : "no JDK" }, () => {
    const dir = mkdtempSync(join(tmpdir(), "emit-cf-"));
    writeFileSync(join(dir, `${name}.class`), emit(name, src));
    // The JVM verifier checks our StackMapTable on load; a wrong frame -> VerifyError.
    const out = execFileSync("java", ["-cp", dir, name], { encoding: "utf8" });
    expect(out).toBe(stdout);
  });
}

// Test cases extracted from the corpus projects (algorithms-java, commons-lang,
// json-java) exercising constructs the targeted suite did not cover in isolation.
test(
  "bit manipulation runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Bits",
      [
        "public class Bits {",
        "  static int setBit(int x, int i){ return x | (1 << i); }",
        "  static int clearBit(int x, int i){ return x & ~(1 << i); }",
        "  static boolean isSet(int x, int i){ return (x & (1 << i)) != 0; }",
        "  static int countOnes(int x){ int c = 0; while (x != 0) { c += x & 1; x >>>= 1; } return c; }",
        "  static long mix(long a, int s){ return (a << s) ^ (a >>> s) | (a & 0xFFL); }",
        "  public static void main(String[] a){",
        "    System.out.println(setBit(0, 3));",
        "    System.out.println(clearBit(15, 1));",
        "    System.out.println(isSet(8, 3));",
        "    System.out.println(countOnes(-1));",
        "    System.out.println(mix(123456789L, 5));",
        "  }",
        "}",
      ].join("\n"),
      "8\n13\ntrue\n32\n3947068637\n",
    );
  },
);

test(
  "multidimensional arrays run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Mat",
      [
        "public class Mat {",
        "  static int rectSum(int n, int m){",
        "    int[][] g = new int[n][m];",
        "    for (int i = 0; i < n; i++) for (int j = 0; j < m; j++) g[i][j] = i * m + j;",
        "    int s = 0;",
        "    for (int i = 0; i < n; i++) for (int j = 0; j < m; j++) s += g[i][j];",
        "    return s;",
        "  }",
        "  static int jagged(){",
        "    int[][] t = new int[3][];",
        "    for (int i = 0; i < 3; i++) { t[i] = new int[i + 1]; for (int j = 0; j <= i; j++) t[i][j] = j; }",
        "    int s = 0;",
        "    for (int[] row : t) for (int v : row) s += v;",
        "    return s;",
        "  }",
        "  public static void main(String[] a){",
        "    System.out.println(rectSum(3, 4));",
        "    System.out.println(jagged());",
        "  }",
        "}",
      ].join("\n"),
      "66\n4\n",
    );
  },
);

test(
  "array element compound assignment runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "ArrAssign",
      [
        "public class ArrAssign {",
        "  public static void main(String[] a){",
        "    int[] x = {1, 2, 3, 4};",
        "    x[0] += 10;",
        "    x[1] *= 5;",
        "    x[2]++;",
        "    x[3] <<= 2;",
        "    int s = 0;",
        "    for (int v : x) s = s * 100 + v;",
        "    System.out.println(s);",
        "  }",
        "}",
      ].join("\n"),
      "11100416\n",
    );
  },
);

test(
  "char and digit arithmetic runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Chars",
      [
        "public class Chars {",
        "  static int parse(String s){",
        "    int n = 0;",
        "    for (int i = 0; i < s.length(); i++) { char c = s.charAt(i); n = n * 10 + (c - '0'); }",
        "    return n;",
        "  }",
        "  static String shift(String s){",
        "    char[] out = new char[s.length()];",
        "    for (int i = 0; i < s.length(); i++) out[i] = (char) (s.charAt(i) + 1);",
        "    return new String(out);",
        "  }",
        "  public static void main(String[] a){",
        '    System.out.println(parse("2026"));',
        '    System.out.println(shift("abc"));',
        "  }",
        "}",
      ].join("\n"),
      "2026\nbcd\n",
    );
  },
);

test(
  "recursion runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Rec",
      [
        "public class Rec {",
        "  static long fact(int n){ return n <= 1 ? 1L : n * fact(n - 1); }",
        "  static int fib(int n){ return n < 2 ? n : fib(n - 1) + fib(n - 2); }",
        "  static boolean even(int n){ return n == 0 ? true : odd(n - 1); }",
        "  static boolean odd(int n){ return n == 0 ? false : even(n - 1); }",
        "  public static void main(String[] a){",
        "    System.out.println(fact(10));",
        "    System.out.println(fib(15));",
        "    System.out.println(even(10));",
        "    System.out.println(odd(7));",
        "  }",
        "}",
      ].join("\n"),
      "3628800\n610\ntrue\ntrue\n",
    );
  },
);

test(
  "do-while with break and continue runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "DoW",
      [
        "public class DoW {",
        "  static int run(int n){",
        "    int i = 0, s = 0;",
        "    do {",
        "      i++;",
        "      if (i % 2 == 0) continue;",
        "      if (i > n) break;",
        "      s += i;",
        "    } while (i < 100);",
        "    return s;",
        "  }",
        "  public static void main(String[] a){ System.out.println(run(9)); }",
        "}",
      ].join("\n"),
      "25\n",
    );
  },
);

test(
  "common JDK library calls run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Lib",
      [
        "public class Lib {",
        "  public static void main(String[] a){",
        "    System.out.println(Math.max(3L, 7L) + Math.min(2L, 9L));",
        "    System.out.println(Integer.toHexString(255));",
        "    System.out.println(Integer.bitCount(255));",
        "    System.out.println(Long.toHexString(4096L));",
        "    System.out.println(Character.getNumericValue('7'));",
        "    int[] src = {1, 2, 3, 4, 5};",
        "    int[] dst = new int[5];",
        "    System.arraycopy(src, 1, dst, 0, 3);",
        "    StringBuilder sb = new StringBuilder();",
        '    sb.append("x=").append(42).append(",").append(3.5).append(",").append(99L);',
        "    System.out.println(sb.toString());",
        "    System.out.println(dst[0] + dst[1] + dst[2]);",
        "    System.out.println(Integer.MAX_VALUE);",
        "  }",
        "}",
      ].join("\n"),
      "9\nff\n8\n1000\n7\nx=42,3.5,99\n9\n2147483647\n",
    );
  },
);

test(
  "array constructor references (T[]::new) run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "ArrNew",
      [
        "import java.util.function.IntFunction;",
        "public class ArrNew {",
        "  public static void main(String[] a){",
        "    IntFunction<int[]> f = int[]::new;",
        "    int[] x = f.apply(5);",
        "    System.out.println(x.length);",
        "    IntFunction<String[]> g = String[]::new;",
        "    String[] s = g.apply(3);",
        '    System.out.println(s.length + " " + (s[0] == null));',
        "  }",
        "}",
      ].join("\n"),
      "5\n3 true\n",
    );
  },
);

test(
  "Arrays utility calls on primitive arrays run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "ArrUtil",
      [
        "import java.util.Arrays;",
        "public class ArrUtil {",
        "  public static void main(String[] a){",
        "    int[] x = {5, 3, 1, 4, 2};",
        "    Arrays.sort(x);",
        "    System.out.println(Arrays.toString(x));",
        "    System.out.println(Arrays.binarySearch(x, 4));",
        "    int[] y = Arrays.copyOf(x, 3);",
        "    System.out.println(Arrays.toString(y));",
        "    int[] z = new int[3];",
        "    Arrays.fill(z, 7);",
        "    System.out.println(Arrays.toString(z));",
        "  }",
        "}",
      ].join("\n"),
      "[1, 2, 3, 4, 5]\n3\n[1, 2, 3]\n[7, 7, 7]\n",
    );
  },
);

test(
  "local classes (no capture) run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Local",
      [
        "public class Local {",
        "  static int counter(){",
        "    class Counter { int n; int inc(){ n = n + 1; return n; } }",
        "    Counter c = new Counter();",
        "    c.inc();",
        "    return c.inc();",
        "  }",
        "  static int viaInterface(int v){",
        "    class Doubler implements java.util.function.IntUnaryOperator {",
        "      public int applyAsInt(int x){ return x * 2; }",
        "    }",
        "    java.util.function.IntUnaryOperator op = new Doubler();",
        "    return op.applyAsInt(v);",
        "  }",
        "  public static void main(String[] a){",
        "    System.out.println(counter());",
        "    System.out.println(viaInterface(21));",
        "  }",
        "}",
      ].join("\n"),
      "2\n42\n",
    );
  },
);

test(
  "user-defined interfaces (abstract + default methods, constants) run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Iface",
      [
        "public class Iface {",
        "  interface Shape {",
        "    int SIDES = 4;",
        "    int area();",
        '    default String describe(){ return "area=" + area(); }',
        "  }",
        "  static class Square implements Shape {",
        "    int s; Square(int s){ this.s = s; }",
        "    public int area(){ return s * s; }",
        "  }",
        "  public static void main(String[] a){",
        "    Shape sh = new Square(3);",
        "    System.out.println(sh.area());",
        "    System.out.println(sh.describe());",
        "    System.out.println(Shape.SIDES);",
        "  }",
        "}",
      ].join("\n"),
      "9\narea=9\n4\n",
    );
  },
);

test(
  "local classes capturing enclosing locals run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Cap",
      [
        "public class Cap {",
        "  static int adder(int base){",
        "    int bonus = 100;",
        "    class Adder { int add(int x){ return base + x + bonus; } }",
        "    Adder a = new Adder();",
        "    return a.add(10);",
        "  }",
        "  static int viaOp(int factor){",
        "    class Mul implements java.util.function.IntUnaryOperator {",
        "      public int applyAsInt(int x){ return x * factor; }",
        "    }",
        "    java.util.function.IntUnaryOperator op = new Mul();",
        "    return op.applyAsInt(7);",
        "  }",
        "  public static void main(String[] z){",
        "    System.out.println(adder(5));",
        "    System.out.println(viaOp(3));",
        "  }",
        "}",
      ].join("\n"),
      "115\n21\n",
    );
  },
);

test(
  "anonymous interface classes (with capture) run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Anon",
      [
        "public class Anon {",
        "  static int adder(int base){",
        "    java.util.function.IntUnaryOperator op = new java.util.function.IntUnaryOperator(){",
        "      public int applyAsInt(int x){ return x + base; }",
        "    };",
        "    return op.applyAsInt(10);",
        "  }",
        "  static String greet(){",
        '    Runnable r = new Runnable(){ public void run(){ System.out.println("hi"); } };',
        "    r.run();",
        '    return "done";',
        "  }",
        "  public static void main(String[] a){",
        "    System.out.println(adder(5));",
        "    System.out.println(greet());",
        "  }",
        "}",
      ].join("\n"),
      "15\nhi\ndone\n",
    );
  },
);

test(
  "anonymous class extending a class (super args + capture) runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "AnonExt",
      [
        "public class AnonExt {",
        "  static class Base {",
        '    Base(int v){ System.out.println("base " + v); }',
        "    int get(){ return 0; }",
        "  }",
        "  static int run(int cap){",
        "    Base b = new Base(99){ public int get(){ return cap + 1; } };",
        "    return b.get();",
        "  }",
        "  public static void main(String[] a){ System.out.println(run(5)); }",
        "}",
      ].join("\n"),
      "base 99\n6\n",
    );
  },
);

test(
  "anonymous class accessing the enclosing instance (this$0) runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Outer",
      [
        "public class Outer {",
        "  int field = 10;",
        "  int outerMethod(){ return 5; }",
        "  Runnable makeRunnable(){",
        "    return new Runnable(){ public void run(){ System.out.println(field + outerMethod()); } };",
        "  }",
        "  public static void main(String[] a){ new Outer().makeRunnable().run(); }",
        "}",
      ].join("\n"),
      "15\n",
    );
  },
);

test(
  "local class accessing enclosing instance and a captured local runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "LThis0",
      [
        "public class LThis0 {",
        "  int base = 100;",
        "  int helper(){ return 7; }",
        "  int run(int p){",
        "    class Calc { int compute(){ return base + helper() + p; } }",
        "    return new Calc().compute();",
        "  }",
        "  public static void main(String[] a){ System.out.println(new LThis0().run(5)); }",
        "}",
      ].join("\n"),
      "112\n",
    );
  },
);

test(
  "pattern switch (type patterns, guard, null, default) runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "PSw",
      [
        "public class PSw {",
        "  static String describe(Object o){",
        "    return switch (o) {",
        '      case String s -> "str:" + s.length();',
        '      case Integer i when i > 0 -> "pos:" + i;',
        '      case Integer i -> "int:" + i;',
        '      case null -> "null";',
        '      default -> "other";',
        "    };",
        "  }",
        "  static int classify(Object o){",
        "    int r;",
        "    switch (o) {",
        "      case String s -> r = s.length();",
        "      case Integer i -> r = i;",
        "      default -> r = -1;",
        "    }",
        "    return r;",
        "  }",
        "  public static void main(String[] a){",
        '    System.out.println(describe("hi"));',
        "    System.out.println(describe(42));",
        "    System.out.println(describe(-1));",
        "    System.out.println(describe(null));",
        "    System.out.println(describe(3.5));",
        '    System.out.println(classify("abc") + "," + classify(7) + "," + classify(3.5));',
        "  }",
        "}",
      ].join("\n"),
      "str:2\npos:42\nint:-1\nnull\nother\n3,7,-1\n",
    );
  },
);

test(
  "private cross-nest access (nestmates) runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Nestmate",
      [
        "public class Nestmate {",
        "  private int secret = 42;",
        "  private int hidden(){ return 8; }",
        "  Runnable r(){",
        "    return new Runnable(){ public void run(){ System.out.println(secret + hidden()); } };",
        "  }",
        "  static class Helper { static int peek(Nestmate n){ return n.secret; } }",
        "  public static void main(String[] a){",
        "    Nestmate n = new Nestmate();",
        "    n.r().run();",
        "    System.out.println(Helper.peek(n));",
        "  }",
        "}",
      ].join("\n"),
      "50\n42\n",
    );
  },
);

test(
  "record declarations run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "RecMain",
      [
        "record Point(int x, int y) {",
        "  int sum(){ return x + y; }",
        "}",
        "public class RecMain {",
        "  public static void main(String[] a){",
        "    Point p = new Point(3, 4);",
        '    System.out.println(p.x() + "," + p.y());',
        "    System.out.println(p);",
        "    System.out.println(p.sum());",
        "    System.out.println(p.equals(new Point(3, 4)));",
        "    System.out.println(p.equals(new Point(3, 5)));",
        "    System.out.println(p.hashCode() == new Point(3, 4).hashCode());",
        "  }",
        "}",
      ].join("\n"),
      "3,4\nPoint[x=3, y=4]\n7\ntrue\nfalse\ntrue\n",
    );
  },
);

test(
  "record deconstruction patterns in switch run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "RecPat",
      [
        "record Point(int x, int y) {}",
        "record Line(Point from, Point to) {}",
        "public class RecPat {",
        "  static String f(Object o) {",
        "    return switch (o) {",
        "      case Line(Point(int x1, int y1), Point(int x2, int y2)) -> \"line:\" + (x1+y1+x2+y2);",
        "      case Point(int x, int y) when x == y -> \"diag:\" + x;",
        "      case Point(int x, int y) -> \"pt:\" + (x+y);",
        "      case String s -> \"str:\" + s;",
        "      default -> \"other\";",
        "    };",
        "  }",
        "  public static void main(String[] a){",
        "    System.out.println(f(new Line(new Point(1,2), new Point(3,4))));",
        "    System.out.println(f(new Point(5,5)));",
        "    System.out.println(f(new Point(2,3)));",
        '    System.out.println(f("hi"));',
        "    System.out.println(f(42));",
        "  }",
        "}",
      ].join("\n"),
      "line:10\ndiag:5\npt:5\nstr:hi\nother\n",
    );
  },
);

test(
  "instanceof record deconstruction runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "InstRec",
      [
        "record Point(int x, int y) {}",
        "public class InstRec {",
        "  static int f(Object o) {",
        "    if (o instanceof Point(int x, int y)) return x + y;",
        "    return -1;",
        "  }",
        "  public static void main(String[] a){",
        "    System.out.println(f(new Point(3,4)));",
        '    System.out.println(f("nope"));',
        "  }",
        "}",
      ].join("\n"),
      "7\n-1\n",
    );
  },
);

test(
  "colon-form pattern switch runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "ColonPat",
      [
        "public class ColonPat {",
        "  static String f(Object o) {",
        "    String r;",
        "    switch (o) {",
        '      case Integer i: r = "int:" + i; break;',
        '      case String s: r = "str:" + s; break;',
        '      default: r = "other"; break;',
        "    }",
        "    return r;",
        "  }",
        "  static int g(Object o) {",
        "    return switch (o) {",
        "      case Integer i: yield i * 2;",
        "      case String s: yield s.length();",
        "      default: yield -1;",
        "    };",
        "  }",
        "  public static void main(String[] a){",
        "    System.out.println(f(42));",
        '    System.out.println(f("hi"));',
        "    System.out.println(f(3.5));",
        "    System.out.println(g(21));",
        '    System.out.println(g("abcd"));',
        "    System.out.println(g(2.0));",
        "  }",
        "}",
      ].join("\n"),
      "int:42\nstr:hi\nother\n42\n4\n-1\n",
    );
  },
);

test(
  "anonymous class with own field initializers runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "AnonOwnField",
      [
        "interface Sup { int get(); }",
        "public class AnonOwnField {",
        "  int base = 100;",
        "  Sup make(int k) {",
        "    int local = 5;",
        "    return new Sup() {",
        "      int v = k + local + base;",
        "      public int get() { return v; }",
        "    };",
        "  }",
        "  public static void main(String[] a){",
        "    System.out.println(new AnonOwnField().make(7).get());",
        "  }",
        "}",
      ].join("\n"),
      "112\n",
    );
  },
);

test(
  "local class with own field initializer and capture runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "LocalField",
      [
        "public class LocalField {",
        "  int run(int k) {",
        "    int local = 3;",
        "    class C { int v = k + local; int get(){ return v + k; } }",
        "    return new C().get();",
        "  }",
        "  public static void main(String[] a){ System.out.println(new LocalField().run(10)); }",
        "}",
      ].join("\n"),
      "23\n",
    );
  },
);

test(
  "anonymous class writing its own field runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "AnonWrite",
      [
        "interface Counter { int next(); }",
        "public class AnonWrite {",
        "  static Counter make() {",
        "    return new Counter() {",
        "      int n = 0;",
        "      public int next() { n = n + 1; return n; }",
        "    };",
        "  }",
        "  public static void main(String[] a){",
        "    Counter c = make();",
        "    System.out.println(c.next());",
        "    System.out.println(c.next());",
        "    System.out.println(c.next());",
        "  }",
        "}",
      ].join("\n"),
      "1\n2\n3\n",
    );
  },
);

test(
  "instance and static initializer blocks run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "InitBlocks",
      [
        "public class InitBlocks {",
        "  static int s;",
        "  static { s = 7; }",
        "  int a;",
        "  int b = 1;",
        "  { a = b + 10; }",
        "  int c = a + 100;",
        "  public static void main(String[] x){",
        "    InitBlocks o = new InitBlocks();",
        "    System.out.println(s);",
        "    System.out.println(o.a);",
        "    System.out.println(o.c);",
        "  }",
        "}",
      ].join("\n"),
      "7\n11\n111\n",
    );
  },
);

test(
  "record compact constructor runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "CompactRec",
      [
        "record Range(int lo, int hi) {",
        "  Range {",
        "    if (lo > hi) { int t = lo; lo = hi; hi = t; }",
        "  }",
        "}",
        "public class CompactRec {",
        "  public static void main(String[] a){",
        "    Range r = new Range(5, 2);",
        '    System.out.println(r.lo() + "," + r.hi());',
        "    Range s = new Range(1, 9);",
        '    System.out.println(s.lo() + "," + s.hi());',
        "  }",
        "}",
      ].join("\n"),
      "2,5\n1,9\n",
    );
  },
);

test(
  "record explicit canonical and alternate constructors run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "CanonRec",
      [
        "record Frac(int num, int den) {",
        "  Frac(int num, int den) {",
        "    if (den == 0) throw new ArithmeticException();",
        "    this.num = num;",
        "    this.den = den;",
        "  }",
        "  Frac(int whole) { this(whole, 1); }",
        "}",
        "public class CanonRec {",
        "  public static void main(String[] a){",
        "    Frac f = new Frac(3, 4);",
        '    System.out.println(f.num() + "/" + f.den());',
        "    Frac g = new Frac(7);",
        '    System.out.println(g.num() + "/" + g.den());',
        "  }",
        "}",
      ].join("\n"),
      "3/4\n7/1\n",
    );
  },
);

test(
  "record explicit accessor override runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "AccRec",
      [
        "record Name(String first, String last) {",
        "  public String first() { return first.toUpperCase(); }",
        "}",
        "public class AccRec {",
        "  public static void main(String[] a){",
        '    Name n = new Name("ann", "lee");',
        "    System.out.println(n.first());",
        "    System.out.println(n.last());",
        "  }",
        "}",
      ].join("\n"),
      "ANN\nlee\n",
    );
  },
);

test(
  "non-static member inner class accessing the enclosing instance runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Outer",
      [
        "public class Outer {",
        "  int base = 10;",
        "  class Inner {",
        "    int v;",
        "    Inner(int x) { v = x; }",
        "    int sum() { return v + base; }",
        "    void bump() { base = base + v; }",
        "  }",
        "  int run() {",
        "    Inner i = new Inner(5);",
        "    i.bump();",
        "    return i.sum();",
        "  }",
        "  public static void main(String[] a){ System.out.println(new Outer().run()); }",
        "}",
      ].join("\n"),
      // bump(): base = 10 + 5 = 15; sum(): v(5) + base(15) = 20
      "20\n",
    );
  },
);

test(
  "local class with a declared constructor and capture runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "LCtor",
      [
        "public class LCtor {",
        "  int run(int k) {",
        "    int local = 3;",
        "    class C {",
        "      int v;",
        "      C(int mult) { v = (k + local) * mult; }",
        "      int get(){ return v + k; }",
        "    }",
        "    return new C(2).get();",
        "  }",
        "  public static void main(String[] a){ System.out.println(new LCtor().run(10)); }",
        "}",
      ].join("\n"),
      // v = (10 + 3) * 2 = 26; get() = 26 + 10 = 36
      "36\n",
    );
  },
);

test(
  "varargs calls and bodies run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "Varargs",
      [
        "public class Varargs {",
        "  static int sum(int... xs){ int s = 0; for (int v : xs) s += v; return s; }",
        '  static String join(String sep, String... parts){',
        '    StringBuilder b = new StringBuilder();',
        "    for (int i = 0; i < parts.length; i++){ if (i > 0) b.append(sep); b.append(parts[i]); }",
        "    return b.toString();",
        "  }",
        "  public static void main(String[] a){",
        "    System.out.println(sum(1, 2, 3, 4));", // packed -> 10
        "    System.out.println(sum());", // empty -> new int[0] -> 0
        "    int[] arr = {5, 6, 7};",
        "    System.out.println(sum(arr));", // exact-array form -> 18
        '    System.out.println(join("-", "a", "b", "c"));', // a-b-c
        "  }",
        "}",
      ].join("\n"),
      "10\n0\n18\na-b-c\n",
    );
  },
);

test(
  "super.method() calls run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "SuperCall",
      [
        "class Base {",
        "  int f() { return 1; }",
        "  int g(int x) { return x * 2; }",
        "}",
        "public class SuperCall extends Base {",
        "  int f() { return super.f() + 10; }", // 11
        "  int h() { return super.g(super.f()); }", // g(1) = 2
        "  public static void main(String[] a){",
        "    SuperCall s = new SuperCall();",
        "    System.out.println(s.f());",
        "    System.out.println(s.h());",
        "    System.out.println(s.g(5));", // inherited, virtual -> 10
        "  }",
        "}",
      ].join("\n"),
      "11\n2\n10\n",
    );
  },
);

test(
  "super field access runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "SuperField",
      [
        "class B { int x = 5; }",
        "public class SuperField extends B {",
        "  int x = 99;",
        "  int hidden() { return super.x; }", // 5 (field access is non-virtual)
        "  int own() { return x; }", // 99
        "  public static void main(String[] a){",
        "    SuperField s = new SuperField();",
        "    System.out.println(s.hidden());",
        "    System.out.println(s.own());",
        "  }",
        "}",
      ].join("\n"),
      "5\n99\n",
    );
  },
);

test(
  "switch over a boxed Integer selector runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "BoxedSwitch",
      [
        "public class BoxedSwitch {",
        "  static String f(Integer n) {",
        "    switch (n) {",
        '      case 1: return "one";',
        '      case 2: return "two";',
        '      default: return "many";',
        "    }",
        "  }",
        "  public static void main(String[] a){",
        "    System.out.println(f(1));",
        "    System.out.println(f(2));",
        "    System.out.println(f(9));",
        "  }",
        "}",
      ].join("\n"),
      "one\ntwo\nmany\n",
    );
  },
);

test(
  "qualified Outer.this access runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "QualThis",
      [
        "public class QualThis {",
        "  int v = 7;",
        "  class Inner {",
        "    int v = 3;",
        "    int outer() { return QualThis.this.v; }", // 7
        "    int inner() { return v; }", // 3
        "    int both() { return QualThis.this.v + this.v; }", // 10
        "  }",
        "  int run() { Inner i = new Inner(); return i.outer() * 100 + i.inner() * 10 + i.both(); }",
        "  public static void main(String[] a){ System.out.println(new QualThis().run()); }",
        "}",
      ].join("\n"),
      // outer()=7, inner()=3, both()=10 -> 7*100 + 3*10 + 10 = 740
      "740\n",
    );
  },
);

test(
  "interface static methods and unrelated-reference conditionals run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "IfaceCond",
      [
        "interface Calc { static int twice(int x){ return x * 2; } }",
        "public class IfaceCond {",
        "  public static void main(String[] a){",
        "    System.out.println(Calc.twice(21));", // interface static -> 42
        "    boolean f = true;",
        '    Object o = f ? "str" : Integer.valueOf(1);', // lub -> Object, prints str
        "    System.out.println(o);",
        '    Object p = f ? Integer.valueOf(1) : "str";',
        "    System.out.println(p);", // prints 1
        "  }",
        "}",
      ].join("\n"),
      "42\nstr\n1\n",
    );
  },
);

test(
  "anonymous class using inherited members by simple name runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "AbstractAnon",
      [
        "abstract class Shape {",
        "  int size;",
        "  Shape(int s) { size = s; }",
        "  int describe() { return 1000; }",
        "  abstract int area();",
        "}",
        "public class AbstractAnon {",
        "  public static void main(String[] a){",
        "    Shape sq = new Shape(5) {",
        "      int area() { return size * size + describe(); }", // inherited field + inherited method
        "    };",
        "    System.out.println(sq.area());", // 25 + 1000 = 1025
        "  }",
        "}",
      ].join("\n"),
      "1025\n",
    );
  },
);

test(
  "static imports and array clone run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "StaticImportClone",
      [
        "import static java.lang.Math.max;",
        "import static java.lang.Math.min;",
        "public class StaticImportClone {",
        "  public static void main(String[] a){",
        "    System.out.println(max(3, 7) + min(3, 7));", // 7 + 3 = 10
        "    int[] x = {1, 2, 3};",
        "    int[] y = x.clone();",
        "    y[0] = 9;",
        "    System.out.println(x[0] + y[0]);", // 1 + 9 = 10
        "  }",
        "}",
      ].join("\n"),
      "10\n10\n",
    );
  },
);

test(
  "increment/decrement as a value runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "IncDec",
      [
        "public class IncDec {",
        "  static int id(int x){ return x; }",
        "  public static void main(String[] a){",
        "    int i = 5;",
        "    int j = i++;", // j=5, i=6
        "    int k = ++i;", // k=7, i=7
        "    System.out.println(i + \" \" + j + \" \" + k);", // 7 5 7
        "    int[] arr = new int[3];",
        "    int n = 0;",
        "    arr[n++] = 10; arr[n++] = 20;", // arr={10,20,0}, n=2
        "    System.out.println(arr[0] + \" \" + arr[1] + \" \" + n);", // 10 20 2
        "    System.out.println(id(n--) + \" \" + n);", // id(2)=2, n=1
        "    long p = 100; long q = p--;", // q=100, p=99
        "    System.out.println(p + \" \" + q);", // 99 100
        "    double d = 1.5; double e = ++d;", // e=2.5, d=2.5
        "    System.out.println(d + \" \" + e);", // 2.5 2.5
        "  }",
        "}",
      ].join("\n"),
      "7 5 7\n10 20 2\n2 1\n99 100\n2.5 2.5\n",
    );
  },
);

test(
  "boolean bitwise operators as values run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "BoolBitwise",
      [
        "public class BoolBitwise {",
        "  public static void main(String[] a){",
        "    boolean x = true, y = false;",
        "    System.out.println(x & y);", // as a call argument
        "    System.out.println(x | y);",
        "    System.out.println(x ^ y);",
        '    System.out.println("" + (x & y) + (x | y));', // as concat operands
        "    boolean r = (x | y) & !(x ^ y);",
        "    System.out.println(r);",
        "  }",
        "}",
      ].join("\n"),
      "false\ntrue\ntrue\nfalsetrue\nfalse\n",
    );
  },
);

test(
  "shifts with wide distances and byte/char increment overflow run identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "ShiftIncEdge",
      [
        "public class ShiftIncEdge {",
        "  public static void main(String[] a){",
        "    int r = 1 << 32L;", // distance 32L masked by 31 -> 0 -> 1
        "    System.out.println(r);",
        "    int x = 1; long n = 40;", // 40 & 31 = 8
        "    System.out.println(x << n);", // 256
        "    int y = 1; long m = 8; y <<= m;", // compound, wide distance
        "    System.out.println(y);", // 256
        "    byte b = 127; b++;", // overflow -> -128 (iinc would give 128)
        "    System.out.println(b);",
        "    byte c = 127; byte d = ++c;", // prefix value, narrowed
        "    System.out.println(d);", // -128
        "    char ch = 65; ch++;", // 'A' -> 'B'
        "    System.out.println(ch);",
        "  }",
        "}",
      ].join("\n"),
      "1\n256\n256\n-128\n-128\nB\n",
    );
  },
);

test(
  "abrupt completion inside finally runs identically to javac",
  { skip: HAS_JAVA ? false : "no JDK" },
  () => {
    runsLikeJavac(
      "FinallyAbrupt",
      [
        "public class FinallyAbrupt {",
        "  static int overrideReturn() { try { return 1; } finally { return 2; } }",
        "  static int swallowThrow() { try { throw new RuntimeException(); } finally { return 3; } }",
        "  static int breakFromFinally() {",
        "    int s = 0;",
        "    for (int i = 0; i < 3; i++) { try { s += i; } finally { if (i == 1) break; } }",
        "    return s;", // i=0: s=0; i=1: s=1, finally breaks -> s=1
        "  }",
        "  public static void main(String[] a){",
        "    System.out.println(overrideReturn());", // 2
        "    System.out.println(swallowThrow());", // 3
        "    System.out.println(breakFromFinally());", // 1
        "  }",
        "}",
      ].join("\n"),
      "2\n3\n1\n",
    );
  },
);
