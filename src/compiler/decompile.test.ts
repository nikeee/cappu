// Phases 1.3 to 1.9 of `cappu decompile` (nikeee/cappu#43): straight-line
// bytecode, branches, loops, calls, `try`/`catch`, array initializers and
// string concatenation, back to Java source.
//
// The class bytes come from test-fixtures/emitter/emit-baselines, which our own
// emitter produced - so no JDK is needed here. Two tiers:
//   1. text baselines, also read by the Go port (togo/internal/compiler/decompile_test.go),
//      which makes them the TS/Go parity check;
//   2. a roundtrip: re-emit the decompiled source and require the same
//      normalized instruction stream, which proves the output is valid Java
//      that means what the input meant.

import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, realpathSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import TempDir from "../TempDir.ts";
import { type Uri } from "../workspace.ts";
import { decompileToSource } from "../cli/decompile.ts";
import { createChecker } from "./checker.ts";
import { decompile } from "./decompile.ts";
import { disassemble } from "./disasm.ts";
import { emitSourceFile } from "./emitter.ts";
import { parseJavapText } from "./javapNormalize.ts";
import { parseSourceFile } from "./parser.ts";
import { readZipEntries } from "./zipReader.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { createProgram } from "./program.ts";

const here = import.meta.dirname;
const emitBaselines = join(here, "..", "..", "test-fixtures", "emitter", "emit-baselines");
const sourceBaselines = join(here, "..", "..", "test-fixtures", "decompiler", "source-baselines");
const shouldUpdate = process.env.UPDATE_BASELINES === "1";

function classBytes(name: string): Uint8Array {
  return readFileSync(join(emitBaselines, `${name}.class`));
}

// Every class our emitter produced whose methods this phase reconstructs in
// full - arithmetic, conversions, fields, arrays, casts, control flow and calls.
const FULLY_DECOMPILED = [
  "AnnAll",
  "Arithmetic",
  "ArrayLoad",
  "ArrayStore",
  "BoundErasure",
  "Boxing",
  "CastInstance",
  "Cl",
  "ClassLit",
  "Compute",
  "Concat",
  "Constants",
  "ControlFlow",
  "Empty",
  "EnumAbstract$1",
  "EnumAbstract$2",
  "EnumMixed$1",
  "EnumMixed$2",
  "EnumUnqualified",
  "Fields",
  "FloatArith",
  "FloatConst",
  "FloatConv",
  "Fold",
  "Hello",
  "ICast",
  "ICast$A",
  "ICast$B",
  "ISA",
  "ISB",
  "ImplicitSealed",
  "IntConv",
  "IntLiterals",
  "Invoke",
  "Locals",
  "LongArith",
  "Methods",
  "ModifiedFields",
  "Nest",
  "Nest$Counter",
  "Nest$Point",
  "NewArray",
  "PrivateCall",
  "Pt",
  "ReturnLiterals",
  "Returns",
  "Rt",
  "Sealed",
  "SealedI",
  "StaticFields",
  "SubA",
  "SubB",
  "SubC",
  "Switches",
  "VarargsAndAbstract",
  "VarargsPack",
];

// Classes kept for the bail-out rendering: an inner class's constructor and the
// members javac generates for an enum are not this phase's job, and must say so.
const NOT_DECOMPILED = [
  "EnumAbstract",
  "EnumMixed",
  "QualifiedAnon$1",
  "QualifiedAnon$Inner",
  "QualifiedAnon",
  "QualifiedNew$Inner",
  "QualifiedNew",
];

// --- tier 1: text baselines ---------------------------------------------------------

for (const name of [...FULLY_DECOMPILED, ...NOT_DECOMPILED]) {
  test(`decompiles to the source baseline: ${name}`, () => {
    const actual = decompileToSource(classBytes(name));
    const baseline = join(sourceBaselines, `${name}.java`);
    if (shouldUpdate || !existsSync(baseline)) {
      mkdirSync(sourceBaselines, { recursive: true });
      writeFileSync(baseline, actual);
    }
    expect(actual).toEqual(readFileSync(baseline, "utf8"));
  });
}

test("only the classes that cannot be reconstructed say so", () => {
  // The marker is the comment, not the `throw`: a bailed-out static initializer
  // has no value to return, so it renders as the disassembly alone.
  for (const name of FULLY_DECOMPILED) {
    expect({ [name]: decompile(classBytes(name)).includes("/* cappu:") }).toEqual({
      [name]: false,
    });
  }
  for (const name of NOT_DECOMPILED) {
    expect({ [name]: decompile(classBytes(name)).includes("/* cappu:") }).toEqual({
      [name]: true,
    });
  }
});

// --- tier 2: the roundtrip ----------------------------------------------------------

function emitClasses(
  mainClass: string,
  source: string,
  debugInfo = false,
): { name: string; bytes: Uint8Array }[] {
  const program = createProgram();
  loadJdkStub(program);
  const uri = `file:///${mainClass}.java` as Uri;
  program.setOpenDocument(uri, source, 1);
  return emitSourceFile(program.getSourceFile(uri)!, program, createChecker(program), {
    debugInfo,
  });
}

function emitClass(name: string, source: string, debugInfo = false): Uint8Array {
  const program = createProgram();
  loadJdkStub(program);
  const uri = `file:///${name}.java` as Uri;
  program.setOpenDocument(uri, source, 1);
  const emitted = emitSourceFile(program.getSourceFile(uri)!, program, createChecker(program), {
    debugInfo,
  });
  const cls = emitted.find(c => c.name === name);
  if (!cls) throw new Error(`no emitted class named ${name}`);
  return cls.bytes;
}

/**
 * The normalized instruction stream per member, as the javac comparison uses.
 * Type arguments are stripped from the member signature: the decompiler works
 * off descriptors, so the re-emitted class has erased generics by design.
 */
function instructionStreams(bytes: Uint8Array, name: string): Map<string, string[]> {
  const disasm = parseJavapText(disassemble(bytes)).get(name);
  expect(disasm).toBeDefined();
  return new Map(disasm!.code.map(([member, code]) => [member.replaceAll(/<[^()]*>/g, ""), code]));
}

// `ClassLit.prim()` reads `java.lang.Integer.TYPE`, which javac accepts and the
// decompiler gets right, but our JDK stub does not declare - so re-emitting it
// degrades to aconst_null and checking it reports an unresolved symbol. Both
// oracles below are only as good as the stub, so that class is held to its text
// baseline alone.
const STUB_GAP = ["ClassLit"];

// `Nest$Counter.tick()` reconstructs `this.n = this.n + 1` exactly, but our
// emitter writes it as `aload_0; aload_0; getfield` where javac used
// `aload_0; dup; getfield` - the same statement, a different codegen strategy,
// which this instruction-identical oracle cannot express.
// The class javac writes for an enum constant with a body is not expressible as
// source at all - `class X$1 extends X` where X is an enum is exactly what Java
// forbids anyone to write - so nothing can re-emit it.
const ENUM_CONSTANT_BODIES = ["EnumAbstract$1", "EnumAbstract$2", "EnumMixed$1", "EnumMixed$2"];

// Reconstructions this oracle cannot judge, for reasons of its own:
//   - `ICast` names two nested types that live outside the one class the
//     decompiler writes, so re-emitting the file alone cannot resolve them;
//   - `BoundErasure` declares `T get()`, and the decompiler works off the
//     descriptor, so what comes back is the erased `CharSequence get()` - a
//     different member, not different code;
//   - `EnumUnqualified` declares a local inside a loop, which is hoisted to the
//     top of the method and shifts every slot after it;
//   - `Boxing` calls `Integer.intValue()`, and our emitter writes the *declaring*
//     class into the method ref (`Number.intValue`) where javac writes the
//     receiver's static type. That is an emitter bug, not a decompiler one.
const EMITTER_GAP = ["ICast", "BoundErasure", "EnumUnqualified", "Boxing"];

const NO_ROUNDTRIP = [...STUB_GAP, "Nest$Counter", ...ENUM_CONSTANT_BODIES, ...EMITTER_GAP];

for (const name of FULLY_DECOMPILED.filter(n => !NO_ROUNDTRIP.includes(n))) {
  test(`recompiles to the same bytecode: ${name}`, () => {
    const original = classBytes(name);
    const source = decompileToSource(original);
    const streams = instructionStreams(emitClass(name, source), name);
    for (const [member, instructions] of instructionStreams(original, name)) {
      expect({ [member]: streams.get(member) }).toEqual({ [member]: instructions });
    }
  });
}

// --- the output has to be valid Java --------------------------------------------------

/** Parse and type-check a reconstruction the way `cappu check` would. */
function diagnosticsOf(name: string, source: string): string[] {
  const program = createProgram();
  loadJdkStub(program);
  const uri = `file:///${name}.java` as Uri;
  program.setOpenDocument(uri, source, 1);
  const sourceFile = program.getSourceFile(uri)!;
  return [
    ...sourceFile.parseDiagnostics.map(d => `parse: ${d.messageText}`),
    ...createChecker(program)
      .getSemanticDiagnostics(sourceFile)
      .map(d => `semantic: ${d.messageText}`),
  ];
}

for (const name of [...FULLY_DECOMPILED, ...NOT_DECOMPILED].filter(n => !STUB_GAP.includes(n))) {
  test(`type-checks its own output: ${name}`, () => {
    expect({ [name]: diagnosticsOf(name, decompileToSource(classBytes(name))) }).toEqual({
      [name]: [],
    });
  });
}

// --- constants javac inlines and javap prints unsourceably --------------------------

