// `cappu test` (nikeee/cappu#16): compile src/test/java against the main
// classes plus lib/classes and lib/test-classes, then run the JUnit Platform
// Console Launcher over the result. Self-contained, mirroring src/packages/:
// nothing here prints and `exec` is injectable; the CLI streams the actual
// JUnit run (live output is the point of a test runner).

import { spawnSync } from "node:child_process";
import { accessSync, constants, existsSync, mkdirSync, realpathSync, writeFileSync } from "node:fs";
import { delimiter, dirname, join } from "node:path";

import { type CompileDiagnostic, parseJavacDiagnostics } from "../compiler/javacDiagnostics.ts";
import { expandedJarDirs } from "../compiler/javacPaths.ts";
import {
  type CappuConfig,
  DEFAULT_TEST_CLASS_PATH,
  DEFAULT_TEST_RESOURCE_PATH,
  DEFAULT_TEST_SOURCE_PATH,
  resolveConfigPath,
} from "../config.ts";
import { configuredSources, storePathFor } from "../install.ts";
import { provisionedJava, provisionedJavac } from "../jdks/index.ts";
import type { Coordinates, PackageSource } from "../packages/index.ts";
import { findJavaFiles } from "../workspace.ts";

// Derived build state, like .cappu/generated-sources (gitignored via /.cappu/).
export const TEST_BUILD_ROOT = ".cappu/test-build";

export function mainClassesDir(config: CappuConfig): string {
  return resolveConfigPath(config, join(TEST_BUILD_ROOT, "classes"));
}

export function testClassesDir(config: CappuConfig): string {
  return resolveConfigPath(config, join(TEST_BUILD_ROOT, "test-classes"));
}

/** All .java files under src/test/java (not configurable yet). */
export function findTestSources(config: CappuConfig): string[] {
  return findJavaFiles(resolveConfigPath(config, DEFAULT_TEST_SOURCE_PATH));
}

// Main classes + compile deps + test deps: what test sources compile against
// and (plus the compiled tests and test resources) what they run with.
function dependencyClassPath(config: CappuConfig): string[] {
  return [
    mainClassesDir(config),
    ...expandedJarDirs(config.compilerOptions.classPath.map(p => resolveConfigPath(config, p))),
    ...expandedJarDirs([resolveConfigPath(config, DEFAULT_TEST_CLASS_PATH)]),
  ];
}

/** The classpath the JUnit launcher runs with. */
export function testRuntimeClassPath(config: CappuConfig): string[] {
  const testResources = resolveConfigPath(config, DEFAULT_TEST_RESOURCE_PATH);
  return [
    testClassesDir(config),
    ...dependencyClassPath(config),
    ...(existsSync(testResources) ? [testResources] : []),
  ];
}

export interface ExecResult {
  status: number | null;
  stderr: string;
  error?: Error;
}

export type Exec = (bin: string, args: string[]) => ExecResult;

const defaultExec: Exec = (bin, args) => {
  const result = spawnSync(bin, args, { stdio: ["ignore", "ignore", "pipe"] });
  return { status: result.status, stderr: result.stderr?.toString() ?? "", error: result.error };
};

/** The javac arguments compiling the test sources. */
export function compileTestsArgs(config: CappuConfig, sources: readonly string[]): string[] {
  return [
    "-d",
    testClassesDir(config),
    "-encoding",
    "UTF-8",
    "-cp",
    dependencyClassPath(config).join(delimiter),
    ...sources,
  ];
}

/** Compile src/test/java; diagnostics non-empty on failure. */
export function compileTests(
  config: CappuConfig,
  sources: readonly string[],
  exec: Exec = defaultExec,
): CompileDiagnostic[] {
  mkdirSync(testClassesDir(config), { recursive: true });
  const javac = provisionedJavac(config) ?? config.compilerOptions.javac;
  const result = exec(javac, compileTestsArgs(config, sources));
  if (result.error || result.status === null) {
    return [
      {
        severity: "error",
        message: `compiling tests needs javac: '${javac}' could not run (${result.error?.message ?? "unknown error"})`,
      },
    ];
  }
  if (result.status !== 0) {
    const diagnostics = parseJavacDiagnostics(result.stderr);
    return diagnostics.length > 0
      ? diagnostics
      : [
          {
            severity: "error",
            message: `test compilation failed: ${result.stderr.trim().slice(-400)}`,
          },
        ];
  }
  return [];
}

// The JUnit Platform Console Launcher: a TOOL, not a project dependency - it
// never appears in cappu.json or the lockfile. Pinned; bundles the platform
// and the jupiter/vintage engines, so projects only declare junit-jupiter.
export const CONSOLE_LAUNCHER: Coordinates = {
  groupId: "org.junit.platform",
  artifactId: "junit-platform-console-standalone",
  version: "1.12.2",
};

/**
 * The launcher jar's path in the global package store, downloading it there
 * on first use. Sources are injectable for tests.
 */
export async function consoleLauncherJar(
  config: CappuConfig,
  sources: readonly PackageSource[] = configuredSources(config),
): Promise<string> {
  const path = storePathFor(CONSOLE_LAUNCHER);
  if (!path) throw new Error("unreachable: launcher coordinates are store-safe");
  if (existsSync(path)) return path;
  for (const source of sources) {
    const bytes = await source.getArtifact?.(CONSOLE_LAUNCHER);
    if (!bytes) continue;
    mkdirSync(dirname(path), { recursive: true });
    writeFileSync(path, bytes);
    return path;
  }
  throw new Error(
    `could not download ${CONSOLE_LAUNCHER.groupId}:${CONSOLE_LAUNCHER.artifactId}:${CONSOLE_LAUNCHER.version} from any package source`,
  );
}

/** The `java` arguments running the launcher over the compiled tests. */
export function testRunArgs(config: CappuConfig, launcherJar: string): string[] {
  return [
    "-jar",
    launcherJar,
    "execute",
    "--class-path",
    testRuntimeClassPath(config).join(delimiter),
    "--scan-class-path",
  ];
}

/**
 * The java launcher tests run under: the provisioned JDK's, else the sibling
 * of the resolved javac (so a PATH skew between javac and java cannot produce
 * UnsupportedClassVersionError), else plain "java" from PATH.
 */
export function resolveJava(config: CappuConfig): string {
  const provisioned = provisionedJava(config);
  if (provisioned) return provisioned;
  const javac = provisionedJavac(config) ?? config.compilerOptions.javac;
  try {
    const real = realpathSync(javac.includes("/") || javac.includes("\\") ? javac : onPath(javac));
    const sibling = join(dirname(real), process.platform === "win32" ? "java.exe" : "java");
    accessSync(sibling, constants.X_OK);
    return sibling;
  } catch {
    return "java";
  }
}

// Resolve a bare binary name against PATH (throws when absent; the caller
// falls back).
function onPath(name: string): string {
  for (const dir of (process.env.PATH ?? "").split(delimiter)) {
    if (!dir) continue;
    const candidate = join(dir, name);
    try {
      accessSync(candidate, constants.X_OK);
      return candidate;
    } catch {
      // keep looking
    }
  }
  throw new Error(`${name} not on PATH`);
}
