#!/usr/bin/env -S tsx

// Minimal command-line compiler: javac-lite. Reads .java files, parses them and
// writes one .class file per top-level class into the output directory (default:
// alongside each source). Code generation is at an early stage - see emitter.ts.
//
//   tsx src/compile.ts [-d <outdir>] <file.java> ...

import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";

import { emitSourceFile } from "./emitter.ts";
import { parseSourceFile } from "./parser.ts";

function main(argv: string[]): number {
  let outDir: string | undefined;
  const files: string[] = [];
  for (let i = 0; i < argv.length; i++) {
    if (argv[i] === "-d") outDir = argv[++i];
    else files.push(argv[i]!);
  }
  if (files.length === 0) {
    process.stderr.write("usage: compile [-d <outdir>] <file.java> ...\n");
    return 2;
  }

  for (const file of files) {
    const source = readFileSync(file, "utf8");
    const sourceFile = parseSourceFile(file, source);
    if (sourceFile.parseDiagnostics.length > 0) {
      for (const d of sourceFile.parseDiagnostics) {
        process.stderr.write(`${file}: error ${d.code}: ${d.messageText}\n`);
      }
      return 1;
    }
    const target = outDir ?? dirname(file);
    for (const cls of emitSourceFile(sourceFile)) {
      const out = join(target, `${cls.name}.class`);
      writeFileSync(out, cls.bytes);
      process.stdout.write(`${out}\n`);
    }
  }
  return 0;
}

process.exit(main(process.argv.slice(2)));
