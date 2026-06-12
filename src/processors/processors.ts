// JSR-269 annotation processing (nikeee/cappu#7). Processors are arbitrary
// JVM bytecode, so cappu never executes them itself: generation is delegated
// to a real javac. The default compile (which IS javac, #17) just adds
// -processorpath/-s to its single invocation; the experimental compiler runs
// a separate `-proc:only` generation pass first and then compiles original +
// generated sources itself. Self-contained, mirroring src/packages/: nothing
// here prints, `exec` is injectable for tests; the CLI renders diagnostics.

import { spawnSync } from "node:child_process";
import {
  existsSync,
  globSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  renameSync,
  rmSync,
} from "node:fs";
import { delimiter, dirname, join } from "node:path";

import { type CompileDiagnostic, parseJavacDiagnostics } from "../compiler/javacDiagnostics.ts";
import { expandedClassPath } from "../compiler/javacPaths.ts";
import { readZipEntries } from "../compiler/zipReader.ts";
import { type CappuConfig, DEFAULT_PROCESSOR_PATH, resolveConfigPath } from "../config.ts";
import { provisionedJavac } from "../jdks/index.ts";

// Generated output is a derived artifact under .cappu/ (gitignored):
// <root>/sources holds Filer SOURCE_OUTPUT (.java), <root>/classes holds
// CLASS_OUTPUT (resources like META-INF/services, pre-built class files).
export const GENERATED_ROOT = ".cappu/generated-sources";

export function generatedRoot(config: CappuConfig): string {
  return resolveConfigPath(config, GENERATED_ROOT);
}

/** The generated .java tree (an implicit extra source path when present). */
export function generatedSourcesDir(config: CappuConfig): string {
  return join(generatedRoot(config), "sources");
}

/** Filer CLASS_OUTPUT (merged into the build output like resources). */
export function generatedClassesDir(config: CappuConfig): string {
  return join(generatedRoot(config), "classes");
}

/** The processor jars installed under lib/processors, if any. */
export function processorJars(config: CappuConfig): string[] {
  const dir = resolveConfigPath(config, DEFAULT_PROCESSOR_PATH);
  if (!existsSync(dir)) return [];
  try {
    return readdirSync(dir)
      .filter(f => f.endsWith(".jar"))
      .toSorted()
      .map(f => join(dir, f));
  } catch {
    return [];
  }
}

/**
 * The processor implementation classes a set of jars declares via
 * META-INF/services/javax.annotation.processing.Processor. Informational
 * (javac discovers them itself from -processorpath); corrupt jars contribute
 * nothing, comments and blank lines are ignored (ServiceLoader format).
 */
export function discoverProcessors(jarPaths: readonly string[]): string[] {
  const processors: string[] = [];
  for (const path of jarPaths) {
    try {
      const entries = readZipEntries(readFileSync(path)) ?? [];
      const services = entries.find(
        e => e.name === "META-INF/services/javax.annotation.processing.Processor",
      );
      if (!services) continue;
      for (const line of new TextDecoder().decode(services.read()).split("\n")) {
        const name = line.split("#")[0]!.trim();
        if (name) processors.push(name);
      }
    } catch {
      // a corrupt jar contributes nothing, as everywhere else
    }
  }
  return processors;
}

export interface ExecResult {
  /** null when the binary could not be spawned at all. */
  status: number | null;
  stderr: string;
  /** Set when spawning failed (ENOENT and friends). */
  error?: Error;
}

export type Exec = (bin: string, args: string[]) => ExecResult;

const defaultExec: Exec = (bin, args) => {
  const result = spawnSync(bin, args, { stdio: ["ignore", "ignore", "pipe"] });
  return { status: result.status, stderr: result.stderr?.toString() ?? "", error: result.error };
};

export interface ProcessingResult {
  /** False when no processor jars are installed (nothing was executed). */
  ran: boolean;
  /** Generated .java files (under generatedSourcesDir), present on success. */
  generatedFiles: string[];
  diagnostics: CompileDiagnostic[];
}

// The -proc:only generation arguments (experimental-compiler mode): all
// rounds run, generated sources land in out.sources, Filer CLASS_OUTPUT in
// out.classes; no project class files are emitted.
export function procOnlyArgs(
  config: CappuConfig,
  files: readonly string[],
  jars: readonly string[],
  out: { sources: string; classes: string },
): string[] {
  const classPath = expandedClassPath(config);
  const sourcePaths = config.compilerOptions.sourcePaths
    .map(p => resolveConfigPath(config, p))
    .filter(p => existsSync(p));
  return [
    "-proc:only",
    "-processorpath",
    jars.join(delimiter),
    "-s",
    out.sources,
    "-d",
    out.classes,
    "-encoding",
    "UTF-8",
    ...(config.compilerOptions.release !== undefined
      ? ["--release", String(config.compilerOptions.release)]
      : []),
    ...(classPath.length > 0 ? ["-cp", classPath.join(delimiter)] : []),
    ...(sourcePaths.length > 0 ? ["-sourcepath", sourcePaths.join(delimiter)] : []),
    ...files,
  ];
}

/**
 * Run the `-proc:only` generation pass (experimental-compiler mode). The new
 * generation replaces .cappu/generated-sources only when javac exits 0, so a
 * failed processor run keeps the last good generation. `ran: false` - with no
 * exec call at all - when no processor jars are installed.
 */
export function runAnnotationProcessing(
  config: CappuConfig,
  files: readonly string[],
  exec: Exec = defaultExec,
): ProcessingResult {
  const jars = processorJars(config);
  if (jars.length === 0) return { ran: false, generatedFiles: [], diagnostics: [] };

  const javac = provisionedJavac(config) ?? config.compilerOptions.javac;
  const target = generatedRoot(config);
  // Stage into a temp sibling and swap on success - never a half-written tree.
  mkdirSync(dirname(target), { recursive: true });
  const stage = mkdtempSync(`${target}.next-`);
  try {
    const out = { sources: join(stage, "sources"), classes: join(stage, "classes") };
    mkdirSync(out.sources, { recursive: true });
    mkdirSync(out.classes, { recursive: true });

    const result = exec(javac, procOnlyArgs(config, files, jars, out));
    if (result.error || result.status === null) {
      return {
        ran: true,
        generatedFiles: [],
        diagnostics: [
          {
            severity: "error",
            message:
              `annotation processing needs javac: '${javac}' could not run ` +
              `(${result.error?.message ?? "unknown error"}); set compilerOptions.javac ` +
              `or configure "jdk"`,
          },
        ],
      };
    }
    if (result.status !== 0) {
      const diagnostics = parseJavacDiagnostics(result.stderr);
      return {
        ran: true,
        generatedFiles: [],
        diagnostics:
          diagnostics.length > 0
            ? diagnostics
            : [
                {
                  severity: "error",
                  message: `annotation processing failed: ${
                    result.stderr.trim().slice(-400) || `${javac} exited ${result.status}`
                  }`,
                },
              ],
      };
    }
    // Success: only LOCATED warnings survive (Messager notes print as
    // "Note: ..." lines, which the parser would otherwise collapse into a
    // bogus unlocated error).
    const warnings = parseJavacDiagnostics(result.stderr).filter(
      d => d.severity === "warning" && d.file !== undefined,
    );
    rmSync(target, { recursive: true, force: true });
    renameSync(stage, target);
    const generatedFiles = globSync("sources/**/*.java", { cwd: target }).map(f => join(target, f));
    return { ran: true, generatedFiles, diagnostics: warnings };
  } finally {
    // a no-op after the successful rename
    rmSync(stage, { recursive: true, force: true });
  }
}
