import { test } from "node:test";
import { expect } from "expect";
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

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
};

function emit(name: string, source: string): Uint8Array {
  const program = createProgram();
  loadJdkStub(program);
  const uri = `file:///${name}.java`;
  program.setOpenDocument(uri, source, 1);
  const classes = emitSourceFile(program.getSourceFile(uri)!, program);
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
    // javap reads it without error -> structurally valid.
    const out = execFileSync("javap", ["-p", join(dir, `${name}.class`)], { encoding: "utf8" });
    expect(out).toContain(`class ${name}`);
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
}
