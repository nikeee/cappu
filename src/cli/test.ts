// `cappu test`: build main + test classes, then stream a JUnit Platform
// Console Launcher run. Exits with the launcher's code (0 = all green).

import { spawnSync } from "node:child_process";

import { type CompileDiagnostic, runCompile } from "../compiler/compiler.ts";
import type { CappuConfig } from "../config.ts";
import { resolveConfigPath } from "../config.ts";
import {
  compileTests,
  consoleLauncherJar,
  findTestSources,
  mainClassesDir,
  resolveJava,
  testRunArgs,
} from "../testing/index.ts";
import { findJavaFiles } from "../workspace.ts";

function renderDiagnostics(diagnostics: readonly CompileDiagnostic[]): void {
  for (const d of diagnostics) {
    const location = d.file !== undefined && d.line !== undefined ? `${d.file}:${d.line}: ` : "";
    process.stderr.write(`${location}${d.severity}: ${d.message}\n`);
  }
}

export async function runTestCommand(config: CappuConfig): Promise<never> {
  const testSources = findTestSources(config);
  if (testSources.length === 0) {
    process.stderr.write("cappu: no tests found under ./src/test/java\n");
    process.exit(1);
  }

  // 1. main classes (annotation processors and resources included), into the
  // derived .cappu/test-build/classes tree
  const mainSources = config.compilerOptions.sourcePaths.flatMap(p =>
    findJavaFiles(resolveConfigPath(config, p)),
  );
  if (mainSources.length > 0) {
    const main = runCompile(mainSources, {
      outDir: mainClassesDir(config),
      output: "classes",
      config,
    });
    if (!main.success) {
      renderDiagnostics(main.diagnostics);
      process.exit(1);
    }
  }

  // 2. test classes against main + lib/classes + lib/test-classes
  const diagnostics = compileTests(config, testSources);
  if (diagnostics.length > 0) {
    renderDiagnostics(diagnostics);
    if (diagnostics.some(d => d.severity === "error")) process.exit(1);
  }

  // 3. the JUnit run, streamed (the launcher's exit code is ours)
  let launcher: string;
  try {
    launcher = await consoleLauncherJar(config);
  } catch (e) {
    process.stderr.write(`cappu: ${(e as Error).message}\n`);
    process.exit(1);
  }
  const result = spawnSync(resolveJava(config), testRunArgs(config, launcher), {
    stdio: "inherit",
  });
  if (result.error) {
    process.stderr.write(`cappu: could not run java: ${result.error.message}\n`);
    process.exit(1);
  }
  process.exit(result.status ?? 1);
}
