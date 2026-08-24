// `cappu decompile`: reconstruct Java source from .class files, or print their
// bytecode in `javap -c -p` layout with --disasm (nikeee/cappu#43).

import { readFileSync, writeSync } from "node:fs";

import { decompile } from "../compiler/decompile.ts";
import { disassemble } from "../compiler/disasm.ts";
import { formatSource } from "../format/index.ts";

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

/**
 * The decompiler emits rough text; the formatter lays it out. A body this phase
 * cannot reconstruct carries its disassembly as a comment, which the formatter
 * may refuse - the unformatted source is still the right answer then.
 */
export function decompileToSource(bytes: Uint8Array): string {
  const source = decompile(bytes);
  try {
    return formatSource(source);
  } catch {
    return source;
  }
}

/**
 * Write to stdout synchronously. process.exit() drops whatever is still queued
 * on an async stream, and stdout is async when it is a pipe - so a listing sent
 * to `less` or `grep` could otherwise be cut short. (Go's fmt.Print is already
 * synchronous, which is the behaviour both builds have to share.)
 */
function writeStdout(text: string): void {
  const buffer = Buffer.from(text, "utf8");
  let at = 0;
  while (at < buffer.length) {
    try {
      at += writeSync(1, buffer, at);
    } catch (e) {
      // A non-blocking pipe with a full buffer: retry, there is nothing to wait
      // on synchronously.
      if ((e as NodeJS.ErrnoException).code !== "EAGAIN") throw e;
    }
  }
}

export function runDecompile(files: string[], disasm: boolean): never {
  if (files.length === 0) {
    process.stderr.write("usage: cappu decompile <file.class> ...\n");
    process.exit(2);
  }
  let failed = false;
  for (const file of files) {
    try {
      const bytes = readFileSync(file);
      writeStdout(disasm ? disassemble(bytes) : decompileToSource(bytes));
    } catch (e) {
      process.stderr.write(`cappu: ${file}: ${readErrorText(e)}\n`);
      failed = true;
    }
  }
  process.exit(failed ? 1 : 0);
}
