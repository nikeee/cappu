// `cappu decompile`: print the bytecode of .class files, in `javap -c -p`
// layout (nikeee/cappu#43). Reconstructing Java source is a later phase, which
// is why there is no output-format flag yet.

import { readFileSync } from "node:fs";

import { disassemble } from "../compiler/disasm.ts";

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
      // Kept identical to the Go build's wording (togo/internal/cli/decompile.go).
      const reason =
        (e as NodeJS.ErrnoException).code === "ENOENT"
          ? "no such file or directory"
          : (e as Error).message;
      process.stderr.write(`cappu: ${file}: ${reason}\n`);
      failed = true;
    }
  }
  process.exit(failed ? 1 : 0);
}
