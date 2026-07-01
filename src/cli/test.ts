// `cappu test`: build main + test classes, then stream a JUnit Platform
// Console Launcher run. Exits with the launcher's code (0 = all green).

import { spawnSync } from "node:child_process";
import { mkdirSync } from "node:fs";

import { runCompile } from "../compiler/compiler.ts";
import { type CappuConfig, resolveConfigPath } from "../config.ts";
import {
  compileTests,
  consoleLauncherJar,
  findTestSources,
  jacocoAgentJar,
  mainClassesDir,
  resolveJava,
  testRunArgs,
} from "../testing/index.ts";
import { findSourceJavaFiles } from "../workspace.ts";
import { renderDiagnostics } from "./renderDiagnostics.ts";
import { emitAnnotation } from "./annotations.ts";

export async function runTestCommand(config: CappuConfig): Promise<never> {
  const testSources = findTestSources(config);
  if (testSources.length === 0) {
    process.stderr.write("cappu: no tests found under ./src/test/java\n");
    process.exit(1);
  }

  // 1. main classes (annotation processors and resources included), into the
  // derived .cappu/test-build/classes tree
  const mainSources = findSourceJavaFiles(config);
  if (mainSources.length > 0) {
    const main = runCompile(mainSources, {
      outDir: mainClassesDir(config),
      output: "classes",
      config,
    });
    for (const w of main.warnings ?? []) {
      process.stderr.write(`warning: ${w}\n`);
      emitAnnotation("warning", w);
    }
    if (!main.success) {
      renderDiagnostics(main.diagnostics);
      process.exit(1);
    }
  }

  // 2. test classes against main + .cappu/lib/classes + .cappu/lib/test-classes
  const diagnostics = compileTests(config, testSources);
  if (diagnostics.length > 0) {
    renderDiagnostics(diagnostics);
    if (diagnostics.some(d => d.severity === "error")) process.exit(1);
  }

  // 3. the JUnit run, streamed (the launcher's exit code is ours). With
  // coverage on, also fetch the JaCoCo agent and attach it (writes jacoco.exec
  // into reportsDir).
  let launcher: string;
  let agent: string | undefined;
  try {
    launcher = await consoleLauncherJar(config);
    if (config.testOptions.coverage) {
      agent = await jacocoAgentJar(config);
      // the JaCoCo agent opens destfile but does not create parent dirs
      mkdirSync(resolveConfigPath(config, config.testOptions.reportsDir), { recursive: true });
    }
  } catch (e) {
    process.stderr.write(`cappu: ${(e as Error).message}\n`);
    process.exit(1);
  }
  const result = spawnSync(resolveJava(config), testRunArgs(config, launcher, agent), {
    stdio: "inherit",
  });
  if (result.error) {
    process.stderr.write(`cappu: could not run java: ${result.error.message}\n`);
    process.exit(1);
  }
  process.exit(result.status ?? 1);
}
