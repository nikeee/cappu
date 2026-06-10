// Minimal compiler core: javac-lite. Reads .java files, parses them and
// writes one .class file per top-level class under the output root, mirroring
// each class's package as a directory path (com.app.Foo -> com/app/Foo.class) so
// the tree can be packed straight into a jar. Output root defaults to the cwd.
// Code generation is at an early stage - see emitter.ts. Invoked via cli.ts.

import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";

import { setDegradeListener } from "./bytecode.ts";
import { createChecker } from "./checker.ts";
import { emitSourceFile } from "./emitter.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { createProgram } from "./program.ts";
import { pathToUri } from "./workspace.ts";

export interface CompileOptions {
  outDir?: string;
  quiet?: boolean;
  /** Treat degraded (placeholder) method bodies as a build failure. */
  failOnDegrade?: boolean;
}

export function runCompile(files: string[], options: CompileOptions = {}): number {
  if (files.length === 0) {
    process.stderr.write("usage: compile [-d <outdir>] <file.java> ...\n");
    return 2;
  }

  // One program over all inputs (+ the JDK stub) so type descriptors resolve.
  const program = createProgram();
  loadJdkStub(program);
  for (const file of files) program.addProjectFile(pathToUri(file), readFileSync(file, "utf8"));
  const checker = createChecker(program);

  // A degraded body still produces a verifiable class, but silently behaves as
  // a stub; surface every one so the build is honest about what it emitted.
  const degraded: string[] = [];
  setDegradeListener((className, member) => {
    degraded.push(`${className.replace(/\//g, ".")}.${member}`);
  });

  try {
    // Single output root so every class lands in one coherent package tree.
    const target = options.outDir ?? ".";
    for (const file of files) {
      const sourceFile = program.getSourceFile(pathToUri(file))!;
      if (sourceFile.parseDiagnostics.length > 0) {
        for (const d of sourceFile.parseDiagnostics) {
          process.stderr.write(`${file}: error ${d.code}: ${d.messageText}\n`);
        }
        return 1;
      }
      for (const cls of emitSourceFile(sourceFile, program, checker)) {
        // cls.name is the internal name (com/app/Foo); mirror it as a directory path.
        const out = join(target, `${cls.name}.class`);
        mkdirSync(dirname(out), { recursive: true });
        writeFileSync(out, cls.bytes);
        if (!options.quiet) process.stdout.write(`${out}\n`);
      }
    }
  } finally {
    setDegradeListener(undefined);
  }

  for (const entry of degraded) {
    process.stderr.write(`warning: ${entry}: unsupported construct, emitted a placeholder body\n`);
  }
  if (degraded.length > 0 && options.failOnDegrade) {
    process.stderr.write(`error: ${degraded.length} method(s) degraded (--fail-on-degrade)\n`);
    return 1;
  }
  return 0;
}
