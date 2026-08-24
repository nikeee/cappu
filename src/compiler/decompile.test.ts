// Phase 1.3 of `cappu decompile` (nikeee/cappu#43): straight-line bytecode back
// to Java source.
//
// The class bytes come from test-fixtures/emitter/emit-baselines, which our own
// emitter produced - so no JDK is needed here. Two tiers:
//   1. text baselines, also read by the Go port (togo/internal/compiler/decompile_test.go),
//      which makes them the TS/Go parity check;
//   2. a roundtrip: re-emit the decompiled source and require the same
//      normalized instruction stream, which proves the output is valid Java
//      that means what the input meant.

import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
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
  "Arithmetic",
  "ArrayLoad",
  "ArrayStore",
  "CastInstance",
  "ClassLit",
  "Constants",
  "Empty",
  "Fields",
  "FloatArith",
  "FloatConst",
  "FloatConv",
  "Fold",
  "IntConv",
  "IntLiterals",
  "Locals",
  "LongArith",
  "Methods",
  "ModifiedFields",
  "NewArray",
  "ReturnLiterals",
  "Returns",
  "StaticFields",
  "VarargsAndAbstract",
];

// Classes kept for the bail-out rendering: control flow (phase 1.4+) and method
// calls (phase 1.6) are not this phase's job, and must say so.
const NOT_DECOMPILED = ["ControlFlow", "Invoke", "Pt"];

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

for (const name of FULLY_DECOMPILED.filter(n => !STUB_GAP.includes(n))) {
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
