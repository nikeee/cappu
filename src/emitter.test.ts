import { test } from "node:test";
import { expect } from "expect";
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { createChecker } from "./checker.ts";
import { emitSourceFile } from "./emitter.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { createProgram } from "./program.ts";

const here = dirname(fileURLToPath(import.meta.url));
const baselinesDir = join(here, "__fixtures__", "emit-baselines");
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
};

// Fixtures with a runnable main and the output they must print.
const RUNS: Record<string, string> = {
  Hello: "Hello, world\n",
};

// javap -c per-method instruction lines, with constant-pool indices stripped so
// only mnemonics + symbolic operands remain (comparable across compilers).
function codeByMethod(classFile: string): Map<string, string[]> {
  const lines = execFileSync("javap", ["-c", "-p", classFile], { encoding: "utf8" }).split("\n");
  const map = new Map<string, string[]>();
  let current: string | undefined;
  for (const raw of lines) {
    const t = raw.trim();
    if (/^\d+:/.test(t)) {
      if (current) {
        map.get(current)!.push(
          t
            .replace(/^\d+:\s*/, "")
            .replace(/#\d+/g, "#")
            .replace(/\s+/g, " ")
            .trim(),
        );
      }
    } else if (t.endsWith(";") && t.includes("(") && !t.startsWith("//")) {
      current = t; // a method/constructor declaration line
      map.set(current, []);
    }
  }
  return map;
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

// javap -p member signature lines (fields, constructors, methods), normalized.
function members(classFile: string): string[] {
  const out = execFileSync("javap", ["-p", classFile], { encoding: "utf8" });
  return out
    .split("\n")
    .map(l => l.trim())
    .filter(l => l.endsWith(";") && !l.startsWith("Compiled"))
    .sort();
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

  test(`emit is JVM-valid: ${name}`, { skip: HAS_JAVA ? false : "no JDK" }, () => {
    const dir = mkdtempSync(join(tmpdir(), "emit-"));
    writeFileSync(join(dir, `${name}.class`), emit(name, source));
    const out = execFileSync("javap", ["-p", join(dir, `${name}.class`)], { encoding: "utf8" });
    expect(out).toContain(`class ${name}`);
    // Loading the class runs the bytecode verifier over every method. A bad body
    // would raise VerifyError/ClassFormatError; "Main method not found" (or an
    // actual run) means it verified cleanly.
    let stderr = "";
    try {
      execFileSync("java", ["-cp", dir, name], { encoding: "utf8", stdio: "pipe" });
    } catch (e) {
      stderr = String((e as { stderr?: string }).stderr ?? "");
    }
    expect(stderr).not.toMatch(/VerifyError|ClassFormatError|Incompatible/);
  });

  test(`members match javac: ${name}`, { skip: HAS_JAVAC && HAS_JAVA ? false : "no JDK" }, () => {
    const dir = mkdtempSync(join(tmpdir(), "emit-"));
    // Reference: compile the same source with javac (Java 21 target).
    writeFileSync(join(dir, `${name}.java`), source);
    execFileSync("javac", ["--release", "21", "-d", dir, join(dir, `${name}.java`)]);
    const reference = members(join(dir, `${name}.class`));
    // Ours, written to a separate dir to avoid overwriting javac's output.
    const oursDir = mkdtempSync(join(tmpdir(), "emit-ours-"));
    writeFileSync(join(oursDir, `${name}.class`), emit(name, source));
    expect(members(join(oursDir, `${name}.class`))).toEqual(reference);
  });

  test(
    `bytecode matches javac: ${name}`,
    { skip: HAS_JAVAC && HAS_JAVA ? false : "no JDK" },
    () => {
      const ref = mkdtempSync(join(tmpdir(), "emit-ref-"));
      writeFileSync(join(ref, `${name}.java`), source);
      execFileSync("javac", ["--release", "21", "-d", ref, join(ref, `${name}.java`)]);
      const ours = mkdtempSync(join(tmpdir(), "emit-ours-"));
      writeFileSync(join(ours, `${name}.class`), emit(name, source));

      const reference = codeByMethod(join(ref, `${name}.class`));
      const generated = codeByMethod(join(ours, `${name}.class`));
      expect([...generated.keys()].sort()).toEqual([...reference.keys()].sort());
      for (const [sig, instrs] of reference) {
        expect(generated.get(sig)).toEqual(instrs);
      }
    },
  );
}

for (const [name, expected] of Object.entries(RUNS)) {
  test(`runs and prints: ${name}`, { skip: HAS_JAVA ? false : "no JDK" }, () => {
    const dir = mkdtempSync(join(tmpdir(), "emit-run-"));
    writeFileSync(join(dir, `${name}.class`), emit(name, source(name)));
    const out = execFileSync("java", ["-cp", dir, name], { encoding: "utf8" });
    expect(out).toBe(expected);
  });
}

function source(name: string): string {
  return FIXTURES[name]!;
}
