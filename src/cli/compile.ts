// `cappu compile`: run the print-free compile pipeline and render its result.
// With no files, this is a project build over the configured sourcePaths.

import { missingConfiguredPaths, type OutputKind, runCompile } from "../compiler/compiler.ts";
import { type CappuConfig, resolveConfigPath } from "../config.ts";
import { findJavaFiles } from "../workspace.ts";

export interface CompileFlags {
  outDir?: string;
  /** Raw --output value; validated here. */
  output?: string;
  quiet?: boolean;
  failOnDegrade?: boolean;
  validate: boolean;
}

const OUTPUT_KINDS: readonly OutputKind[] = ["classes", "jar", "fat-jar"];

export async function runCompileCommand(
  files: string[],
  flags: CompileFlags,
  config: CappuConfig,
): Promise<never> {
  // No explicit files: a project build - compile the configured sourcePaths
  // (they are resolution-only context when explicit files are given).
  const inputs =
    files.length > 0
      ? files
      : config.compilerOptions.sourcePaths.flatMap(p =>
          findJavaFiles(resolveConfigPath(config, p)),
        );
  if (inputs.length === 0) {
    process.stderr.write(
      "usage: cappu compile [-d <outdir>] <file.java> ...\n" +
        "(no files given and the configured sourcePaths contain no .java files)\n",
    );
    process.exit(2);
  }
  // Missing configured dirs are treated as empty; warn only when they come
  // from an actual cappu.json.
  for (const path of missingConfiguredPaths(config)) {
    process.stderr.write(`warning: configured path not found (treated as empty): ${path}\n`);
  }
  const output = OUTPUT_KINDS.find(k => k === flags.output);
  if (flags.output !== undefined && output === undefined) {
    process.stderr.write(`cappu: invalid --output '${flags.output}' (classes, jar, fat-jar)\n`);
    process.exit(2);
  }
  const effectiveOutput = output ?? config.compilerOptions.output;
  if (flags.validate && effectiveOutput !== "classes") {
    process.stderr.write("cappu: --validate requires --output classes (javap reads class files)\n");
    process.exit(2);
  }
  const result = runCompile(inputs, {
    outDir: flags.outDir,
    output,
    failOnDegrade: flags.failOnDegrade,
    config,
  });
  // runCompile is print-free; render its outcome here.
  const quiet = flags.quiet ?? config.compilerOptions.quiet ?? false;
  if (!quiet) for (const out of result.written) process.stdout.write(`${out}\n`);
  for (const entry of result.degraded) {
    process.stderr.write(`warning: ${entry}: unsupported construct, emitted a placeholder body\n`);
  }
  if (!result.success) {
    for (const d of result.diagnostics) {
      const location = d.file ? `${d.file}:${d.line}:${d.column}: ` : "";
      const code = d.code !== undefined ? ` ${d.code}` : "";
      process.stderr.write(`${location}${d.severity}${code}: ${d.message}\n`);
    }
    process.exit(1);
  }
  if (flags.validate) {
    const { validateAgainstJavac } = await import("../compiler/validateJavac.ts");
    // `inputs`, not `files`: a project build validates the sourcePaths sources.
    const validation = validateAgainstJavac(inputs, result.written, config.compilerOptions.javac);
    if (!validation.ok) {
      if ("error" in validation) {
        process.stderr.write(`cappu: --validate: ${validation.error}\n`);
      } else {
        for (const m of validation.mismatches) {
          process.stderr.write(`error: ${m.className}: bytecode differs from javac: ${m.detail}\n`);
        }
      }
      process.exit(1);
    }
    if (!quiet) {
      process.stderr.write(`--validate: ${validation.compared} class(es) match javac\n`);
    }
  }
  process.exit(0);
}
