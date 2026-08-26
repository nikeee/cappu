// Phases 1.3 to 1.7 of `cappu decompile` (nikeee/cappu#43): straight-line
// bytecode, branches, loops, calls and `try`/`catch`, back to Java source.
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
  "VarargsAndAbstract",
  "VarargsPack",
];

// Classes kept for the bail-out rendering: string concatenation (an
// invokedynamic) and an inner class's constructor are not this phase's job, and
// must say so.
const NOT_DECOMPILED = [
  "Concat",
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
  for (const name of FULLY_DECOMPILED) {
    expect({ [name]: decompile(classBytes(name)).includes("not decompiled") }).toEqual({
      [name]: false,
    });
  }
  for (const name of NOT_DECOMPILED) {
    expect({ [name]: decompile(classBytes(name)).includes("not decompiled") }).toEqual({
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
    expect(source).not.toContain("not decompiled");
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
    expect(source).not.toContain("not decompiled");
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
    expect(source).not.toContain("not decompiled");
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
    expect(source).not.toContain("not decompiled");
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
    expect(source).not.toContain("not decompiled");
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
    expect(source).not.toContain("not decompiled");
    const roundTripped = compileWithJavac(source, "Arrayly", join(dir.path, "again"));
    expect(javap(roundTripped)).toEqual(javap(classFile));
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
    // catch-all that rethrows: which of those copies source wrote is not in the
    // class file, so this one says so.
    source:
      "class Finally { static int f(int a) { try { return a; } finally { java.lang.System.out.println(a); } } }",
    expect: ["cappu: a finally or synchronized block"],
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

test("reconstructs the control flow of the ControlFlow fixture", () => {
  const source = decompileToSource(classBytes("ControlFlow"));
  // Every shape comes back as the statement it was written as.
  expect(source).toContain("if (arg0 < 0) {");
  expect(source).toContain("return arg0 >= arg1 && arg0 <= arg2;");
  expect(source).toContain("while (var2 < arg0) {"); // the `for`, whose update is at the bottom
  expect(source).toContain("while (arg0 > 0) {");
  expect(source).toContain("} while (var1 < arg0);");
  expect(source).toContain("java.lang.System.out.println(sum(5));");
  expect(source).not.toContain("not decompiled");
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

/** The JDK on PATH (or JAVA_HOME), when it ships the jmods/ a class corpus needs. */
function javaBaseJmod(): string | undefined {
  const home =
    process.env.JAVA_HOME ??
    (process.env.PATH ?? "")
      .split(":")
      .map(dir => join(dir, "javac"))
      .filter(existsSync)
      .map(javac => dirname(dirname(realpathSync(javac))))[0];
  if (home === undefined) return undefined;
  const jmod = join(home, "jmods", "java.base.jmod");
  return existsSync(jmod) ? jmod : undefined;
}

// Real classes from a real compiler: every shape javac emits, including the ones
// no fixture here covers. The bar is not a full reconstruction - most of these
// bail - but that what comes out is always Java the parser accepts.
test("decompiles every class in java.base to parseable source", () => {
  const jmod = javaBaseJmod();
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