function hasTool(name: string): boolean {
  try {
    execFileSync(name, ["-version"], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
}
const HAS_JAVAC = hasTool("javac");
const HAS_JAVAP = hasTool("javap");
const HAS_JAVA = hasTool("java");

// Only javac produces these: NaN and the infinities reach the constant pool
// because `Float.NaN` and friends are constant variables, and our own emitter
// does not fold float division. javap prints them as `NaNf`/`Infinity`, which is
// not Java - the wrapper constants are.
const NON_FINITE_SOURCE =
  "public class NonFinite {\n" +
  "  static float nan() { return Float.NaN; }\n" +
  "  static float inf() { return Float.POSITIVE_INFINITY; }\n" +
  "  static double negInf() { return Double.NEGATIVE_INFINITY; }\n" +
  "  static double dnan() { return Double.NaN; }\n" +
  "}";

test(
  "renders NaN and the infinities as the wrapper constants",
  { skip: HAS_JAVAC && HAS_JAVAP ? false : "no JDK (javac/javap)" },
  () => {
    using dir = TempDir.create("cappu-decompile-nonfinite-");
    const classFile = compileWithJavac(NON_FINITE_SOURCE, "NonFinite", dir.path);
    const source = decompileToSource(readFileSync(classFile));
    for (const constant of [
      "java.lang.Float.NaN",
      "java.lang.Float.POSITIVE_INFINITY",
      "java.lang.Double.NEGATIVE_INFINITY",
      "java.lang.Double.NaN",
    ]) {
      expect(source).toContain(constant);
    }
    // Not checked with diagnosticsOf: our JDK stub declares no fields on Float
    // and Double, so our own checker calls these unresolved (the same gap that
    // exempts ClassLit above). javac is the oracle here instead - it inlines
    // them right back, so the bytecode has to come out identical.
    const roundTripped = compileWithJavac(source, "NonFinite", join(dir.path, "again"));
    expect(javap(roundTripped)).toEqual(javap(classFile));
  },
);

// javac lays branches out its own way - our emitter is not the oracle here, the
// real compiler is: decompiled and recompiled, the bytecode has to come back
// identical, instruction for instruction.
const BRANCHY_SOURCE =
  "public class Branchy {\n" +
  "  static int clamp(int v, int lo, int hi) { if (v < lo) return lo; if (v > hi) return hi; return v; }\n" +
  "  static boolean between(int v, int lo, int hi) { return v >= lo && v <= hi; }\n" +
  "  static boolean either(int a, int b) { return a > 0 || b > 0; }\n" +
  "  static int max3(int a, int b, int c) { int m = a > b ? a : b; return m > c ? m : c; }\n" +
  "  static int sign(long v) { return v < 0L ? -1 : (v > 0L ? 1 : 0); }\n" +
  "  static int check(int a, java.lang.RuntimeException e) { if (a < 0) throw e; return a; }\n" +
  "  static int both(boolean c) { int x; if (c) { x = 1; } else { x = 2; } return x; }\n" +
  "  static boolean nested(int a, int b, int c) { return (a > 0 && b > 0) || c > 0; }\n" +
  "  static double pick(boolean c, int a, double b) { return c ? a : b; }\n" +
  "  static boolean isNull(java.lang.Object o) { return o == null; }\n" +
  // A materialized boolean used where a number belongs: the int form has to
  // keep the branch's own arms.
  "  static int index(boolean c, int[] a) { a[c ? 0 : 1] = 7; return a[0]; }\n" +
  "  static boolean staleCondition(int a) { boolean b = true; if (b) { return a > 0; } return b; }\n" +
  "  static int numeric(int a) { int x = a > 0 ? 1 : 0; return x + 1; }\n" +
  "  static int counted(boolean q, int[] xs) { return xs[q ? 2 : 0] + (q ? 1 : 0); }\n" +
  // The three groupings of a compound condition, which javac lays out as a
  // chain of branches that share their outcomes.
  "  static int andOr(int a, int b, int c) { if ((a > 0 && b > 0) || c > 0) { return 11; } else { return 22; } }\n" +
  "  static int orAnd(int a, int b, int c) { if (a > 0 || (b > 0 && c > 0)) { return 11; } else { return 22; } }\n" +
  "  static int andGroup(int a, int b, int c) { if (a > 0 && (b > 0 || c > 0)) { return 11; } else { return 22; } }\n" +
  "}";

test(
  "recompiles javac's own branches to the same bytecode",
  { skip: HAS_JAVAC && HAS_JAVAP ? false : "no JDK (javac/javap)" },
  () => {
    using dir = TempDir.create("cappu-decompile-branchy-");
    const classFile = compileWithJavac(BRANCHY_SOURCE, "Branchy", dir.path);
    const source = decompileToSource(readFileSync(classFile));
    expect(source).not.toContain("/* cappu:");
    const roundTripped = compileWithJavac(source, "Branchy", join(dir.path, "again"));
    expect(javap(roundTripped)).toEqual(javap(classFile));
  },
);

// Every loop shape javac writes. These reconstruct to source it compiles back to
// the same bytecode from, which is the only oracle that can see an inverted test
// or an arm on the wrong side - our own emitter lays branches out differently.
// Every `try` shape javac writes, and the reconstruction has to recompile to the
// same bytecode: clause order, which arm is the body, and where the statement
// ends are all invisible to a text baseline.
const CATCHY_SOURCE =
  "public class Catchy {\n" +
  "  static int one(int a) { try { return 10 / a; } catch (java.lang.ArithmeticException e) { return -1; } }\n" +
  "  static int two(int[] xs, int i) { int r = 0; try { r = xs[i]; } catch (java.lang.ArrayIndexOutOfBoundsException e) { r = -1; } catch (java.lang.NullPointerException e2) { r = -2; } return r; }\n" +
  "  static int multi(int[] xs, int i) { try { return xs[i] / i; } catch (java.lang.ArithmeticException | java.lang.ArrayIndexOutOfBoundsException e) { return e.hashCode(); } }\n" +
  // A `try` inside a loop: the handler leaves the loop, which is a `break` and
  // not the end of the statement.
  "  static int loopy(int[] xs) { int s = 0; for (int i = 0; i < 10; i++) { try { s = s + xs[i]; } catch (java.lang.RuntimeException e) { break; } } return s; }\n" +
  "  static int continues(int[] xs) { int s = 0; int i = 0; while (i < xs.length) { try { s = s + 10 / xs[i]; } catch (java.lang.ArithmeticException e) { i = i + 1; continue; } i = i + 1; } return s; }\n" +
  // The outer `try` protects the inner `catch` too, which splits its range in two.
  "  static int nested(int[] xs) { try { try { return xs[0]; } catch (java.lang.NullPointerException e) { return 1; } } catch (java.lang.RuntimeException e) { return 2; } }\n" +
  "  static void unused(int a) { try { java.lang.System.out.println(a); } catch (java.lang.RuntimeException e) { } }\n" +
  "  static int rethrow(int[] xs) { try { return xs[0]; } catch (java.lang.RuntimeException e) { throw e; } }\n" +
  "  static int afterCatch(int[] xs) { int r = 0; try { return xs[0]; } catch (java.lang.RuntimeException e) { r = 5; } return r + 1; }\n" +
  "  static int twice(int[] xs) { int r = 0; try { r = xs[0]; } catch (java.lang.RuntimeException e) { r = 1; } try { r = r + xs[1]; } catch (java.lang.RuntimeException e2) { r = 2; } return r; }\n" +
  "  static int inIf(boolean c, int[] xs) { if (c) { try { return xs[0]; } catch (java.lang.RuntimeException e) { return -1; } } return 0; }\n" +
  "  static int throwsInside(int a) { try { if (a < 0) { throw new java.lang.IllegalStateException(); } return a; } catch (java.lang.IllegalStateException e) { return -1; } }\n" +
  "  static int alwaysThrows(int a) { try { throw new java.lang.IllegalStateException(); } catch (java.lang.IllegalStateException e) { return a; } }\n" +
  // A `do`'s latch holds the tail of the body, so falling into it is not a
  // `continue` - and the tail is not part of the `try`.
  "  static int doWhile(int a) { int d = 0; do { try { check(a); } catch (java.lang.IllegalStateException e) { return -1; } d = d + 1; } while (d < 3); return d; }\n" +
  // The handler branches and the body never returns normally: the end of the
  // statement is not the merge point inside the `catch`.
  "  static int handlerBranch(int a) { try { check(a); return 1; } catch (java.lang.IllegalStateException e) { return a > 0 ? 5 : 6; } }\n" +
  // The slot the catch parameter sat in is reused once the clause ends.
  "  static int slotAfter(int a) { int r = 0; try { r = 100 / a; } catch (java.lang.ArithmeticException e) { r = -1; } int q = r * 2; return q; }\n" +
  // A handler that falls back into the loop body: without the throwing edges the
  // loop stops being reducible.
  "  static int fallsBack(int n) { int s = 0; for (int i = 0; i < n; i++) { try { check(i); s = s + i; } catch (java.lang.IllegalStateException e) { s = s + 100; } } return s; }\n" +
  // javac keeps a `return`, a `break` and a `continue` out of the protected
  // range, which splits it around them.
  "  static int earlyReturn(int n) { try { if (n == 1) { return 1; } check(n); } catch (java.lang.IllegalStateException e) { return 8; } return 9; }\n" +
  "  static int breakInTry(int n) { int s = 0; for (int i = 0; i < n; i++) { try { if (i == 2) { break; } s = s + i; } catch (java.lang.IllegalStateException e) { s = -1; } } return s; }\n" +
  "  static int endExit(boolean c, int n) { try { if (c) { check(n); } else { return 0; } } catch (java.lang.IllegalStateException e) { return 2; } return 3; }\n" +
  "  static void check(int n) { if (n < 0) { throw new java.lang.IllegalStateException(); } }\n" +
  "}";

test(
  "recompiles javac's own try/catch to the same bytecode",
  { skip: HAS_JAVAC && HAS_JAVAP ? false : "no JDK (javac/javap)" },
  () => {
    using dir = TempDir.create("cappu-decompile-catchy-");
    const classFile = compileWithJavac(CATCHY_SOURCE, "Catchy", dir.path);
    const source = decompileToSource(readFileSync(classFile));
    expect(source).not.toContain("/* cappu:");
    const roundTripped = compileWithJavac(source, "Catchy", join(dir.path, "again"));
    expect(javap(roundTripped)).toEqual(javap(classFile));
  },
);

const LOOPY_SOURCE =
  "public class Loopy {\n" +
  "  static int sum(int n) { int s = 0; for (int i = 0; i < n; i++) { s = s + i; } return s; }\n" +
  "  static int down(int n) { int c = 0; while (n > 0) { c = c + n; n = n - 1; } return c; }\n" +
  "  static int atLeastOnce(int n) { int i = 0; do { i = i + 3; } while (i < n); return i; }\n" +
  "  static int breaks(int[] xs, int stop) { int t = 0; for (int i = 0; i < xs.length; i++) { if (xs[i] == stop) { break; } t = t + xs[i]; } return t; }\n" +
  "  static int forever(int n) { int i = 0; while (true) { i = i + 2; if (i > n) { return i; } } }\n" +
  "  static int both(int a, int b) { int t = 0; while (a > 0 && b > 0) { t = t + 1; a = a - 1; b = b - 2; } return t; }\n" +
  "  static int either(int a, int b) { int t = 0; while (a > 0 || b > 0) { t = t + 1; a = a - 1; b = b - 1; } return t; }\n" +
  "  static int ifInside(int n) { int t = 0; for (int i = 0; i < n; i++) { if (i % 2 == 0) { t = t + i; } else { t = t - i; } } return t; }\n" +
  "  static int untilNull(java.lang.Object o, int n) { int i = 0; while (o == null && i < n) { i = i + 1; } return i; }\n" +
  "  static long longLoop(long n) { long s = 0L; while (s < n) { s = s + 3L; } return s; }\n" +
  // An arm of an `if` that ends in `continue` never reaches the merge point, and
  // a `do` whose test shares its block with the tail of the body still has one.
  "  static int arms(int a, int b) { int t = a; int i = 0; do { i = i + 1; if (i <= a) { t = t * i; if (a >= b) { continue; } } t = t * t; } while (i < b); return t; }\n" +
  "  static int tail(int a, int b) { int u = b; int i = 0; do { i = i + 1; if (i > a) { u = u * u; } else { u = u - a; } u = u * (u + 1); } while (i < a); return u; }\n" +
  "}";

test(
  "recompiles javac's own loops to the same bytecode",
  { skip: HAS_JAVAC && HAS_JAVAP ? false : "no JDK (javac/javap)" },
  () => {
    using dir = TempDir.create("cappu-decompile-loopy-");
    const classFile = compileWithJavac(LOOPY_SOURCE, "Loopy", dir.path);
    const source = decompileToSource(readFileSync(classFile));
    expect(source).not.toContain("/* cappu:");
    const roundTripped = compileWithJavac(source, "Loopy", join(dir.path, "again"));
    expect(javap(roundTripped)).toEqual(javap(classFile));
  },
);

// A local first written inside a loop is declared at the top of the method, so
// the recompiled slots do not line up with javac's - the bytecode is not
// identical, but what it computes has to be. These run instead.
const LOOPY_RUN_SOURCE =
  "public class LoopyRun {\n" +
  "  static int nested(int n, int m) { int t = 0; for (int i = 0; i < n; i++) { for (int j = 0; j < m; j++) { t = t + i * j; } } return t; }\n" +
  "  static int continues(int[] xs) { int t = 0; int i = 0; while (i < xs.length) { int v = xs[i]; i = i + 1; if (v < 0) { continue; } t = t + v; } return t; }\n" +
  "  static int windows(int n) { int t = 0; int i = 0; while (i < n) { int step = i % 3 + 1; i = i + step; t = t + step * i; } return t; }\n" +
  "  static int triangle(int n) { int t = 0; int i = 0; do { int row = 0; for (int j = 0; j <= i; j++) { row = row + j; } t = t + row; i = i + 1; } while (i < n); return t; }\n" +
  // A `do` whose body starts with a statement and leaves through a `break`: what
  // the head branches to is the break, not the end of the loop.
  "  static int breakOut(int a, int b) { int u = b; int i = 0; do { i = i + 1; u = a * 4; if (a == u) { break; } for (int j = 0; j < i; j++) { if (a != u) { u = a + b; } } } while (i < a); return u; }\n" +
  "}";

// The caller stays javac's, so only the class under test is swapped for the
// decompiled one - `main` itself is full of calls, which is a later phase.
const LOOPY_DRIVER_SOURCE =
  "public class LoopyDriver {\n" +
  "  public static void main(String[] args) {\n" +
  "    int[] xs = { 3, -1, 4, -1, 5, 9, -2, 6 };\n" +
  "    for (int n = -1; n < 6; n++) {\n" +
  '      System.out.println(LoopyRun.nested(n, n + 1) + " " + LoopyRun.continues(xs)\n' +
  '        + " " + LoopyRun.windows(n) + " " + LoopyRun.triangle(n)\n' +
  '        + " " + LoopyRun.breakOut(n, n + 2));\n' +
  "    }\n" +
  "  }\n" +
  "}";

test(
  "runs like javac's own loops when the slots cannot line up",
  { skip: HAS_JAVAC && HAS_JAVA ? false : "no JDK (javac/java)" },
  () => {
    using dir = TempDir.create("cappu-decompile-loopyrun-");
    const classFile = compileWithJavac(LOOPY_RUN_SOURCE, "LoopyRun", dir.path);
    compileWithJavac(LOOPY_DRIVER_SOURCE, "LoopyDriver", dir.path, dir.path);
    const source = decompileToSource(readFileSync(classFile));
    expect(source).not.toContain("/* cappu:");
    const again = join(dir.path, "again");
    compileWithJavac(source, "LoopyRun", again);
    const expected = execFileSync("java", ["-cp", dir.path, "LoopyDriver"], { encoding: "utf8" });
    // `again` first, so the decompiled class is the one that runs.
    const actual = execFileSync("java", ["-cp", `${again}:${dir.path}`, "LoopyDriver"], {
      encoding: "utf8",
    });
    expect(actual).toEqual(expected);
    expect(actual).not.toEqual("");
  },
);

// Every call shape javac writes: static, virtual, interface, private and
// `super`, a `new`, a chain, a call whose value is dropped, and one inside a
// loop. Raw `java.util.List` on purpose - the decompiler works off descriptors,
// so a type argument would come back erased and only the signature would differ.
const CALLSY_SOURCE =
  "public class Callsy {\n" +
  "  private int seed;\n" +
  "  public Callsy(int seed) { this.seed = seed; }\n" +
  "  private int twice(int v) { return v * 2; }\n" +
  "  static int stat(int v) { return v + 1; }\n" +
  "  int use(int v) { return this.twice(v) + stat(v); }\n" +
  "  int chain(String s) { return s.trim().length(); }\n" +
  "  static int iface(java.util.List xs) { return xs.size(); }\n" +
  "  static Object make(int v) { return new Callsy(v); }\n" +
  "  static int viaNew(int v) { return new Callsy(v).seed; }\n" +
  "  static void discard(java.util.List xs) { xs.remove(0); }\n" +
  "  static int nested(int v) { return stat(stat(stat(v))); }\n" +
  "  static String str(Object o) { return o.toString(); }\n" +
  "  static int cmp(String a, String b) { return a.compareTo(b); }\n" +
  "  int loopCall(int n) { int t = 0; for (int i = 0; i < n; i++) { t = t + this.twice(i); } return t; }\n" +
  "  static boolean eq(Object a, Object b) { return a.equals(b); }\n" +
  "  static int len(String s) { if (s == null) { return 0; } return s.length(); }\n" +
  "  int superHash() { return super.hashCode(); }\n" +
  "  static long widen(int v) { return java.lang.Math.abs((long) v); }\n" +
  "  static int both(int a, String s) { if (a > 0 && s.length() > 3) { return 1; } return 0; }\n" +
  "  static int either(String s, int a) { if (s == null || a > 0) { return 1; } return 0; }\n" +
  "  static int untilLen(String s) { int t = 0; while (t < s.length()) { t = t + 2; } return t; }\n" +
  "  static int pick(boolean c, int a) { return c ? stat(a) : stat(-a); }\n" +
  '  static String name(Object o) { return o == null ? "null" : o.toString(); }\n' +
  // A `do` whose latch is body-tail *calls* plus the test: cutting it would
  // put a `continue` in front of the tail and drop it.
  "  static int callTail(int n) { int t = 0; int i = 0; do { if (i > 1) { t = t + stat(i); } else { t = t - stat(i); } t = t + stat(t); i = i + 1; } while (stat(i) < n); return t; }\n" +
  "}\n";

test(
  "recompiles javac's own calls to the same bytecode",
  { skip: HAS_JAVAC && HAS_JAVAP ? false : "no JDK (javac/javap)" },
  () => {
    using dir = TempDir.create("cappu-decompile-callsy-");
    const classFile = compileWithJavac(CALLSY_SOURCE, "Callsy", dir.path);
    const source = decompileToSource(readFileSync(classFile));
    expect(source).not.toContain("/* cappu:");
    const roundTripped = compileWithJavac(source, "Callsy", join(dir.path, "again"));
    expect(javap(roundTripped)).toEqual(javap(classFile));
  },
);

// Every array-initializer shape javac writes: the `new T[]{...}` form and the
// `{...}` shorthand, primitives of every width, a nested one, an element that
// is a call, a varargs pack, and the sized form that is *not* an initializer.
const ARRAYLY_SOURCE =
  "public class Arrayly {\n" +
  "  static int[] ints() { return new int[]{1, 2, 3}; }\n" +
  "  static int[] shorthand() { int[] a = {4, 5}; return a; }\n" +
  "  static int[] empty() { return new int[]{}; }\n" +
  "  static long[] longs() { return new long[]{1L, 2L}; }\n" +
  "  static double[] doubles() { return new double[]{1.5, 2.5}; }\n" +
  "  static boolean[] flags() { return new boolean[]{true, false}; }\n" +
  "  static char[] chars() { return new char[]{'a', 'b'}; }\n" +
  "  static byte[] bytes() { return new byte[]{1, 2}; }\n" +
  "  static short[] shorts() { return new short[]{1, 2}; }\n" +
  "  static float[] floats() { return new float[]{1.5f}; }\n" +
  '  static String[] strings() { return new String[]{"a", "b"}; }\n' +
  "  static Object[] objects() { return new Object[]{null, null}; }\n" +
  "  static int[][] nested() { return new int[][]{{1, 2}, {3}}; }\n" +
  "  static String[][] nestedRefs() { return new String[][]{{null}}; }\n" +
  "  static int sum(int[] a) { return a[0] + a[1]; }\n" +
  "  static int call() { return sum(new int[]{7, 8}); }\n" +
  "  static int[] fromCalls(int n) { return new int[]{sum(new int[]{n, n}), n}; }\n" +
  "  static int[] sized(int n) { int[] a = new int[n]; a[0] = 1; return a; }\n" +
  "  static int[] sizedConst() { int[] a = new int[2]; a[1] = 1; return a; }\n" +
  "  static int[] branchy(boolean c) { return new int[]{c ? 1 : 2, 3}; }\n" +
  '  static String fmt(int a) { return String.format("%d", new Object[]{Integer.valueOf(a)}); }\n' +
  "  static int[][] multi(int n) { return new int[n][2]; }\n" +
  "}\n";

test(
  "recompiles javac's own array initializers to the same bytecode",
  { skip: HAS_JAVAC && HAS_JAVAP ? false : "no JDK (javac/javap)" },
  () => {
    using dir = TempDir.create("cappu-decompile-arrayly-");
    const classFile = compileWithJavac(ARRAYLY_SOURCE, "Arrayly", dir.path);
    const source = decompileToSource(readFileSync(classFile));
    expect(source).not.toContain("/* cappu:");
    const roundTripped = compileWithJavac(source, "Arrayly", join(dir.path, "again"));
    expect(javap(roundTripped)).toEqual(javap(classFile));
  },
);

// Every string concatenation javac writes. The recipe is what says where the
// literal parts sit, and only a recompile can see a misplaced one - so this
// runs the reconstruction back through javac and compares the bytecode.
const CONCATTY_SOURCE =
  "public class Concatty {\n" +
  "  static String si(String s, int i) { return s + i; }\n" +
  "  static String is(int i, String s) { return i + s; }\n" +
  '  static String around(String s, int i) { return "x=" + s + ", i=" + i + "!"; }\n' +
  // A single operand is still a concatenation: `null + ""` is "null".
  '  static String plain(String s) { return s + ""; }\n' +
  // Neither operand is a String, so the empty literal has to come back with it.
  '  static String twoInts(int i, int j) { return "" + i + j; }\n' +
  '  static String objects(Object a, Object b) { return "" + a + b; }\n' +
  // A literal that contains a recipe tag character is passed as a bootstrap
  // constant instead, and one that contains both tags needs two of them.
  '  static String tag(String s) { return "tag\\u0001here" + s; }\n' +
  '  static String tags(String s) { return "a\\u0002b" + s + "c\\u0001d"; }\n' +
  // javac folds a constant operand into the recipe - it comes back as text.
  "  static String charConst(String s) { return s + '\\n' + \"q\"; }\n" +
  "  static String nullPart(String s) { return s + null; }\n" +
  '  static String escapes(String s) { return "\\"q\\"\\\\\\t" + s + "\\n"; }\n' +
  // The parentheses are the difference between an int addition and two parts.
  '  static String grouped(int i) { return "a" + (i + 1) + "b"; }\n' +
  "  static String append(String s, int n) { s += n; return s; }\n" +
  "  static String types(String s, long l, double d, float f, boolean b, char c, byte y, short h) { return s + l + d + f + b + c + y + h; }\n" +
  "  static String call(String s) { return s + s.length(); }\n" +
  // A `null` operand: javac passes it through `String.valueOf`, and a bare
  // `null` there would bind the `char[]` overload and throw.
  '  static String objectNull(Object o) { return "v" + (Object) null + o; }\n' +
  '  static String stringNull(String s) { return "v" + (String) null + s; }\n' +
  "  static String nested(String a, String b, String c) { return a + b.trim() + c; }\n" +
  "  static int used(String a, int i) { return (a + i).length(); }\n" +
  "  static boolean cond(String a, String b) { return (a + b).isEmpty(); }\n" +
  '  static String loop(int n) { String s = ""; for (int i = 0; i < n; i++) { s = s + i; } return s; }\n' +
  '  static String ternary(boolean c, String a, int i) { return c ? a + i : a + "no"; }\n' +
  '  static String upper(String s) { return ("A" + s).toUpperCase(); }\n' +
  '  static void print(int i) { System.out.println("v" + i); }\n' +
  "}\n";

test(
  "recompiles javac's own string concatenations to the same bytecode",
  { skip: HAS_JAVAC && HAS_JAVAP ? false : "no JDK (javac/javap)" },
  () => {
    using dir = TempDir.create("cappu-decompile-concatty-");
    const classFile = compileWithJavac(CONCATTY_SOURCE, "Concatty", dir.path);
    const source = decompileToSource(readFileSync(classFile));
    expect(source).not.toContain("/* cappu:");
    const roundTripped = compileWithJavac(source, "Concatty", join(dir.path, "again"));
    expect(javap(roundTripped)).toEqual(javap(classFile));
  },
);

const SWITCHY_SOURCE =
  "public class Switchy {\n" +
  "  static int dense(int x) { switch (x) { case 1: return 10; case 2: return 20; case 3: return 30; default: return -1; } }\n" +
  "  static int breaks(int x) { int r = 0; switch (x) { case 0: r = 1; break; case 1: r = 2; break; default: r = 9; } return r + 1; }\n" +
  // Shared labels, a fallthrough, and the gaps a tableswitch pads with its default.
  "  static int fall(int x) { int r = 0; switch (x) { case 1: case 2: r += 1; case 3: r += 2; break; case 7: r += 4; break; } return r; }\n" +
  "  static int sparse(int x) { switch (x) { case 100: return 1; case 5000: return 2; case -7: return 3; } return 0; }\n" +
  "  static int noDefault(int x) { int r = 0; switch (x) { case 1: r = 5; break; case 2: r = 6; break; } return r; }\n" +
  // A case whose whole body is the `break`, and one that returns while the rest
  // break - the post-dominator of the table is EXIT for the second one.
  "  static int emptyCase(int x) { int r = 3; switch (x) { case 1: break; case 2: r = 7; break; default: r = 8; } return r; }\n" +
  "  static int mixedExit(int x) { int r = 0; switch (x) { case 1: return 100; case 2: r = 2; break; default: r = 3; } return r; }\n" +
  "  static int defaultFirst(int x) { int r = 0; switch (x) { default: r += 1; case 5: r += 2; break; case 9: r += 4; } return r; }\n" +
  "  static int nested(int x, int y) { int r = 0; switch (x) { case 1: switch (y) { case 1: r = 11; break; default: r = 12; } break; case 2: r = 20; break; default: r = 99; } return r; }\n" +
  "  static int withIf(int x, boolean b) { int r = 0; switch (x) { case 1: if (b) { r = 1; } else { r = 2; } break; case 2: if (b) { return -1; } r = 3; break; } return r; }\n" +
  "  static int inWhile(int n) { int r = 0; int i = 0; while (i < n) { switch (i % 2) { case 0: r += 1; break; default: r += 2; } i = i + 1; } return r; }\n" +
  // A `switch` catches `break` but not `continue`, which still leaves the loop test.
  "  static int continues(int n) { int r = 0; int i = 0; while (i < n) { i = i + 1; switch (i % 3) { case 0: continue; case 1: r += 1; break; default: r += 2; } r *= 2; } return r; }\n" +
  "  static int charSwitch(char c) { switch (c) { case 'a': return 1; case 'z': return 26; default: return 0; } }\n" +
  "  static int inTry(int x) { int r = 0; try { switch (x) { case 1: r = 1; break; default: r = 2; } } catch (RuntimeException e) { r = -1; } return r; }\n" +
  // A `do`'s continue target is its latch, and javac puts the tail of the body
  // in there: a `break` out of the `switch` that lands on it is not a `continue`.
  "  static int doWhile(int n) { int r = 0; int i = 0; do { switch (i % 3) { case 0: r += 1; break; case 1: r += 2; break; default: r += 3; } r *= 2; i = i + 1; } while (i < n); return r; }\n" +
  "  static int doWhileTail(int n) { int r = 0; int i = 0; do { switch (i % 3) { case 0: r += 1; break; default: r += 3; } i = i + 1; } while (i < n); return r; }\n" +
  // The `switch` is the last statement of the loop, so every case leaves through
  // the loop's own edge: no block after the table ends the statement, and the
  // `default` in the middle is not it either.
  "  static int loopTail(int n, int x) { int r = 0; int i = 0; while (i < n) { i = i + 1; switch (x) { case 2: r += 1; break; case 3: r += 1; r += 4; break; default: r += 1; break; case 4: r += 1; } } return r; }\n" +
  // The inner switch has no `default` of its own, so its table's default is the
  // end of the outer case it sits in - a case that lands exactly on where the
  // statement around it ends.
  "  static int sharedExit(int a, int b) { int r = 0; switch (a) { case 1: switch (b) { case 0: r += 2; case 1: r += 1; break; case 3: return r; } case 4: r += 9; return r; } return r; }\n" +
  // javac writes a `String` switch as two: one over `hashCode`, one over the
  // index it stores. Both come back, which is what the original ran.
  '  static String str(String s) { switch (s) { case "a": return "A"; case "b": return "B"; default: return "?"; } }\n' +
  "}\n";

test(
  "recompiles javac's own switches to the same bytecode",
  { skip: HAS_JAVAC && HAS_JAVAP ? false : "no JDK (javac/javap)" },
  () => {
    using dir = TempDir.create("cappu-decompile-switchy-");
    const classFile = compileWithJavac(SWITCHY_SOURCE, "Switchy", dir.path);
    const source = decompileToSource(readFileSync(classFile));
    expect(source).not.toContain("/* cappu:");
    const roundTripped = compileWithJavac(source, "Switchy", join(dir.path, "again"));
    expect(javap(roundTripped)).toEqual(javap(classFile));
  },
);

// A loop inside a `case` declares its variable there, which the reconstruction
// hoists to the top of the method - the slots shift, so only running it can say
// the two are the same.
const SWITCHY_RUN_SOURCE =
  "public class SwitchyRun {\n" +
  "  static int loopInside(int x, int n) { int r = 0; switch (x) { case 1: for (int i = 0; i < n; i++) { if (i == 3) { break; } r += i; } break; default: r = -1; } return r; }\n" +
  "  static int doInside(int x, int n) { int r = 0; switch (x) { case 1: { int i = 0; do { r += i; i = i + 1; } while (i < n); break; } default: r = 7; } return r; }\n" +
  '  static int tryInside(int x) { switch (x) { case 1: try { return Integer.parseInt("nope"); } catch (NumberFormatException e) { return -1; } default: return 0; } }\n' +
  "}";

const SWITCHY_DRIVER_SOURCE =
  "public class SwitchyDriver {\n" +
  "  public static void main(String[] args) {\n" +
  "    for (int x = -1; x < 4; x++) {\n" +
  "      for (int n = 0; n < 5; n++) {\n" +
  '        System.out.println(SwitchyRun.loopInside(x, n) + " " + SwitchyRun.doInside(x, n)\n' +
  '          + " " + SwitchyRun.tryInside(x));\n' +
  "      }\n" +
  "    }\n" +
  "  }\n" +
  "}";

test(
  "runs like javac's own switches when the slots cannot line up",
  { skip: HAS_JAVAC && HAS_JAVA ? false : "no JDK (javac/java)" },
  () => {
    using dir = TempDir.create("cappu-decompile-switchyrun-");
    const classFile = compileWithJavac(SWITCHY_RUN_SOURCE, "SwitchyRun", dir.path);
    compileWithJavac(SWITCHY_DRIVER_SOURCE, "SwitchyDriver", dir.path, dir.path);
    const source = decompileToSource(readFileSync(classFile));
    expect(source).not.toContain("/* cappu:");
    const again = join(dir.path, "again");
    compileWithJavac(source, "SwitchyRun", again);
    const expected = execFileSync("java", ["-cp", dir.path, "SwitchyDriver"], { encoding: "utf8" });
    const actual = execFileSync("java", ["-cp", `${again}:${dir.path}`, "SwitchyDriver"], {
      encoding: "utf8",
    });
    expect(actual).toEqual(expected);
    expect(actual).not.toEqual("");
  },
);

// The same trap one level out from `i++`: `arr[idx++]` where `idx` is a *field*
// is a getstatic/dup/putstatic, and writing the assignment out first would make
// the read take the new value.
const FIELD_POST_INCREMENT_SOURCE =
  "public class FieldPost {\n" +
  "  static int[] arr = { 5, 6, 7 };\n" +
  "  static int idx = 0;\n" +
  "  static int f() { int v = arr[idx++]; return v * 100 + idx; }\n" +
  "}\n";

test(
  "says so when a field is assigned while it is on the stack",
  { skip: HAS_JAVAC ? false : "no JDK (javac)" },
  () => {
    using dir = TempDir.create("cappu-decompile-fieldpost-");
    const classFile = compileWithJavac(FIELD_POST_INCREMENT_SOURCE, "FieldPost", dir.path);
    expect(decompileToSource(readFileSync(classFile))).toContain(
      "cappu: an assignment with a value that could see it on the stack",
    );
  },
);

// javac puts the tail of a `do` body in the same block as the test, and the jump
// that leaves the inner `switch` lands there: `continue;` would skip the tail, so
// this says so instead of writing one.
const DO_TAIL_SOURCE =
  "public class DoTail {\n" +
  "  static int f(int n) { int r = 0; int i = 0; do { switch (i % 2) { case 0:" +
  " switch (r % 3) { case 0: r += 1; break; case 1: return -1; default: r += 4; break; }" +
  " break; default: r += 100; } i++; } while (i < n); return r; }\n" +
  "}\n";

test(
  "says so when a jump lands in the tail of a do-while body",
  { skip: HAS_JAVAC ? false : "no JDK (javac)" },
  () => {
    using dir = TempDir.create("cappu-decompile-dotail-");
    const classFile = compileWithJavac(DO_TAIL_SOURCE, "DoTail", dir.path);
    expect(decompileToSource(readFileSync(classFile))).toContain(
      "cappu: a jump into the tail of a do-while",
    );
  },
);

// javac writes a `switch` over an enum from another file as a lookup through a
// synthetic `$SwitchMap$` array, held by an anonymous class no source can name.
const ENUM_SOURCE = "public enum Colour { RED, GREEN, BLUE }\n";
const ENUM_SWITCH_SOURCE =
  "public class Painter {\n" +
  "  static int f(Colour c) { switch (c) { case RED: return 1; case GREEN: return 2; default: return 0; } }\n" +
  "}\n";

test(
  "says so when a switch reads javac's enum lookup table",
  { skip: HAS_JAVAC ? false : "no JDK (javac)" },
  () => {
    using dir = TempDir.create("cappu-decompile-enumswitch-");
    compileWithJavac(ENUM_SOURCE, "Colour", dir.path);
    const classFile = compileWithJavac(ENUM_SWITCH_SOURCE, "Painter", dir.path, dir.path);
    expect(decompileToSource(readFileSync(classFile))).toContain("cappu: an enum switch");
  },
);

// javac lays a `for` out with the test at the top and the update at the bottom,
// and a `continue` jumps to that update - which only the `for` form can say.
const FORRY_SOURCE =
  "public class Forry {\n" +
  "  static int simple(int n) { int r = 0; for (int i = 0; i < n; i++) { if (i % 3 == 1) { r += 5; continue; } r += 1; r *= 2; } return r; }\n" +
  "  static int inSwitch(int n) { int r = 0; for (int i = 0; i < n; i++) { switch (i % 3) { case 0: r += 1; break; case 1: continue; default: r += 3; } r *= 2; } return r; }\n" +
  "  static int inSwitchTry(int n) { int r = 0; for (int i = 0; i < n; i++) { switch (i % 2) { case 0: continue; default: r += 3; } } return r; }\n" +
  // Nothing jumps to the update here, so this stays the `while` it reads as.
  "  static int whileForm(int n) { int r = 0; int i = 0; while (i < n) { if (i % 2 == 0) { r += 1; } else { r += 2; } i++; } return r; }\n" +
  // A `continue` *and* a case that returns: the end of the statement around this
  // one is the update, and the code after the switch is reached from two cases,
  // so only the block every case leads to can be where the switch ends.
  "  static int switchExits(int n, int x) { int r = 0; for (int i = 0; i < n; i++) { switch (x) { case 1: continue; case 2: r += 1; break; case 3: return -1; default: r += 3; } r *= 2; } return r; }\n" +
  "}\n";

test(
  "recompiles javac's own `for` loops to the same bytecode",
  { skip: HAS_JAVAC && HAS_JAVAP ? false : "no JDK (javac/javap)" },
  () => {
    using dir = TempDir.create("cappu-decompile-forry-");
    const classFile = compileWithJavac(FORRY_SOURCE, "Forry", dir.path);
    const source = decompileToSource(readFileSync(classFile));
    expect(source).not.toContain("/* cappu:");
    expect(source).toContain("for (; var2 < arg0; var2++) {");
    const roundTripped = compileWithJavac(source, "Forry", join(dir.path, "again"));
    expect(javap(roundTripped)).toEqual(javap(classFile));
  },
);

// The update of a `for` is a list of expressions. When the block at the bottom
// of the body is not that - an allocation whose value is dropped, or an
// assignment a later retype still has to reach - it is not an update clause, and
// the statements belong at the end of the body, where the `while` form puts them.
const NOT_AN_UPDATE_SOURCE =
  "public class NotAnUpdate {\n" +
  "  static int dropped(int n, StringBuilder out) { int r = 0; int i = 0;" +
  " while (i < n) { if (i % 2 == 0) { r += 1; } else { r += 2; }" +
  ' new StringBuilder("x").append(i).toString(); out.append(i); i++; } return r; }\n' +
  "  static boolean retyped(int n) { boolean b = false; int i = 0;" +
  " while (i < n) { if (i % 2 == 0) { i += 1; } else { i += 3; } b = true; i++; } return b; }\n" +
  "}\n";

test(
  "keeps a loop tail that is not an update clause",
  { skip: HAS_JAVAC && HAS_JAVAP ? false : "no JDK (javac/javap)" },
  () => {
    using dir = TempDir.create("cappu-decompile-notanupdate-");
    const classFile = compileWithJavac(NOT_AN_UPDATE_SOURCE, "NotAnUpdate", dir.path);
    const source = decompileToSource(readFileSync(classFile));
    expect(source).not.toContain("/* cappu:");
    // The dropped allocation is a statement of the body, not an update.
    expect(source).toContain(".toString();");
    expect(source).toContain("var1 = true;");
    const roundTripped = compileWithJavac(source, "NotAnUpdate", join(dir.path, "again"));
    expect(javap(roundTripped)).toEqual(javap(classFile));
  },
);

// A `continue` whose arm is the whole `if` comes back as the inverted test that
// runs the rest - the same thing, other bytecode - and a nested loop's variable
// is hoisted, so these can only be judged by running them.
const FORRY_RUN_SOURCE =
  "public class ForryRun {\n" +
  "  static int twoUpdates(int n) { int r = 0; for (int i = 0, j = n; i < j; i++, j--) { if (i == 2) { continue; } r += i * j; } return r; }\n" +
  "  static int twoContinues(int n) { int r = 0; for (int i = 0; i < n; i++) { if (i == 1) { continue; } if (i == 3) { r += 7; continue; } r += 1; } return r; }\n" +
  "  static int nested(int n) { int r = 0; for (int i = 0; i < n; i++) { for (int j = 0; j < n; j++) { if (j == 1) { continue; } r += i + j; } r += 1; } return r; }\n" +
  "  static int inWhile(int n) { int r = 0; int i = 0; while (i < n) { i = i + 1; if (i == 2) { continue; } r += i; } return r; }\n" +
  // Nothing to update: the jump back to the test stands in a block of its own,
  // which is still where a `continue` goes. The loop variable is declared in the
  // body, so only running it can say the two are the same.
  "  static int search(int[] a, int key) { int low = 0; int high = a.length - 1; while (low <= high) { int mid = (low + high) >>> 1; if (a[mid] < key) { low = mid + 1; } else if (a[mid] > key) { high = mid - 1; } else { return mid; } } return -(low + 1); }\n" +
  "}\n";

const FORRY_DRIVER_SOURCE =
  "public class ForryDriver {\n" +
  "  public static void main(String[] args) {\n" +
  "    for (int n = 0; n < 8; n++) {\n" +
  '      System.out.println(n + " " + ForryRun.twoUpdates(n) + " " + ForryRun.twoContinues(n)\n' +
  '        + " " + ForryRun.nested(n) + " " + ForryRun.inWhile(n)\n' +
  '        + " " + ForryRun.search(new int[] { 0, 2, 4, 6, 8 }, n));\n' +
  "    }\n" +
  "  }\n" +
  "}";

test(
  "runs like javac's own `for` loops when the shape cannot line up",
  { skip: HAS_JAVAC && HAS_JAVA ? false : "no JDK (javac/java)" },
  () => {
    using dir = TempDir.create("cappu-decompile-forryrun-");
    const classFile = compileWithJavac(FORRY_RUN_SOURCE, "ForryRun", dir.path);
    compileWithJavac(FORRY_DRIVER_SOURCE, "ForryDriver", dir.path, dir.path);
    const source = decompileToSource(readFileSync(classFile));
    expect(source).not.toContain("/* cappu:");
    const again = join(dir.path, "again");
    compileWithJavac(source, "ForryRun", again);
    const expected = execFileSync("java", ["-cp", dir.path, "ForryDriver"], { encoding: "utf8" });
    const actual = execFileSync("java", ["-cp", `${again}:${dir.path}`, "ForryDriver"], {
      encoding: "utf8",
    });
    expect(actual).toEqual(expected);
    expect(actual).not.toEqual("");
  },
);

// javac writes `synchronized` as a monitor held in a synthetic local, guarded by
// a catch-all that releases it and rethrows - and splits the range around every
// `return`, `break` and `continue` that leaves the statement.
const SYNCY_SOURCE =
  "public class Syncy {\n" +
  "  private final Object lock = new Object();\n" +
  "  int n;\n" +
  "  int simple() { synchronized (lock) { n = n + 1; } return n; }\n" +
  "  int early(int x) { synchronized (lock) { if (x > 0) { return 1; } n = n + 2; } return n; }\n" +
  "  int onThis() { synchronized (this) { n = n + 3; } return n; }\n" +
  "  int nested(Object other) { synchronized (lock) { synchronized (other) { n = n + 4; } } return n; }\n" +
  "  int allReturn() { synchronized (lock) { return n; } }\n" +
  '  int throwing() { synchronized (lock) { if (n == 0) { throw new IllegalStateException("x"); } return n; } }\n' +
  '  int withTry() { synchronized (lock) { try { return Integer.parseInt("7"); } catch (NumberFormatException e) { return -1; } } }\n' +
  "  int inTry() { try { synchronized (lock) { n = n + 1; } } catch (RuntimeException e) { return -1; } return n; }\n" +
  "  static int stat(Object o) { synchronized (o) { return o.hashCode(); } }\n" +
  "  int twice() { synchronized (lock) { n = n + 1; } synchronized (lock) { n = n + 2; } return n; }\n" +
  // javac frees the monitor's local when the statement ends and hands the slot
  // to the next variable: reading it back as the monitor would lose that one.
  '  int reused(Object o) { synchronized (o) { n = n + 1; } String s = "hello"; return s.length() + n; }\n' +
  '  int shared(Object o) { String before = "a"; synchronized (o) { n = before.length(); } String after = "bb"; return n + after.length(); }\n' +
  // A `break` and a `continue` out of the statement: javac releases the monitor
  // first, and the merge of an `if` in the body is not past the release.
  "  int breakOut(int k) { int r = 0; for (int i = 0; i < k; i++) { synchronized (lock) { if (i == 2) { break; } r = r + i; } } return r; }\n" +
  "  int continueOut(int k) { int r = 0; for (int i = 0; i < k; i++) { synchronized (lock) { if (i == 2) { continue; } r = r + i; } r = r * 2; } return r; }\n" +
  "  synchronized int flagged() { return n; }\n" +
  "}\n";

test(
  "recompiles javac's own `synchronized` blocks to the same bytecode",
  { skip: HAS_JAVAC && HAS_JAVAP ? false : "no JDK (javac/javap)" },
  () => {
    using dir = TempDir.create("cappu-decompile-syncy-");
    const classFile = compileWithJavac(SYNCY_SOURCE, "Syncy", dir.path);
    const source = decompileToSource(readFileSync(classFile));
    expect(source).not.toContain("/* cappu:");
    expect(source).toContain("synchronized (this.lock) {");
    // The monitor local javac copies the expression into is not a variable.
    expect(source).not.toContain("monitorexit");
    const roundTripped = compileWithJavac(source, "Syncy", join(dir.path, "again"));
    expect(javap(roundTripped)).toEqual(javap(classFile));
  },
);

// javac writes the body of a `finally` twice - once on the way out of the
// protected range, once in the catch-all that rethrows - and a `return` inside
// the body is one more copy. One way out is what this reads back; the rest say so.
const FINALLY_SOURCE =
  "public class Finallies {\n" +
  "  static int n;\n" +
  "  static int simple(int x) { int r = 0; try { r = 10 / x; } finally { n += 1; } return r; }\n" +
  '  static int several(int x) { int r = 0; try { r = 10 / x; n += 2; } finally { System.out.print(""); } return r; }\n' +
  "  static int inLoop(int x) { int r = 0; for (int i = 0; i < x; i++) { try { r += 10 / (x - i); } finally { n += 5; } } return r; }\n" +
  "}\n";

const FINALLY_BAILS_SOURCE =
  "public class Bails {\n" +
  "  static int n;\n" +
  "  static int returning(int a) { try { return a; } finally { n += 1; } }\n" +
  // A `catch` beside the `finally`, and a `finally` inside one: each is another
  // copy of the body, and which one source wrote is not in the class file.
  "  static int caught(int x) { int r = 0; try { r = 10 / x; } catch (ArithmeticException e) { r = -1; } finally { n += 2; } return r; }\n" +
  "  static int nested(int x) { int r = 0; try { try { r = 10 / x; } finally { n += 3; } } finally { n += 4; } return r; }\n" +
  "}\n";

test(
  "reconstructs a `finally` with one way out",
  { skip: HAS_JAVAC && HAS_JAVA ? false : "no JDK (javac/java)" },
  () => {
    using dir = TempDir.create("cappu-decompile-finally-");
    const classFile = compileWithJavac(FINALLY_SOURCE, "Finallies", dir.path);
    const source = decompileToSource(readFileSync(classFile));
    expect(source).not.toContain("/* cappu:");
    expect(source).toContain("} finally {");
    // The copy javac wrote on the way out is not a statement of its own.
    expect(source.match(/n = n \+ 1;/g)?.length).toBe(1);
    const again = join(dir.path, "again");
    compileWithJavac(source, "Finallies", again);
    const driver =
      "public class FinalliesDriver {\n" +
      "  public static void main(String[] args) {\n" +
      "    for (int x = -2; x < 4; x++) {\n" +
      "      String line;\n" +
      "      try {\n" +
      '        line = Finallies.simple(x) + " " + Finallies.several(x)\n' +
      '          + " " + Finallies.inLoop(x);\n' +
      '      } catch (RuntimeException e) { line = "ex"; }\n' +
      '      System.out.println(line + " " + Finallies.n);\n' +
      "    }\n" +
      "  }\n" +
      "}";
    compileWithJavac(driver, "FinalliesDriver", dir.path, dir.path);
    const expected = execFileSync("java", ["-cp", dir.path, "FinalliesDriver"], {
      encoding: "utf8",
    });
    const actual = execFileSync("java", ["-cp", `${again}:${dir.path}`, "FinalliesDriver"], {
      encoding: "utf8",
    });
    expect(actual).toEqual(expected);
    expect(actual).not.toEqual("");
  },
);

test(
  "says so when a `finally` has more than one way out",
  { skip: HAS_JAVAC ? false : "no JDK (javac)" },
  () => {
    using dir = TempDir.create("cappu-decompile-finallybails-");
    const classFile = compileWithJavac(FINALLY_BAILS_SOURCE, "Bails", dir.path);
    const source = decompileToSource(readFileSync(classFile));
    expect(source).toContain("cappu: a finally with more than one way out");
    // Every one of them says so rather than guessing which copy source wrote.
    expect(source.match(/cappu: /g)?.length).toBe(6);
  },
);

// javac compiles a lambda into a synthetic method plus an invokedynamic that
// `LambdaMetafactory` turns into the interface; a method reference points the
// same call site straight at the method. The body comes back inlined, so javac
// generates the same method from it again.
const LAMMY_SOURCE =
  "import java.util.*;\n" +
  "import java.util.function.*;\n" +
  "public class Lammy {\n" +
  "  int base = 5;\n" +
  "  static int stat = 7;\n" +
  '  Runnable noCapture() { return () -> System.out.print("x"); }\n' +
  "  Runnable capture(int n) { return () -> System.out.print(n); }\n" +
  "  Supplier<Integer> field() { return () -> base; }\n" +
  "  IntUnaryOperator math(int k) { return x -> x * k + base; }\n" +
  "  Function<String, Integer> unboundRef() { return String::length; }\n" +
  "  Supplier<String> boundRef(String s) { return s::trim; }\n" +
  "  Supplier<Object> ctorRef() { return Object::new; }\n" +
  "  static Runnable staticRef() { return Lammy::helper; }\n" +
  "  static void helper() {}\n" +
  "  BiFunction<Integer, Integer, Integer> two(int k) { return (a, b) -> a + b + k; }\n" +
  "  int localCapture(int n) { int k = n * 2; Supplier<Integer> s = () -> k + base; return s.get(); }\n" +
  // A lambda whose body is a block, and one that returns another lambda - the
  // interface erases both, so the reconstruction has to name them.
  "  Supplier<Integer> block(int n) { return () -> { int t = n; t = t * 3; return t + base; }; }\n" +
  "  Function<Integer, Supplier<Integer>> nested(int n) { return a -> () -> a + n; }\n" +
  "  int stream(List<String> xs) { return xs.stream().map(String::length).reduce(0, Integer::sum); }\n" +
  "  Comparator<String> comparator() { return (a, b) -> a.length() - b.length(); }\n" +
  "  Runnable staticField() { return () -> stat++; }\n" +
  // A body that is one *statement* and not one expression needs the braces, and
  // a lambda written where nothing says the interface has to name it.
  '  Runnable throwing() { return () -> { throw new RuntimeException("x"); }; }\n' +
  "  Runnable declaring() { return () -> { int q = 3; System.out.print(q); }; }\n" +
  '  String receiver() { return ((Supplier<String>) () -> "sup").get(); }\n' +
  '  Object arm(boolean c) { return c ? (Runnable) () -> System.out.print("1") : (Runnable) () -> System.out.print("2"); }\n' +
  "}\n";

test(
  "reconstructs javac's own lambdas and method references",
  { skip: HAS_JAVAC && HAS_JAVA ? false : "no JDK (javac/java)" },
  () => {
    using dir = TempDir.create("cappu-decompile-lammy-");
    const classFile = compileWithJavac(LAMMY_SOURCE, "Lammy", dir.path);
    const source = decompileToSource(readFileSync(classFile));
    expect(source).not.toContain("/* cappu:");
    // A reference where source wrote one, a lambda where the body is inlined,
    // and nothing left of the synthetic method javac generated.
    expect(source).toContain("java.lang.Object::new");
    expect(source).toContain("Lammy::helper");
    expect(source).toContain("() -> java.lang.System.out.print(arg0)");
    expect(source).not.toContain("lambda$");
    const again = join(dir.path, "again");
    compileWithJavac(source, "Lammy", again);
    // The lambdas have to run, not just compile: the captured values and the
    // interface method's own parameters are bound in one order only.
    const driver =
      "import java.util.*;\n" +
      "public class LammyDriver {\n" +
      "  public static void main(String[] args) {\n" +
      '    List<String> xs = Arrays.asList("a", "bb", "", "cccc");\n' +
      "    for (int n = 0; n < 4; n++) {\n" +
      "      Lammy l = new Lammy();\n" +
      "      l.noCapture().run();\n" +
      "      l.capture(n).run();\n" +
      "      l.staticRef().run();\n" +
      "      l.staticField().run();\n" +
      '      System.out.println(" " + l.field().get() + " " + l.math(n).applyAsInt(3)\n' +
      '        + " " + l.unboundRef().apply("abcd") + " " + l.boundRef(" q ").get()\n' +
      '        + " " + l.ctorRef().get().getClass() + " " + l.two(n).apply(1, 2)\n' +
      '        + " " + l.localCapture(n) + " " + l.block(n).get()\n' +
      '        + " " + l.nested(n).apply(3).get() + " " + l.stream(xs)\n' +
      '        + " " + l.comparator().compare("aa", "b"));\n' +
      "    }\n" +
      "  }\n" +
      "}";
    compileWithJavac(driver, "LammyDriver", dir.path, dir.path);
    const expected = execFileSync("java", ["-cp", dir.path, "LammyDriver"], { encoding: "utf8" });
    const actual = execFileSync("java", ["-cp", `${again}:${dir.path}`, "LammyDriver"], {
      encoding: "utf8",
    });
    expect(actual).toEqual(expected);
    expect(actual).not.toEqual("");
  },
);

// A variable declared inside a loop is effectively final in source, but this
// hoists the declaration to the top of the method, and what is left behind is an
// assignment that runs once per turn - which Java will not let a lambda capture.
// Scoped declarations are what would take these.
const HOISTED_CAPTURE_SOURCE =
  "import java.util.function.*;\n" +
  "public class Hoisted {\n" +
  "  static int f(int n) { int t = 0; for (int i = 0; i < n; i++) { int j = i; Supplier<Integer> s = () -> j * 2; t += s.get(); } return t; }\n" +
  "}\n";

test(
  "says so when a lambda captures a variable that cannot be final",
  { skip: HAS_JAVAC ? false : "no JDK (javac)" },
  () => {
    using dir = TempDir.create("cappu-decompile-hoisted-");
    const classFile = compileWithJavac(HOISTED_CAPTURE_SOURCE, "Hoisted", dir.path);
    expect(decompileToSource(readFileSync(classFile))).toContain(
      "cappu: a lambda that captures a variable that is not final",
    );
  },
);

// A `while (true)` opens with the block a `continue` jumps to, and a
// `synchronized` inside one keeps the `return` javac writes in it: neither is
// where the loop ends.
const FOREVER_SOURCE =
  "public class Forever {\n" +
  "  static final Object L = new Object();\n" +
  "  static int broke(int n) { int r = 0; while (true) { r += n; if (r > 100) { break; } n++; } return r; }\n" +
  "  static int held(int p) { int n = p; while (true) { synchronized (L) { if (n > 3) { return n; } n = n + 1; } } }\n" +
  "  static int leaves(int p) { int n = p; for (;;) { synchronized (L) { if (n > 3) { break; } } n = n + 1; } return n; }\n" +
  "  static int inside(int p) { int n = p; synchronized (L) { while (n < 4) { n = n + 1; } n = n * 2; } return n; }\n" +
  "}\n";

test(
  "reconstructs a `while (true)` and what a `synchronized` in one holds",
  { skip: HAS_JAVAC && HAS_JAVA ? false : "no JDK (javac/java)" },
  () => {
    using dir = TempDir.create("cappu-decompile-forever-");
    const classFile = compileWithJavac(FOREVER_SOURCE, "Forever", dir.path);
    const source = decompileToSource(readFileSync(classFile));
    expect(source).not.toContain("/* cappu:");
    expect(source).toContain("while (true) {");
    const again = join(dir.path, "again");
    compileWithJavac(source, "Forever", again);
    const driver =
      "public class ForeverDriver {\n" +
      "  public static void main(String[] args) {\n" +
      "    for (int p = 0; p < 7; p++) {\n" +
      '      System.out.println(Forever.broke(p) + " " + Forever.held(p) + " " + Forever.leaves(p)\n' +
      '        + " " + Forever.inside(p));\n' +
      "    }\n" +
      "  }\n" +
      "}";
    compileWithJavac(driver, "ForeverDriver", dir.path, dir.path);
    const expected = execFileSync("java", ["-cp", dir.path, "ForeverDriver"], { encoding: "utf8" });
    const actual = execFileSync("java", ["-cp", `${again}:${dir.path}`, "ForeverDriver"], {
      encoding: "utf8",
    });
    expect(actual).toEqual(expected);
    expect(actual).not.toEqual("");
  },
);

// The guards a reconstruction rests on, each of which said nothing when it was
// removed: a `Serializable` lambda is `altMetafactory` and carries flags this
// drops; one statement that is a *declaration* still needs the braces; a
// captured field is read again every time the lambda runs, where javac read it
// once; and a loop over a `synchronized` needs the handler's edge to find its
// own end.
const GUARD_SOURCE =
  "import java.io.Serializable;\n" +
  "import java.util.function.*;\n" +
  "public class Guard {\n" +
  "  static final Object L = new Object();\n" +
  '  String name = "one";\n' +
  "  interface SRun extends Runnable, Serializable {}\n" +
  '  static SRun serial() { return (SRun) () -> System.out.print("s"); }\n' +
  "  static Runnable declaring() { return () -> { int q = 3; }; }\n" +
  "  Supplier<String> boundField() { return name::toUpperCase; }\n" +
  "  static int hooks(int n) { int r = 0; for (int i = 0; i < n; i++) { synchronized (L) { if (i == 2) { return r; } r += i; } } return r; }\n" +
  // A `synchronized` inside a `try` inside a loop: without the monitor
  // handler's edge, the loop's dominators collapse and this is irreducible.
  "  static final int[] taken = { 1, 0, 3 };\n" +
  "  static int runHooks() { int r = 0; for (int i = 0; i < taken.length; i++) { try { int hook; synchronized (L) { hook = taken[i]; } if (hook != 0) { r += 10 / hook; } } catch (RuntimeException t) { r -= 1; } } return r; }\n" +
  "}\n";

test(
  "keeps the guards a lambda and a monitor rest on",
  { skip: HAS_JAVAC ? false : "no JDK (javac)" },
  () => {
    using dir = TempDir.create("cappu-decompile-guard-");
    const classFile = compileWithJavac(GUARD_SOURCE, "Guard", dir.path);
    const source = decompileToSource(readFileSync(classFile));
    expect(source).toContain(
      "cappu: an invokedynamic that is neither a lambda nor a concatenation",
    );
    expect(source).toContain("cappu: a lambda that captures more than a variable");
    // The declaration keeps its braces, and the loop over the monitor is written.
    expect(source).toContain("() -> {");
    expect(source).toContain("synchronized (L) {");
    // The loop over a `try` over a monitor is reducible only with the handler's
    // edge in the graph.
    expect(source).not.toContain("cappu: irreducible control flow");
  },
);

// javac writes a compound assignment by *copying* the target on the stack -
// `dup2` for an array element, `dup` for a field - and reads it back through the
// copy. The long form is what comes back, which is the same thing as long as the
// array and the index are read the same way twice.
const COMPOUND_SOURCE =
  "public class Compound {\n" +
  "  static int[] a = { 1, 2, 3 };\n" +
  "  static long[] longs = { 1L };\n" +
  "  int n;\n" +
  "  static int s;\n" +
  "  static void arrPlus(int i, int x) { a[i] += x; }\n" +
  "  static void arrInc(int i) { a[i]++; }\n" +
  "  static void arrPre(int i) { ++a[i]; }\n" +
  "  static void arrShift(int i) { a[i] <<= 2; }\n" +
  "  static void longPlus(int i, long x) { longs[i] += x; }\n" +
  "  void fieldPlus(int x) { n += x; }\n" +
  "  void fieldInc() { n++; }\n" +
  "  static void statPlus(int x) { s += x; }\n" +
  "  static void statInc() { s++; }\n" +
  "}\n";

// The *value* of a post-increment is the old one, and the long form reads the
// new one: those need the assignment to stay an expression, which it does not.
const COMPOUND_BAILS_SOURCE =
  "public class Both {\n" +
  "  static int[] a = { 1, 2, 3 };\n" +
  "  int n;\n" +
  "  static int arrValue(int i) { return a[i]++; }\n" +
  "  int fieldValue() { return n++; }\n" +
  "}\n";

test(
  "reconstructs a compound assignment to a field and an array element",
  { skip: HAS_JAVAC && HAS_JAVA ? false : "no JDK (javac/java)" },
  () => {
    using dir = TempDir.create("cappu-decompile-compound-");
    const classFile = compileWithJavac(COMPOUND_SOURCE, "Compound", dir.path);
    const source = decompileToSource(readFileSync(classFile));
    expect(source).not.toContain("/* cappu:");
    expect(source).toContain("a[arg0] = a[arg0] + arg1;");
    const again = join(dir.path, "again");
    compileWithJavac(source, "Compound", again);
    const driver =
      "public class CompoundDriver {\n" +
      "  public static void main(String[] args) {\n" +
      "    for (int i = 0; i < 3; i++) {\n" +
      "      Compound c = new Compound();\n" +
      "      Compound.arrPlus(i, 5); Compound.arrInc(i); Compound.arrPre(i);\n" +
      "      Compound.arrShift(i); Compound.longPlus(0, 7L);\n" +
      "      c.fieldPlus(3); c.fieldInc(); Compound.statPlus(2); Compound.statInc();\n" +
      '      System.out.println(Compound.a[0] + " " + Compound.a[1] + " " + Compound.a[2]\n' +
      '        + " " + c.n + " " + Compound.s + " " + Compound.longs[0]);\n' +
      "    }\n" +
      "  }\n" +
      "}";
    compileWithJavac(driver, "CompoundDriver", dir.path, dir.path);
    const expected = execFileSync("java", ["-cp", dir.path, "CompoundDriver"], {
      encoding: "utf8",
    });
    const actual = execFileSync("java", ["-cp", `${again}:${dir.path}`, "CompoundDriver"], {
      encoding: "utf8",
    });
    expect(actual).toEqual(expected);
    expect(actual).not.toEqual("");
  },
);

test(
  "says so when the value of a post-increment is used",
  { skip: HAS_JAVAC ? false : "no JDK (javac)" },
  () => {
    using dir = TempDir.create("cappu-decompile-compoundbails-");
    const classFile = compileWithJavac(COMPOUND_BAILS_SOURCE, "Both", dir.path);
    const source = decompileToSource(readFileSync(classFile));
    // Both of them, and both for the same reason: the store would have to run
    // in front of the value the post-increment yields.
    const guarded = source.match(/an assignment with a value that could see it on the stack/g);
    expect(guarded?.length).toBe(2);
  },
);

// A record declares its state in the header, and javac writes the accessors, the
// canonical constructor and `equals`/`hashCode`/`toString` (through the
// `ObjectMethods` bootstrap) from it. Those come back as the header; anything the
// source added stays.
// A hand-written accessor or canonical constructor may be *smaller* than the one
// javac generates, so only their shape tells them apart: reading another
// component, or storing in another order, is the source's and stays.
const RECORD_KEPT_SOURCE =
  "public class Kept {\n" +
  "  public record Accessor(int x, int y) { public int x() { return y; } }\n" +
  "  public record Swapped(int x, int y) { public Swapped(int x, int y) { this.x = y; this.y = x; } }\n" +
  "  public record Negated(int x) { public Negated(int x) { this.x = -x; } }\n" +
  "  public record Wide(long a, String b, double c) { public int extra() { return 1; } }\n" +
  "}\n";

test(
  "keeps a record member that only looks generated",
  { skip: HAS_JAVAC && HAS_JAVA ? false : "no JDK (javac/java)" },
  () => {
    using dir = TempDir.create("cappu-decompile-recordkept-");
    compileWithJavac(RECORD_KEPT_SOURCE, "Kept", dir.path);
    for (const [name, wanted] of [
      ["Kept$Accessor", "return this.y;"],
      ["Kept$Swapped", "this.x = y;"],
      ["Kept$Negated", "this.x = -x;"],
    ] as const) {
      const source = decompileToSource(readFileSync(join(dir.path, `${name}.class`)));
      expect(source).toContain(wanted);
    }
    // The generated members of a record whose components are wide are still
    // recognised - the slots they load from are two apart.
    const wide = decompileToSource(readFileSync(join(dir.path, "Kept$Wide.class")));
    expect(wide).toContain("record Kept$Wide(long a, java.lang.String b, double c)");
    expect(wide).not.toContain("public long a()");
  },
);

const RECORD_SOURCE =
  "public record Recordy(int x, String name, long[] data) {\n" +
  "  static int made;\n" +
  "  public Recordy {\n" +
  '    if (x < 0) { throw new IllegalArgumentException("x"); }\n' +
  "  }\n" +
  "  public int twice() { return x * 2; }\n" +
  '  public static Recordy of(int x) { made++; return new Recordy(x, "n", new long[] { 1L }); }\n' +
  "}\n";

test(
  "reconstructs a record from its components",
  { skip: HAS_JAVAC && HAS_JAVA ? false : "no JDK (javac/java)" },
  () => {
    using dir = TempDir.create("cappu-decompile-record-");
    const classFile = compileWithJavac(RECORD_SOURCE, "Recordy", dir.path);
    const source = decompileToSource(readFileSync(classFile));
    expect(source).not.toContain("/* cappu:");
    expect(source).toContain("public record Recordy(int x, java.lang.String name, long[] data) {");
    // The three `ObjectMethods` members and the accessors are the header, and a
    // component may not be declared as a field on top of it.
    expect(source).not.toContain("hashCode()");
    expect(source).not.toContain("private final int x;");
    // The canonical constructor did more than store, so it stays - named after
    // the components, which Java checks.
    expect(source).toContain("public Recordy(int x, java.lang.String name, long[] data) {");
    const again = join(dir.path, "again");
    compileWithJavac(source, "Recordy", again);
    const driver =
      "public class RecordyDriver {\n" +
      "  public static void main(String[] args) {\n" +
      "    for (int i = 0; i < 3; i++) {\n" +
      "      Recordy r = Recordy.of(i);\n" +
      '      System.out.println(r + " " + r.x() + " " + r.name() + " " + r.twice()\n' +
      '        + " " + r.hashCode() + " " + r.equals(Recordy.of(i)) + " " + Recordy.made);\n' +
      "    }\n" +
      '    try { new Recordy(-1, "n", null); } catch (RuntimeException e) {\n' +
      '      System.out.println("caught " + e.getMessage());\n' +
      "    }\n" +
      "  }\n" +
      "}";
    compileWithJavac(driver, "RecordyDriver", dir.path, dir.path);
    const expected = execFileSync("java", ["-cp", dir.path, "RecordyDriver"], { encoding: "utf8" });
    const actual = execFileSync("java", ["-cp", `${again}:${dir.path}`, "RecordyDriver"], {
      encoding: "utf8",
    });
    expect(actual).toEqual(expected);
    expect(actual).not.toEqual("");
  },
);

// An assignment written out as a statement runs in front of everything the stack
// already holds, and a value that reads a field or an array element may be the
// one being written - under this name or another. Only locals and literals are
// safe, so the rest say so.
const ALIAS_SOURCE =
  "public class Aliased {\n" +
  "  static int[] a = { 0, 0, 0 };\n" +
  "  int x;\n" +
  "  static int reads;\n" +
  "  static int rd() { reads++; return a[0]; }\n" +
  "  static int aliasArray() { int[] b = a; a[0] = 1; return a[0] + (b[0] = 5); }\n" +
  "  static int sameIndex() { int i = 0, j = 0; a[1] = 1; return a[i] + (a[j] = 7); }\n" +
  "  int sameObject(Aliased that) { this.x = 1; return this.x + (that.x = 9); }\n" +
  "  static int throughCall() { return rd() + (a[0] = 7); }\n" +
  "  static int chained() { int p, q; p = q = 5; return p + q; }\n" +
  "}\n";

test(
  "says so when an assignment would move in front of a value that could see it",
  { skip: HAS_JAVAC ? false : "no JDK (javac)" },
  () => {
    using dir = TempDir.create("cappu-decompile-alias-");
    const classFile = compileWithJavac(ALIAS_SOURCE, "Aliased", dir.path);
    const source = decompileToSource(readFileSync(classFile));
    const guarded = source.match(/an assignment with a value that could see it on the stack/g);
    expect(guarded?.length).toBe(4);
    // A chained assignment holds only the literal it copied, which sees nothing.
    expect(source).toContain("int var1 = 5;");
  },
);

/** Compile one source file with javac and return the .class path. */
function compileWithJavac(
  source: string,
  name: string,
  outDir: string,
  classPath?: string,
): string {
  mkdirSync(outDir, { recursive: true });
  const javaFile = join(outDir, `${name}.java`);
  writeFileSync(javaFile, source);
  const path = classPath === undefined ? [] : ["-cp", classPath];
  execFileSync("javac", ["--release", "21", "-d", outDir, ...path, javaFile], { stdio: "pipe" });
  return join(outDir, `${name}.class`);
}

function javap(classFile: string): string {
  return execFileSync("javap", ["-c", "-p", classFile], { encoding: "utf8" });
}

// --- shapes the reconstruction has to get right ---------------------------------------

// Each case is emitted by our own emitter, decompiled, and held to the text it
// has to produce; `selfContained` cases are type-checked on top (the others
// reference a class that lives in another file).
const RECONSTRUCTIONS: {
  name: string;
  source: string;
  expect: string[];
  reject?: string[];
  selfContained?: boolean;
}[] = [
  {
    name: "Neg",
    source: "class Neg { static int f(int a) { return -(-a); } }",
    expect: ["return -(-arg0);"], // `--arg0` would decrement it
    selfContained: true,
  },
  {
    name: "Jag",
    source:
      "class Jag { static java.lang.String[][] f(int n) { return new java.lang.String[n][]; } }",
    expect: ["new java.lang.String[arg0][]"],
    selfContained: true,
  },
  {
    name: "Ctors",
    // A no-arg constructor is only javac's when it is the only one.
    source: "class Ctors { int v; Ctors() { this.v = 1; } Ctors(int x) { this.v = x; } }",
    expect: ["Ctors() {", "Ctors(int arg0) {"],
    selfContained: true,
  },
  {
    name: "Single",
    source: "class Single { private Single() {} }",
    expect: ["private Single() {"], // dropping it would make the class instantiable
    selfContained: true,
  },
  {
    name: "Erased",
    // Only the use says these are not ints: the store opcode is the same.
    source:
      "class Erased { static boolean b() { boolean v = true; return v; }" +
      " static char c() { char v = 'a'; return v; } }",
    expect: ["boolean var0 = true;", "char var0 = 'a';"],
    selfContained: true,
  },
  // --- phase 1.8: array initializers ---
  {
    name: "ArrLit",
    // The `dup; index; value; store` chain is one literal, not three statements.
    source: "class ArrLit { static int[] f() { return new int[]{1, 2}; } }",
    expect: ["return new int[] {1, 2};"],
    reject: ["new int[2]"],
    selfContained: true,
  },
  {
    name: "ArrSized",
    // No `dup`, so this stays the sized form with the write as a statement.
    source: "class ArrSized { static int[] f() { int[] a = new int[2]; a[1] = 3; return a; } }",
    expect: ["int[] var0 = new int[2];", "var0[1] = 3;"],
    selfContained: true,
  },
  {
    name: "ArrNest",
    source: "class ArrNest { static int[][] f() { return new int[][]{{1}, {2}}; } }",
    expect: ["return new int[][] {new int[] {1}, new int[] {2}};"],
    selfContained: true,
  },
  // --- phase 1.4: acyclic control flow ---
  {
    name: "IfOnly",
    source: "class IfOnly { static int f(int a) { int r = 0; if (a > 0) { r = a; } return r; } }",
    expect: ["int var1 = 0;", "if (arg0 > 0) {", "var1 = arg0;"],
    selfContained: true,
  },
  {
    name: "IfElse",
    // The arm that leaves the method is the whole `if`; the rest follows it.
    source: "class IfElse { static int f(int a) { if (a > 0) return 1; else return 2; } }",
    expect: ["if (arg0 > 0) {", "return 1;", "}", "return 2;"],
    reject: ["} else {"],
    selfContained: true,
  },
  {
    name: "Hoist",
    // Java scopes a variable to the branch it is declared in, the bytecode does
    // not: assigned in both arms, it has to be declared before the `if`.
    source:
      "class Hoist { static int f(boolean c) { int x; if (c) { x = 1; } else { x = 2; } return x; } }",
    expect: ["int var1;", "if (arg0) {", "var1 = 1;", "} else {", "var1 = 2;"],
    selfContained: true,
  },
  {
    name: "Short",
    source:
      "class Short { static boolean f(int a, int b) { return a > 0 && b < 10; }" +
      " static boolean g(int a, int b) { return a > 0 || b < 10; } }",
    expect: ["return arg0 > 0 && arg1 < 10;", "return arg0 > 0 || arg1 < 10;"],
    selfContained: true,
  },
  {
    name: "Mixed",
    // Nested short-circuits share the block the value comes from, so the
    // parenthesization is the only thing that says which grouping was written.
    source:
      "class Mixed { static boolean f(int a, int b, int c) { return (a > 0 && b > 0) || c > 0; }" +
      " static boolean g(int a, int b, int c) { return a > 0 && (b > 0 || c > 0); } }",
    expect: [
      "return arg0 > 0 && arg1 > 0 || arg2 > 0;",
      "return arg0 > 0 && (arg1 > 0 || arg2 > 0);",
    ],
    selfContained: true,
  },
  {
    name: "Tern",
    source:
      "class Tern { static int f(int a, int b) { return (a > b ? a : b) + 1; }" +
      " static int g(boolean c) { int[] xs = new int[3]; xs[c ? 0 : 1] = 7; return xs[0]; } }",
    // The condition is a boolean, so an index has to ask for the int back - and
    // the int form keeps the branch's own arms, not the boolean reading of them.
    expect: ["return (arg0 > arg1 ? arg0 : arg1) + 1;", "var1[arg0 ? 0 : 1] = 7;"],
    selfContained: true,
  },
  {
    name: "BoolVar",
    // `istore` is what an int uses, so a materialized condition starts as one -
    // and a use that needs a boolean (`return b`) is what narrows it. With no
    // such use it stays an int, which still compiles and still branches.
    source:
      "class BoolVar { static boolean f(int a) { boolean b = a > 10; if (b) { return b; } return false; }" +
      " static int g(int a) { boolean b = a > 10; if (b) { return 1; } return 0; }" +
      " static int h(int a) { int x = a > 0 ? 1 : 0; return x + 1; } }",
    expect: [
      "boolean var1 = arg0 > 10;",
      "if (var1) {",
      // Our emitter branches on the true arm, so the int form reads inverted -
      // the same value, written the way *this* branch is laid out.
      "int var1 = arg0 <= 10 ? 0 : 1;",
      "if (var1 != 0) {",
      "int var1 = arg0 > 0 ? 1 : 0;",
      "return var1 + 1;",
    ],
    selfContained: true,
  },
  {
    name: "AsNumber",
    // A materialized condition in a position that wants a number: arithmetic
    // and an array index splice the text in as it stands, so it has to be the
    // ternary again rather than the boolean it reads as elsewhere.
    source:
      "class AsNumber { static int f(boolean q, int[] xs) { return xs[q ? 2 : 0] + (q ? 1 : 0); }" +
      " static int g(int a) { return -(a > 0 ? 1 : 0); }" +
      " static long h(int a) { return (long) (a > 0 ? 1 : 0); } }",
    expect: [
      "arg0 ? 2 : 0",
      "+ (arg0 ? 1 : 0)",
      "-(arg0 > 0 ? 1 : 0)",
      "(long) (arg0 > 0 ? 1 : 0)",
    ],
    selfContained: true,
  },
  {
    name: "Cmp",
    // lcmp/dcmpg have no source form: the comparison they feed is what was written.
    source:
      "class Cmp { static boolean f(long a, long b) { return a < b; }" +
      " static boolean g(double a, double b) { return a >= b; } }",
    expect: ["return arg0 < arg1;", "return arg0 >= arg1;"],
    selfContained: true,
  },
  {
    name: "Throwing",
    source:
      "class Throwing { static int f(int a, java.lang.RuntimeException e) {" +
      " if (a < 0) throw e; return a; } }",
    expect: ["if (arg0 < 0) {", "throw arg1;"],
    selfContained: true,
  },
  {
    name: "Retype",
    // Only the *use* says the slot is a boolean, and the use comes after the
    // branch the assignments sit in - so the rewrite has to reach into it.
    source:
      "class Retype { static boolean f(int a) { boolean b; if (a > 0) { b = true; }" +
      " else { b = false; } return b; } }",
    expect: ["boolean var1;", "var1 = true;", "var1 = false;"],
    reject: ["var1 = 1;", "var1 = 0;"],
    selfContained: true,
  },
  {
    name: "SwitchLabeled",
    // A `switch` catches an unlabeled `break`, so one that leaves the loop
    // around it needs a label - which this phase does not write.
    source:
      "class SwitchLabeled { static int f(int n, int x) { int r = 0;" +
      " outer: while (r < n) { switch (x) { case 1: r += 1; break; case 2: break outer;" +
      " default: r += 3; } r += 1; } return r; } }",
    expect: ["cappu: a labeled break or continue"],
    selfContained: true,
  },
  {
    name: "SwitchDefaultPad",
    // The gaps a tableswitch pads with its default target say nothing a
    // `default:` does not, so they are not written back as cases.
    source:
      "class SwitchDefaultPad { static int f(int x) { int r = 0; switch (x) { case 1: r = 1; break;" +
      " case 4: r = 4; break; default: r = 9; } return r; } }",
    expect: ["case 1:", "case 4:", "default:"],
    reject: ["case 2:", "case 3:", "not decompiled"],
    selfContained: true,
  },
  {
    name: "Loop",
    // A `for` is a `while` whose update sits at the bottom of the body - the same
    // bytecode, so that is what it comes back as.
    source:
      "class Loop { static int f(int n) { int s = 0; for (int i = 0; i < n; i++) s += i; return s; } }",
    expect: ["while (var2 < arg0) {", "var1 = var1 + var2;", "var2++;"],
    reject: ["not decompiled"],
    selfContained: true,
  },
  {
    name: "WhileLoop",
    source:
      "class WhileLoop { static int f(int n) { int c = 0; while (n > 0) { c++; n--; } return c; } }",
    expect: ["while (arg0 > 0) {"],
    reject: ["while (true)", "not decompiled"],
    selfContained: true,
  },
  {
    name: "DoLoop",
    // The test is at the foot, so the body runs before it is asked.
    source:
      "class DoLoop { static int f(int n) { int i = 0; do { i += 3; } while (i < n); return i; } }",
    expect: ["do {", "var1 = var1 + 3;", "} while (var1 < arg0);"],
    reject: ["not decompiled"],
    selfContained: true,
  },
  {
    name: "Forever",
    source:
      "class Forever { static int f(int n) { int i = 0; while (true) { i += 2; if (i > n) { break; } i += n; } return i; } }",
    expect: ["while (true) {", "break;"],
    reject: ["not decompiled"],
    selfContained: true,
  },
  {
    name: "BreakOut",
    source:
      "class BreakOut { static int f(int[] xs, int stop) { int t = 0;" +
      " for (int i = 0; i < xs.length; i++) { if (xs[i] == stop) { break; } t += xs[i]; } return t; } }",
    expect: ["while (var3 < arg0.length) {", "break;"],
    reject: ["not decompiled"],
    selfContained: true,
  },
  {
    name: "Nested",
    source:
      "class Nested { static int f(int n, int m) { int t = 0;" +
      " for (int i = 0; i < n; i++) { for (int j = 0; j < m; j++) { t += i * j; } } return t; } }",
    expect: ["while (var3 < arg0) {", "while (var4 < arg1) {"],
    reject: ["not decompiled"],
    selfContained: true,
  },
  {
    name: "LoopAnd",
    // Both tests belong to the loop's own condition, not to an `if` inside it.
    source:
      "class LoopAnd { static int f(int a, int b) { int t = 0; while (a > 0 && b > 0) { t++; a--; b--; } return t; } }",
    expect: ["while (arg0 > 0 && arg1 > 0) {"],
    reject: ["not decompiled"],
    selfContained: true,
  },
  {
    name: "LoopContinue",
    source:
      "class LoopContinue { static int f(int[] xs) { int t = 0; int i = 0;" +
      " while (i < xs.length) { int v = xs[i]; i++; if (v < 0) { continue; } t += v; } return t; } }",
    expect: ["while (var2 < arg0.length) {"],
    reject: ["not decompiled"],
    selfContained: true,
  },
  {
    name: "Catching",
    source:
      "class Catching { static int f(int[] xs, int i) { try { return xs[i]; }" +
      " catch (java.lang.RuntimeException e) { return -1; } } }",
    expect: [
      "try {",
      "return arg0[arg1];",
      "} catch (java.lang.RuntimeException e) {",
      "return -1;",
    ],
    reject: ["not decompiled"],
    selfContained: true,
  },
  {
    name: "MultiCatch",
    // One clause per handler, one handler per `catch`: the two types of a
    // multi-catch share theirs, the two clauses of a chain do not.
    source:
      "class MultiCatch { static int f(int[] xs, int i) { try { return xs[i] / i; }" +
      " catch (java.lang.ArithmeticException | java.lang.NullPointerException e) { return 0; } }" +
      " static int g(int[] xs) { int r = 0; try { r = xs[0]; }" +
      " catch (java.lang.RuntimeException e) { r = 1; } catch (java.lang.Error e2) { r = 2; } return r; } }",
    expect: [
      "} catch (java.lang.ArithmeticException | java.lang.NullPointerException e) {",
      "} catch (java.lang.RuntimeException e) {",
      "} catch (java.lang.Error e_2) {",
    ],
    reject: ["not decompiled"],
    selfContained: true,
  },
  {
    name: "CatchBreak",
    // The `try` sits inside the loop, and what leaves the loop from a handler is
    // a `break` - not the statement's own end.
    source:
      "class CatchBreak { static int f(int[] xs) { int s = 0; int i = 0;" +
      " while (i < xs.length) { try { s += xs[i]; } catch (java.lang.RuntimeException e) { break; } i++; }" +
      " return s; } }",
    expect: ["while (var2 < arg0.length) {", "try {", "break;", "var2++;"],
    reject: ["not decompiled"],
    selfContained: true,
  },
  {
    name: "CatchEmpty",
    source:
      "class CatchEmpty { static void f(int a) { try { java.lang.System.out.println(a); }" +
      " catch (java.lang.RuntimeException e) { } } }",
    expect: ["} catch (java.lang.RuntimeException e) {"],
    reject: ["not decompiled"],
    // Not checked: an empty `catch` is what the source had, and our own checker
    // flags it as a swallowed exception.
  },
  {
    // A loop inside a handler: a handler is only reachable by throwing, so the
    // loop analysis has to see the throwing edges or it never finds this one.
    name: "CatchLoop",
    source:
      "class CatchLoop { static int f(int[] xs) { int s = 0; try { s = xs[0]; }" +
      " catch (java.lang.RuntimeException e) { for (int k = 0; k < 3; k++) { s += k; } } return s; } }",
    expect: ["} catch (java.lang.RuntimeException e) {", "while (var3 < 3) {"],
    reject: ["not decompiled"],
    selfContained: true,
  },
  {
    // The catch parameter's scope is its clause: javac hands the slot to the
    // next variable, and with no debug table only the scope says so.
    name: "CatchSlot",
    source:
      "class CatchSlot { static int f(int a) { int r = 0; try { r = 100 / a; }" +
      " catch (java.lang.ArithmeticException e) { r = -1; } int q = r * 2; return q; } }",
    expect: ["} catch (java.lang.ArithmeticException e) {", "int var2 = var1 * 2;"],
    reject: ["not decompiled", "e = "],
    selfContained: true,
  },
  {
    name: "Finally",
    // javac copies the `finally` into every exit path and guards the rest with a
    // catch-all that rethrows. One way out is one copy, which comes back; a
    // `return` inside the body is a second, and which copy source wrote is not
    // in the class file.
    source:
      "class Finally { static int f(int a) { try { return a; } finally { java.lang.System.out.println(a); } } }",
    expect: ["cappu: a finally with more than one way out"],
  },
  {
    name: "Blank",
    source: "class Blank { static int v() { return 1; } static final int N; static { N = v(); } }",
    // A blank `static final` is only assignable in the initializer, so the
    // initializer has to come back for the `final` to stand.
    expect: ["static final int N;", "N = v();"],
    reject: ["UnsupportedOperationException"],
    selfContained: true,
  },
];

for (const { name, source, expect: wanted, reject, selfContained } of RECONSTRUCTIONS) {
  test(`reconstructs ${name}`, () => {
    const decompiled = decompileToSource(emitClass(name, source));
    for (const text of wanted) expect(decompiled).toContain(text);
    for (const text of reject ?? []) expect(decompiled).not.toContain(text);
    if (selfContained) {
      expect({ [name]: diagnosticsOf(name, decompiled) }).toEqual({ [name]: [] });
    }
  });
}

// A static nested class is `new Outer.Inner(...)`; a true inner one is only
// writable as `outer.new Inner(...)`, which needs the enclosing file. The
// InnerClasses attribute of *this* file is what tells them apart - the first
// constructor parameter cannot, since a static one may take the outer type too.
test("tells a static nested class from an inner one", () => {
  const emitted = emitClasses(
    "Nested",
    "public class Nested { static class St { int v; St(Nested n, int v) { this.v = v; } }" +
      " class In { int v; In(int v) { this.v = v; } }" +
      " static int useStatic(Nested n) { return new St(n, 3).v; }" +
      " int useInner() { return new In(4).v; } }",
  );
  const outer = emitted.find(c => c.name === "Nested");
  expect(outer).toBeDefined();
  const source = decompileToSource(outer!.bytes);
  expect(source).toContain("new Nested.St(arg0, 3)");
  expect(source).toContain("cappu: an inner class constructor");
});

test("writes a nested type reference with a dot, not the binary $", () => {
  const source = decompileToSource(
    emitClass("Outer", "class Outer { static class Inner {} static Inner get() { return null; } }"),
  );
  expect(source).toContain("Outer.Inner get()");
});

// `i++` in a concatenation reads the variable before the increment and again
// after it. The increment is a statement here, so writing it back in front of
// the parts would change what they read - the method has to bail instead.
test("says so when an increment happens while the variable is on the stack", () => {
  const source = decompileToSource(
    emitClass("Inc", 'public class Inc { static String f(int i) { return "x" + i++ + i; } }'),
  );
  expect(source).toContain("cappu: an increment of a variable that is already on the stack");
});

test("reconstructs the control flow of the ControlFlow fixture", () => {
  const source = decompileToSource(classBytes("ControlFlow"));
  // Every shape comes back as the statement it was written as.
  expect(source).toContain("if (arg0 < 0) {");
  expect(source).toContain("return arg0 >= arg1 && arg0 <= arg2;");
  expect(source).toContain("while (var2 < arg0) {"); // the `for`, whose update is at the bottom
  expect(source).toContain("while (arg0 > 0) {");
  expect(source).toContain("} while (var1 < arg0);");
  expect(source).toContain("java.lang.System.out.println(sum(5));");
  expect(source).not.toContain("/* cappu:");
});

test("chains to the superclass constructor", () => {
  const emitted = emitClasses(
    "Base",
    "class Base { int v; Base(int v) { this.v = v; } }" +
      " class Sub extends Base { Sub(int x) { super(x); } }",
  );
  const sub = emitted.find(c => c.name === "Sub");
  expect(sub).toBeDefined();
  expect(decompileToSource(sub!.bytes)).toContain("super(arg0);");
});

// --- the JDK as a corpus -------------------------------------------------------------

/** A module of the JDK on PATH (or JAVA_HOME), when it ships the jmods/ a corpus needs. */
function jmodOf(module: string): string | undefined {
  const home =
    process.env.JAVA_HOME ??
    (process.env.PATH ?? "")
      .split(":")
      .map(dir => join(dir, "javac"))
      .filter(existsSync)
      .map(javac => dirname(dirname(realpathSync(javac))))[0];
  if (home === undefined) return undefined;
  const jmod = join(home, "jmods", `${module}.jmod`);
  return existsSync(jmod) ? jmod : undefined;
}

// java.base is built with `-XDstringConcat=inline` - it holds StringConcatFactory
// itself - so it contains almost no concatenation invokedynamic. java.desktop is
// the module that covers that phase.
const CORPUS_MODULES = ["java.base", "java.desktop"];

// Real classes from a real compiler: every shape javac emits, including the ones
// no fixture here covers. The bar is not a full reconstruction - most of these
// bail - but that what comes out is always Java the parser accepts.
for (const module of CORPUS_MODULES) {
  test(`decompiles every class in ${module} to parseable source`, () => {
    const jmod = jmodOf(module);
    if (jmod === undefined) return; // a JRE or a stripped image: nothing to read
    // .jmod is a zip behind a 4-byte magic.
    const entries = readZipEntries(readFileSync(jmod).subarray(4)) ?? [];
    const classes = entries.filter(e => e.name.startsWith("classes/") && e.name.endsWith(".class"));
    expect(classes.length).toBeGreaterThan(1000);
    const failures: string[] = [];
    for (const entry of classes) {
      const name = entry.name.slice("classes/".length, -".class".length);
      if (name === "module-info") continue;
      let source: string;
      try {
        source = decompile(entry.read());
      } catch (e) {
        failures.push(`${name}: threw ${(e as Error).message}`);
        continue;
      }
      const diagnostics = parseSourceFile(`${name}.java`, source).parseDiagnostics;
      if (diagnostics.length > 0) failures.push(`${name}: ${diagnostics[0]!.messageText}`);
    }
    expect(failures.slice(0, 10)).toEqual([]);
  });
}

// --- names ---------------------------------------------------------------------------

// javac (and our emitter) reuse a slot once a variable goes out of scope, so a
// slot is not a variable: the second one needs its own name and type, or the
// output does not compile.
const REUSED_SLOT =
  "public class Reuse { static int f(int n) {" +
  " { int a = n + 1; n = a; } { long b = n * 2L; n = (int) b; } return n; } }";

test("declares a second variable when a slot is reused", () => {
  const source = decompileToSource(emitClass("Reuse", REUSED_SLOT));
  expect(source).toContain("int var1 = arg0 + 1;");
  expect(source).toContain("long var1_2 = (long) arg0 * 2L;");
  // Reusing the name would assign a long to an int; the checker says so.
  expect({ Reuse: diagnosticsOf("Reuse", source) }).toEqual({ Reuse: [] });
});

test("keeps both debug-table names when a slot is reused", () => {
  const source = decompileToSource(emitClass("Reuse", REUSED_SLOT, true));
  expect(source).toContain("int a = n + 1;");
  expect(source).toContain("long b = (long) n * 2L;");
});

// The two arms store to the same slot with the same opcode but differently
// typed values, so without a debug table there is nothing left to say whether
// that is one variable or two - and guessing would produce code that lies.
const AMBIGUOUS =
  "class Amb { static java.lang.Object f(boolean c, java.lang.String s, int[] a) {" +
  " java.lang.Object o; if (c) { o = s; } else { o = a; } return o; } }";

test("says so when a slot's value could come from either branch", () => {
  const source = decompileToSource(emitClass("Amb", AMBIGUOUS));
  expect(source).toContain("cappu: local 3 is written in more than one branch");
});

test("reads one variable when the debug table scopes it per branch", () => {
  // javac (and our emitter with -g) writes a LocalVariableTable row per scope
  // range, so one variable can appear once per arm; name and type say it is one.
  const source = decompileToSource(emitClass("Amb", AMBIGUOUS, true));
  expect(source).toContain("java.lang.Object o;");
  expect(source).toContain("o = s;");
  expect(source).toContain("o = a;");
  expect(source).toContain("return o;");
  expect(source).not.toContain("o_2");
  expect({ Amb: diagnosticsOf("Amb", source) }).toEqual({ Amb: [] });
});

const DEBUGGY = "class Debuggy { int f(int seed) { int doubled = seed * 2; return doubled; } }";

test("uses the LocalVariableTable names when the class carries one", () => {
  const source = decompileToSource(emitClass("Debuggy", DEBUGGY, true));
  expect(source).toContain("int f(int seed)");
  expect(source).toContain("int doubled = seed * 2;");
});

test("falls back to slot names when the class has no debug info", () => {
  const source = decompileToSource(emitClass("Debuggy", DEBUGGY));
  expect(source).toContain("int f(int arg0)");
  expect(source).toContain("int var2 = arg0 * 2;");
});
