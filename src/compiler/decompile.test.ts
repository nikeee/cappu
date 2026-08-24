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

import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

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

// `ClassLit.prim()` reads `java.lang.Integer.TYPE`, which the decompiler gets
// right but our own emitter degrades to aconst_null - the oracle is only as good
// as the emitter, so that one class is checked by its text baseline alone.
const NO_ROUNDTRIP = ["ClassLit"];

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

// --- names ---------------------------------------------------------------------------

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
