#!/usr/bin/env -S tsx

// Minimal command-line compiler: javac-lite. Reads .java files, parses them and
// writes one .class file per top-level class into the output directory (default:
// alongside each source). Code generation is at an early stage - see emitter.ts.
//
//   tsx src/compile.ts [-d <outdir>] <file.java> ...

import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";

import { createChecker } from "./checker.ts";
import { emitSourceFile } from "./emitter.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { createProgram } from "./program.ts";
import { pathToUri } from "./workspace.ts";

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

  // One program over all inputs (+ the JDK stub) so type descriptors resolve.
  const program = createProgram();
  loadJdkStub(program);
  for (const file of files) program.addProjectFile(pathToUri(file), readFileSync(file, "utf8"));
  const checker = createChecker(program);

  for (const file of files) {
    const sourceFile = program.getSourceFile(pathToUri(file))!;
    if (sourceFile.parseDiagnostics.length > 0) {
      for (const d of sourceFile.parseDiagnostics) {
        process.stderr.write(`${file}: error ${d.code}: ${d.messageText}\n`);
      }
      return 1;
    }
    const target = outDir ?? dirname(file);
    for (const cls of emitSourceFile(sourceFile, program, checker)) {
      // cls.name is the internal name (com/app/Foo); mirror it as a directory path.
      const out = join(target, `${cls.name}.class`);
      mkdirSync(dirname(out), { recursive: true });
      writeFileSync(out, cls.bytes);
      process.stdout.write(`${out}\n`);
    }
  }
  return 0;
}

process.exit(main(process.argv.slice(2)));
