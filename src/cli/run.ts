// `cappu run [-- <args>]`: build the project to a class tree and run it on the
// JVM, the way `cargo run` / `uv run` end the happy path inside the tool instead
// of dropping the user to a raw `java -jar dist/<name>.jar`. The main class is
// the configured compilerOptions.mainClass, else the single class that declares
// main(String[]); a 0/ambiguous result is an error. Compiles to a private
// .cappu/run-build/classes tree (gitignored, no jar packaging), assembles the
// runtime classpath from it plus the configured dependency classPath, and execs
// the same java the test runner resolves.

import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { delimiter, relative, sep } from "node:path";

import { classDeclaresMain } from "../compiler/classfileReader.ts";
import { runCompile } from "../compiler/compiler.ts";
import { expandedClassPath } from "../compiler/javacPaths.ts";
import { type CappuConfig, resolveConfigPath } from "../config.ts";
import { resolveJava } from "../testing/index.ts";
import { findSourceJavaFiles } from "../workspace.ts";
import { emitAnnotation } from "./annotations.ts";
import { renderDiagnostics } from "./renderDiagnostics.ts";

// A private build tree (gitignored under /.cappu/), like .cappu/test-build.
const RUN_CLASSES_DIR = ".cappu/run-build/classes";

/**
 * Pick the class to run: the configured mainClass wins; otherwise the single
 * detected entry point. Pure, so the 0/1/many policy is unit-tested without
 * class bytes.
 */
export function selectMainClass(
  detected: readonly string[],
  configured: string | undefined,
): { mainClass: string } | { error: string } {
  if (configured) return { mainClass: configured };
  if (detected.length === 1) return { mainClass: detected[0] };
  if (detected.length === 0) {
    return { error: "no class declares a main(String[]) method; set compilerOptions.mainClass" };
  }
  return {
    error: `several classes declare main(String[]) (${detected.join(", ")}); set compilerOptions.mainClass to pick one`,
  };
}

// The fully qualified names of the compiled .class files that declare a main
// method (com/app/Main.class -> com.app.Main).
function detectMainClasses(written: readonly string[], outDir: string): string[] {
  return written
    .filter(f => f.endsWith(".class"))
    .filter(f => classDeclaresMain(readFileSync(f)))
    .map(f => relative(outDir, f).replace(/\.class$/, "").split(sep).join("."));
}

export async function runRunCommand(args: string[], config: CappuConfig): Promise<never> {
  const sources = findSourceJavaFiles(config);
  if (sources.length === 0) {
    process.stderr.write("cappu: no .java files under the configured sourcePaths\n");
    process.exit(2);
  }

  const outDir = resolveConfigPath(config, RUN_CLASSES_DIR);
  const result = runCompile(sources, { outDir, output: "classes", config });
  for (const w of result.warnings ?? []) {
    process.stderr.write(`warning: ${w}\n`);
    emitAnnotation("warning", w);
  }
  if (!result.success) {
    renderDiagnostics(result.diagnostics);
    process.exit(1);
  }
  renderDiagnostics(result.diagnostics ?? []);

  const picked = selectMainClass(
    detectMainClasses(result.written, outDir),
    config.compilerOptions.mainClass,
  );
  if ("error" in picked) {
    process.stderr.write(`cappu: ${picked.error}\n`);
    emitAnnotation("error", picked.error);
    process.exit(2);
  }

  const classPath = [outDir, ...expandedClassPath(config)].join(delimiter);
  const child = spawnSync(resolveJava(config), ["-cp", classPath, picked.mainClass, ...args], {
    stdio: "inherit",
  });
  if (child.error) {
    process.stderr.write(`cappu: could not run java: ${child.error.message}\n`);
    process.exit(1);
  }
  process.exit(child.status ?? 1);
}
