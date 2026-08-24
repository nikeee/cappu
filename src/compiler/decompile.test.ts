// Phases 1.3 and 1.4 of `cappu decompile` (nikeee/cappu#43): straight-line
// bytecode, and branches without a loop, back to Java source.
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
// full - straight-line arithmetic, conversions, fields, arrays and casts.
const FULLY_DECOMPILED = [
  "AnnAll",
  "Arithmetic",
  "ArrayLoad",
  "ArrayStore",
  "CastInstance",
  "Cl",
  "ClassLit",
  "Constants",
  "Empty",
  "EnumAbstract$1",
  "EnumAbstract$2",
  "EnumMixed$1",
  "EnumMixed$2",
  "Fields",
  "FloatArith",
  "FloatConst",
  "FloatConv",
  "Fold",
  "ICast$A",
  "ICast$B",
  "ISA",
  "ISB",
  "ImplicitSealed",
  "IntConv",
  "IntLiterals",
  "Locals",
  "LongArith",
  "Methods",
  "ModifiedFields",
  "Nest$Counter",
  "Nest$Point",
  "Nest",
  "NewArray",
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
];

// Classes kept for the bail-out rendering: control flow (phase 1.4+) and method
// calls (phase 1.6) are not this phase's job, and must say so.
const NOT_DECOMPILED = [
  "BoundErasure",
  "Boxing",
  "Compute",
  "Concat",
  "ControlFlow",
  "EnumAbstract",
  "EnumMixed",
  "EnumUnqualified",
  "Hello",
  "ICast",
  "Invoke",
  "PrivateCall",
  "QualifiedAnon$1",
  "QualifiedAnon$Inner",
  "QualifiedAnon",
  "QualifiedNew$Inner",
  "QualifiedNew",
  "VarargsPack",
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

const NO_ROUNDTRIP = [...STUB_GAP, "Nest$Counter", ...ENUM_CONSTANT_BODIES];

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

/** Compile one source file with javac and return the .class path. */
function compileWithJavac(source: string, name: string, outDir: string): string {
  mkdirSync(outDir, { recursive: true });
  const javaFile = join(outDir, `${name}.java`);
  writeFileSync(javaFile, source);
  execFileSync("javac", ["--release", "21", "-d", outDir, javaFile], { stdio: "pipe" });
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
    // The condition is a boolean, so an index has to ask for the int back.
    expect: ["return (arg0 > arg1 ? arg0 : arg1) + 1;", "var1[!arg0 ? 0 : 1] = 7;"],
    selfContained: true,
  },
  {
    name: "BoolVar",
    // Only the branch says the local is a boolean: `istore` is what an int uses.
    source:
      "class BoolVar { static int f(int a) { boolean big = a > 10; if (big) { return 1; } return 0; } }",
    expect: ["boolean var1 = arg0 > 10;", "if (var1) {"],
    reject: ["var1 != 0"],
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
    name: "Loop",
    // Phase 1.5; until then it has to say so rather than produce something wrong.
    source:
      "class Loop { static int f(int n) { int s = 0; for (int i = 0; i < n; i++) s += i; return s; } }",
    expect: ["loops are not decompiled yet", "not decompiled"],
    selfContained: true,
  },
  {
    name: "Blank",
    source: "class Blank { static int v() { return 1; } static final int N; static { N = v(); } }",
    // The initializer could not be reconstructed, so `final` cannot stand, and
    // a static initializer may not throw.
    expect: ["static int N;"],
    reject: ["static final int N", "UnsupportedOperationException"],
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

test("writes a nested type reference with a dot, not the binary $", () => {
  const source = decompileToSource(
    emitClass("Outer", "class Outer { static class Inner {} static Inner get() { return null; } }"),
  );
  expect(source).toContain("Outer.Inner get()");
});

test("reconstructs the control flow of the ControlFlow fixture", () => {
  const source = decompileToSource(classBytes("ControlFlow"));
  // Two branches, no loop: both come back as the expression they were written as.
  expect(source).toContain("if (arg0 < 0) {");
  expect(source).toContain("return arg0 >= arg1 && arg0 <= arg2;");
  // The loops in the same class still say they are a later phase.
  expect(source).toContain("cappu: loops are not decompiled yet");
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
