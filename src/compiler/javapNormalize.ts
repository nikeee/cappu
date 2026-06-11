// Normalized disassembly via `javap -c -p`, used to compare our emitted
// bytecode against javac's. Constant-pool indices are stripped so only
// mnemonics + symbolic operands remain (stable across compilers); this is the
// form checked into the *-baselines fixtures and what `cappu compile
// --validate` compares (validateJavac.ts). Imported by the *.test.ts files
// and the validator.

import { execFileSync } from "node:child_process";

export interface Disasm {
  members: string[];
  code: [string, string[]][]; // [methodSignature, instructionLines]
}

// Disassemble one or more class files in a SINGLE javap invocation, keyed by the
// (binary) class name javap prints.
export function disasmFiles(classFiles: string[], javapBin = "javap"): Map<string, Disasm> {
  const out = execFileSync(javapBin, ["-c", "-p", ...classFiles], {
    encoding: "utf8",
    maxBuffer: 256 * 1024 * 1024, // large projects produce a lot of disassembly
  });
  const map = new Map<string, Disasm>();
  let cur: Disasm | undefined;
  let method: string[] | undefined;
  for (const raw of out.split("\n")) {
    const t = raw.trim();
    if (!t) continue;
    // A class header is unindented (raw === trimmed), names a class/interface/enum
    // and opens a brace; everything below it (indented) belongs to that class.
    const header = raw === t && t.endsWith("{") && /(?:class|interface|enum)\s+[\w$.]+/.test(t);
    if (header) {
      const name = t.match(/(?:class|interface|enum)\s+([\w$.]+)/)![1]!;
      cur = { members: [], code: [] };
      map.set(name, cur);
      method = undefined;
    } else if (!cur) {
      continue;
    } else if (/^\d+:/.test(t)) {
      method?.push(
        t
          .replace(/^\d+:\s*/, "")
          .replace(/#\d+/g, "#")
          .replace(/\s+/g, " ")
          .trim(),
      );
    } else if (t.endsWith(";") && !t.startsWith("//")) {
      cur.members.push(t);
      if (t.includes("(")) {
        method = [];
        cur.code.push([t, method]); // a method/constructor declaration line
      } else {
        method = undefined; // a field (or `static {};`): no comparable code
      }
    }
  }
  for (const d of map.values()) d.members.sort();
  return map;
}

// The trivial method bodies our emitter falls back to when a construct is not yet
// supported (see defaultReturnBody in bytecode.ts). A method whose disassembly is
// one of these is a degraded placeholder and is skipped when comparing to javac.
const PLACEHOLDERS: string[][] = [
  ["return"],
  ["iconst_0", "ireturn"],
  ["lconst_0", "lreturn"],
  ["fconst_0", "freturn"],
  ["dconst_0", "dreturn"],
  ["aconst_null", "areturn"],
];

export function isPlaceholderBody(instrs: string[]): boolean {
  return PLACEHOLDERS.some(p => p.length === instrs.length && p.every((x, i) => x === instrs[i]));
}
