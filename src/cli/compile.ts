// `cappu compile`: run the print-free compile pipeline and render its result.
// With no files, this is a project build over the configured sourcePaths.

import { writeFileSync } from "node:fs";

import { missingConfiguredPaths, type OutputKind, runCompile } from "../compiler/compiler.ts";
import type { CappuConfig } from "../config.ts";
import { validateAgainstJavac } from "../compiler/validateJavac.ts";
import { generatePom, missingCoordinates } from "../publish/index.ts";
import { renderDiagnostics } from "./renderDiagnostics.ts";
import { findSourceJavaFiles } from "../workspace.ts";

export interface CompileFlags {
  /** Raw --output value; validated here. */
  output?: string;
  experimentalCompiler?: boolean;
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
  const inputs = files.length > 0 ? files : findSourceJavaFiles(config);
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
  const experimental =
    flags.experimentalCompiler ?? config.compilerOptions.experimentalCompiler ?? false;
  // --validate compares OUR bytecode against javac's; --fail-on-degrade is
  // about OUR placeholder bodies - both only mean something with the
  // experimental compiler (the default IS javac).
  if (flags.validate && !experimental) {
    process.stderr.write(
      "cappu: --validate requires --experimental-compiler (javac is the default)\n",
    );
    process.exit(2);
  }
  if (flags.failOnDegrade && !experimental) {
    process.stderr.write(
      "cappu: --fail-on-degrade requires --experimental-compiler (javac never degrades)\n",
    );
    process.exit(2);
  }
  const result = runCompile(inputs, {
    output,
    experimentalCompiler: flags.experimentalCompiler,
    failOnDegrade: flags.failOnDegrade,
    config,
  });
  // runCompile is print-free; render its outcome here.
  const quiet = flags.quiet ?? config.compilerOptions.quiet ?? false;
  if (!quiet) for (const out of result.written) process.stdout.write(`${out}\n`);
  for (const entry of result.degraded) {
    process.stderr.write(`warning: ${entry}: unsupported construct, emitted a placeholder body\n`);
  }
  for (const w of result.warnings ?? []) process.stderr.write(`warning: ${w}\n`);
  if (!result.success) {
    renderDiagnostics(result.diagnostics);
    process.exit(1);
  }
  // A plain jar with full Maven coordinates is publishable: emit its POM beside
  // it so `cappu publish` (or any registry upload) has the descriptor. fat-jar
  // shades its dependencies, so a deps-listing POM beside it would be wrong.
  if (effectiveOutput === "jar" && missingCoordinates(config).length === 0) {
    const jar = result.written.find(f => f.endsWith(".jar"));
    if (jar) {
      const pomPath = jar.replace(/\.jar$/, ".pom");
      writeFileSync(pomPath, generatePom(config));
      if (!quiet) process.stdout.write(`${pomPath}\n`);
    }
  }
  if (flags.validate) {
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
