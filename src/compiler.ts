// Minimal compiler core: javac-lite. Reads .java files, parses them and
// writes one .class file per top-level class under the output root, mirroring
// each class's package as a directory path (com.app.Foo -> com/app/Foo.class) so
// the tree can be packed straight into a jar. Output root defaults to the cwd.
// Code generation is at an early stage - see emitter.ts. Invoked via cli.ts.

import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";

import { setDegradeListener } from "./bytecode.ts";
import { createChecker } from "./checker.ts";
import { loadClassPath } from "./classfileReader.ts";
import { type CappuConfig, resolveConfigPath } from "./config.ts";
import { emitSourceFile } from "./emitter.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { createProgram, type Program } from "./program.ts";
import { loadJavaFiles, pathToUri } from "./workspace.ts";

export interface CompileOptions {
  outDir?: string;
  quiet?: boolean;
  /** Treat degraded (placeholder) method bodies as a build failure. */
  failOnDegrade?: boolean;
  /** Project configuration (cappu.json); CLI flags take precedence. */
  config?: CappuConfig;
}

/**
 * Register the config's classPath (.class stubs) and sourcePaths (.java
 * sources, for resolution only - they are not compiled) into a program.
 */
export function loadConfiguredPaths(program: Program, config: CappuConfig): void {
  loadClassPath(
    program,
    config.compilerOptions.classPath.map(p => resolveConfigPath(config, p)),
  );
  for (const dir of config.compilerOptions.sourcePaths) {
    try {
      for (const { uri, text } of loadJavaFiles(resolveConfigPath(config, dir))) {
        program.addProjectFile(uri, text);
      }
    } catch {
      // a missing source path entry never breaks the build
    }
  }
}

export function runCompile(files: string[], options: CompileOptions = {}): number {
  if (files.length === 0) {
    process.stderr.write("usage: compile [-d <outdir>] <file.java> ...\n");
    return 2;
  }
  const quiet = options.quiet ?? options.config?.compilerOptions.quiet ?? false;
  const failOnDegrade =
    options.failOnDegrade ?? options.config?.compilerOptions.failOnDegrade ?? false;
  const outDir = options.outDir ?? options.config?.compilerOptions.outDir;

  // One program over all inputs (+ the JDK stub + the configured classpath and
  // source paths) so type descriptors resolve.
  const program = createProgram();
  loadJdkStub(program);
  if (options.config) loadConfiguredPaths(program, options.config);
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
    const target = outDir ?? ".";
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
        if (!quiet) process.stdout.write(`${out}\n`);
      }
    }
  } finally {
    setDegradeListener(undefined);
  }

  for (const entry of degraded) {
    process.stderr.write(`warning: ${entry}: unsupported construct, emitted a placeholder body\n`);
  }
  if (degraded.length > 0 && failOnDegrade) {
    process.stderr.write(`error: ${degraded.length} method(s) degraded (--fail-on-degrade)\n`);
    return 1;
  }
  return 0;
}
