// Resolving and building the program cappu launches under the debugger. v1
// debugs a configured (or launch-supplied) main class. Sources are compiled
// with javac -g into a dedicated debug-build tree so the bytecode carries the
// LocalVariableTable that variable inspection needs (default `cappu compile`
// omits it to match plain javac). Mirrors src/testing/testing.ts's build/run
// classpath plumbing.
//
// Port reference for togo/internal/dapserver/debuggee.go.

import { execFileSync } from "node:child_process";
import { mkdirSync, rmSync } from "node:fs";
import { delimiter } from "node:path";

import { type CompileDiagnostic, parseJavacDiagnostics } from "../../compiler/javacDiagnostics.ts";
import { expandedJarDirs } from "../../compiler/javacPaths.ts";
import { type CappuConfig, resolveConfigPath } from "../../config.ts";
import { provisionedJavac } from "../../jdks/index.ts";
import { findSourceJavaFiles } from "../../workspace.ts";
import type { LaunchArguments } from "./protocol.ts";

const DEBUG_BUILD_CLASSES = ".cappu/debug-build/classes";

export function debugClassesDir(config: CappuConfig): string {
  return resolveConfigPath(config, DEBUG_BUILD_CLASSES);
}

function libClassPath(config: CappuConfig): string[] {
  return expandedJarDirs(config.compilerOptions.classPath.map(p => resolveConfigPath(config, p)));
}

/** Compile src/main/java with javac -g; diagnostics non-empty on failure. */
export function compileForDebug(config: CappuConfig): CompileDiagnostic[] {
  const sources = findSourceJavaFiles(config);
  if (sources.length === 0) {
    return [{ severity: "error", message: "no sources under src/main/java to debug" }];
  }
  const dir = debugClassesDir(config);
  rmSync(dir, { recursive: true, force: true });
  mkdirSync(dir, { recursive: true });
  const javac = provisionedJavac(config) ?? config.compilerOptions.javac;
  const cp = libClassPath(config);
  const args = [
    "-g", // full debug info: lines, source, LocalVariableTable
    "-d",
    dir,
    "-encoding",
    "UTF-8",
    ...(config.compilerOptions.release !== undefined
      ? ["--release", String(config.compilerOptions.release)]
      : []),
    ...(cp.length > 0 ? ["-cp", cp.join(delimiter)] : []),
    ...sources,
  ];
  try {
    execFileSync(javac, args, { stdio: ["ignore", "pipe", "pipe"] });
    return [];
  } catch (e) {
    const stderr = (e as { stderr?: Buffer }).stderr?.toString() ?? "";
    const diagnostics = parseJavacDiagnostics(stderr);
    return diagnostics.length
      ? diagnostics
      : [{ severity: "error", message: `javac failed: ${(e as Error).message}` }];
  }
}

/** Runtime classpath: the debug classes plus the resolved dependency jars. */
export function debuggeeClassPath(config: CappuConfig, extra: string[] = []): string {
  return [debugClassesDir(config), ...libClassPath(config), ...extra].join(delimiter);
}

/**
 * The debuggee's JVM args: the project-wide defaults (e.g. -ea from
 * dapOptions.enableAssertions) first, then the launch request's own vmArgs, so
 * a launch request can still override (a trailing -da disables assertions).
 */
export function debuggeeVmArgs(config: CappuConfig, args: LaunchArguments): string[] {
  const defaults = config.dapOptions.enableAssertions ? ["-ea"] : [];
  return [...defaults, ...(args.vmArgs ?? [])];
}

export function resolveMainClass(config: CappuConfig, args: LaunchArguments): string {
  const mainClass = args.mainClass ?? config.compilerOptions.mainClass;
  if (!mainClass) {
    throw new Error(
      "no main class: set compilerOptions.mainClass in cappu.json or pass mainClass in the launch request",
    );
  }
  return mainClass;
}
