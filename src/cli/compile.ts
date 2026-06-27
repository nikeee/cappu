// `cappu compile`: run the print-free compile pipeline and render its result.
// With no files, this is a project build over the configured sourcePaths.

import { writeFileSync } from "node:fs";
import { relative } from "node:path";

import { missingConfiguredPaths, type OutputKind, runCompile } from "../compiler/compiler.ts";
import type { CappuConfig } from "../config.ts";
import { validateAgainstJavac } from "../compiler/validateJavac.ts";
import { generatePom, missingCoordinates } from "../publish/index.ts";
import { renderDiagnostics } from "./renderDiagnostics.ts";
import { emitAnnotation } from "./annotations.ts";
import { findSourceJavaFiles } from "../workspace.ts";

export interface CompileFlags {
  /** Raw --output value; validated here. */
  output?: string;
  /** --artifact: jar base name override (steers the output jar, e.g. for Docker). */
  artifact?: string;
  quiet?: boolean;
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
    emitAnnotation("warning", `configured path not found (treated as empty): ${path}`);
  }
  const output = OUTPUT_KINDS.find(k => k === flags.output);
  if (flags.output !== undefined && output === undefined) {
    process.stderr.write(`cappu: invalid --output '${flags.output}' (classes, jar, fat-jar)\n`);
    emitAnnotation("error", `invalid --output '${flags.output}' (classes, jar, fat-jar)`);
    process.exit(2);
  }
  const effectiveOutput = output ?? config.compilerOptions.output;
  // The experimental compiler and its validate / fail-on-degrade settings live
  // only in cappu.json (compilerOptions.experimentalCompiler), not on the CLI.
  const experimental = config.compilerOptions.experimentalCompiler.enabled;
  // validate (compare our bytecode against javac's) runs only under the
  // experimental compiler and needs a class tree for javap to read.
  const validate = experimental && config.compilerOptions.experimentalCompiler.validate;
  if (validate && effectiveOutput !== "classes") {
    process.stderr.write(
      'cappu: experimentalCompiler.validate needs "output": "classes" (javap reads class files)\n',
    );
    emitAnnotation(
      "error",
      'experimentalCompiler.validate needs "output": "classes" (javap reads class files)',
    );
    process.exit(2);
  }
  const result = runCompile(inputs, {
    output,
    artifactName: flags.artifact?.replace(/\.jar$/, ""),
    config,
  });
  // runCompile is print-free; render its outcome here.
  const quiet = flags.quiet ?? false;
  if (!quiet) for (const out of result.written) process.stdout.write(`${out}\n`);
  for (const entry of result.degraded) {
    process.stderr.write(`warning: ${entry}: unsupported construct, emitted a placeholder body\n`);
    emitAnnotation("warning", `${entry}: unsupported construct, emitted a placeholder body`);
  }
  for (const w of result.warnings ?? []) {
    process.stderr.write(`warning: ${w}\n`);
    emitAnnotation("warning", w);
  }
  if (!result.success) {
    renderDiagnostics(result.diagnostics);
    process.exit(1);
  }
  // A successful build still surfaces its warning-severity diagnostics (e.g.
  // nullness, deprecation); they are non-fatal so the build stays green.
  renderDiagnostics(result.diagnostics ?? []);
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
  if (validate) {
    // `inputs`, not `files`: a project build validates the sourcePaths sources.
    const validation = validateAgainstJavac(inputs, result.written, config.compilerOptions.javac);
    if (!validation.ok) {
      if ("error" in validation) {
        process.stderr.write(`cappu: --validate: ${validation.error}\n`);
        emitAnnotation("error", `--validate: ${validation.error}`);
      } else {
        for (const m of validation.mismatches) {
          process.stderr.write(`error: ${m.className}: bytecode differs from javac: ${m.detail}\n`);
          emitAnnotation("error", `${m.className}: bytecode differs from javac: ${m.detail}`);
        }
      }
      process.exit(1);
    }
    if (!quiet) {
      process.stderr.write(`--validate: ${validation.compared} class(es) match javac\n`);
    }
  }
  // DX: after building a runnable application jar, show how to start it. Only
  // for applications - a library jar has no Main-Class, so result.mainClass is
  // undefined and nothing is printed.
  if (!quiet && (effectiveOutput === "jar" || effectiveOutput === "fat-jar") && result.mainClass) {
    const jar = result.written.find(f => f.endsWith(".jar"));
    if (jar) process.stdout.write(`\nRun it with:\n  java -jar ${relative(process.cwd(), jar)}\n`);
  }
  process.exit(0);
}
