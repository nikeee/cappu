// `cappu decompile`: print the bytecode of .class files, in `javap -c -p`
// layout (nikeee/cappu#43). Reconstructing Java source is a later phase, which
// is why there is no output-format flag yet.

import { readFileSync } from "node:fs";

import { disassemble } from "../compiler/disasm.ts";

// Node and Go word their I/O errors differently, so both builds map the cases
// that matter to the same text (togo/internal/cli/decompile.go does the same).
function readErrorText(e: unknown): string {
  switch ((e as NodeJS.ErrnoException).code) {
    case "ENOENT":
      return "no such file or directory";
    case "EISDIR":
      return "is a directory";
    case "EACCES":
    case "EPERM":
      return "permission denied";
    case undefined:
      return (e as Error).message; // a ClassFileError, not an I/O failure
    default:
      return "cannot read file";
  }
}

export function runDecompile(files: string[]): never {
  if (files.length === 0) {
    process.stderr.write("usage: cappu decompile <file.class> ...\n");
    process.exit(2);
  }
  let failed = false;
  for (const file of files) {
    try {
      process.stdout.write(disassemble(readFileSync(file)));
    } catch (e) {
      process.stderr.write(`cappu: ${file}: ${readErrorText(e)}\n`);
      failed = true;
    }
  }
  process.exit(failed ? 1 : 0);
}
