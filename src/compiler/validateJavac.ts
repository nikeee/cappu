// `cappu compile --validate`: compile the same sources with javac and compare
// the bytecode. Equality is the project's established normalized-disassembly
// form (javap -c -p with constant-pool indices stripped, see javapNormalize):
// raw bytes differ across compilers for irrelevant reasons (constant pool
// order), the mnemonic stream does not. Print-free; the cli renders the result.

import { execFileSync } from "node:child_process";
import { globSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";

import { type Disasm, disasmFiles, isPlaceholderBody } from "./javapNormalize.ts";

export interface ValidationMismatch {
  className: string;
  detail: string;
}

export type ValidationResult =
  /** Every class compared equal (degraded placeholder bodies are skipped). */
  | { ok: true; compared: number }
  | { ok: false; mismatches: ValidationMismatch[]; compared: number }
  /** javac (or javap) could not be run at all. */
  | { ok: false; error: string };

/** javap next to a configured javac; plain "javap" for a bare $PATH "javac". */
function javapFor(javacBin: string): string {
  return javacBin.includes("/") ? join(dirname(javacBin), "javap") : "javap";
}

function compareClass(name: string, ours: Disasm, theirs: Disasm): string | undefined {
  const ourMembers = ours.members.join("\n");
  const theirMembers = theirs.members.join("\n");
  if (ourMembers !== theirMembers) return "declared members differ";
  const theirCode = new Map(theirs.code);
  for (const [signature, instructions] of ours.code) {
    if (isPlaceholderBody(instructions)) continue; // degraded body, warned elsewhere
    const reference = theirCode.get(signature);
    if (!reference) return `no javac counterpart for ${signature}`;
    if (instructions.length !== reference.length) {
      return `${signature}: ${instructions.length} vs ${reference.length} instructions`;
    }
    for (let i = 0; i < instructions.length; i++) {
      if (instructions[i] !== reference[i]) {
        return `${signature}: instruction ${i}: '${instructions[i]}' vs '${reference[i]}'`;
      }
    }
  }
  return undefined;
}

/**
 * Compile `sourceFiles` with javac into a temp dir and compare every class we
 * wrote (`written`) against javac's output for the same binary name.
 */
export function validateAgainstJavac(
  sourceFiles: string[],
  written: string[],
  javacBin = "javac",
): ValidationResult {
  const tmp = mkdtempSync(join(tmpdir(), "cappu-validate-"));
  try {
    try {
      execFileSync(javacBin, ["-d", tmp, "--release", "21", ...sourceFiles], {
        stdio: ["ignore", "pipe", "pipe"],
      });
    } catch (e) {
      const detail = (e as { stderr?: Buffer }).stderr?.toString().trim();
      return { ok: false, error: `${javacBin} failed: ${detail || (e as Error).message}` };
    }

    const javacClasses = globSync("**/*.class", { cwd: tmp }).map(f => join(tmp, f));
    let ours: Map<string, Disasm>;
    let theirs: Map<string, Disasm>;
    try {
      const javap = javapFor(javacBin);
      ours = disasmFiles(written, javap);
      theirs = disasmFiles(javacClasses, javap);
    } catch (e) {
      return { ok: false, error: `javap failed: ${(e as Error).message}` };
    }

    const mismatches: ValidationMismatch[] = [];
    let compared = 0;
    for (const [name, disasm] of ours) {
      const reference = theirs.get(name);
      if (!reference) {
        mismatches.push({ className: name, detail: "javac produced no such class" });
        continue;
      }
      compared++;
      const detail = compareClass(name, disasm, reference);
      if (detail) mismatches.push({ className: name, detail });
    }
    return mismatches.length > 0 ? { ok: false, mismatches, compared } : { ok: true, compared };
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
}
