// Minimal compiler core: javac-lite. Reads .java files, parses them and
// writes one .class file per top-level class under the output root, mirroring
// each class's package as a directory path (com.app.Foo -> com/app/Foo.class) so
// the tree can be packed straight into a jar. Output root defaults to the cwd.
// Code generation is at an early stage - see emitter.ts. Invoked via cli.ts.
//
// runCompile never prints: it returns what was written, what degraded and the
// diagnostics; the caller decides how to render them.

import { execFileSync } from "node:child_process";
import {
  existsSync,
  globSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { basename, delimiter, dirname, join, resolve } from "node:path";

import { setDegradeListener } from "./bytecode.ts";
import { createChecker } from "./checker.ts";
import { classDeclaresMain, loadClassPath } from "./classfileReader.ts";
import { type CappuConfig, resolveConfigPath } from "../config.ts";
import { emitSourceFile } from "./emitter.ts";
import { type CompileDiagnostic, parseJavacDiagnostics } from "./javacDiagnostics.ts";
import { expandedClassPath } from "./javacPaths.ts";
import { provisionedJavac } from "../jdks/index.ts";
import {
  generatedClassesDir,
  generatedSourcesDir,
  processorJars,
  runAnnotationProcessing,
} from "../processors/index.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { computeLineStarts, getLineAndCharacterOfPosition } from "./lineMap.ts";
import { createProgram, type Program } from "./program.ts";
import { type Diagnostic, DiagnosticCategory } from "./types.ts";
import { findFilesRelative, loadJavaFiles, pathToUri } from "../workspace.ts";
import { readZipEntries } from "./zipReader.ts";
import { writeZip, type ZipEntryInput } from "./zipWriter.ts";

export type OutputKind = "classes" | "jar" | "fat-jar";

export interface CompileOptions {
  outDir?: string;
  /** What to produce in outDir (nikeee/cappu#5). Default: the config's, then "classes". */
  output?: OutputKind;
  /** Compile with cappu's own (experimental) compiler instead of javac. */
  experimentalCompiler?: boolean;
  /** Treat degraded (placeholder) method bodies as a build failure. */
  failOnDegrade?: boolean;
  /** Run the type checker over the inputs and fail on semantic errors. Default: true. */
  typeCheck?: boolean;
  /** Project configuration (cappu.json); explicit options take precedence. */
  config: CappuConfig;
}

export { type CompileDiagnostic } from "./javacDiagnostics.ts";

interface CompileOutput {
  /** Paths of the .class files written, in emission order. */
  written: string[];
  /** Members emitted with a placeholder body (binary name + member). */
  degraded: string[];
}

export type CompileResult =
  | ({ success: true } & CompileOutput)
  | ({ success: false; diagnostics: CompileDiagnostic[] } & CompileOutput);

// The dependency class files and jar contents reachable through the
// configured classPath, as archive entries for a fat jar. META-INF/ of the
// dependency jars is dropped (their manifests and signatures must not leak
// into ours); the first occurrence of a path wins.
function classPathEntries(config: CappuConfig): ZipEntryInput[] {
  const entries: ZipEntryInput[] = [];
  const addJar = (path: string): void => {
    try {
      for (const entry of readZipEntries(readFileSync(path)) ?? []) {
        if (entry.name.startsWith("META-INF/") || entry.name.endsWith("/")) continue;
        entries.push({ name: entry.name, bytes: entry.read() });
      }
    } catch {
      // an unreadable or corrupt jar contributes nothing, as everywhere else
    }
  };
  for (const configured of config.compilerOptions.classPath) {
    const root = resolveConfigPath(config, configured);
    if (root.endsWith(".jar")) {
      addJar(root);
      continue;
    }
    if (!existsSync(root)) continue;
    let matches: string[];
    try {
      matches = globSync("**/*.{class,jar}", { cwd: root });
    } catch {
      continue;
    }
    for (const relative of matches) {
      if (relative.endsWith(".jar")) addJar(join(root, relative));
      else entries.push({ name: relative, bytes: readFileSync(join(root, relative)) });
    }
  }
  return entries;
}

// Every file under the configured resourcePaths, as archive entries whose
// names mirror the path relative to the resource root (Maven's layout:
// src/main/resources/a/b.txt -> a/b.txt). A missing resource directory is
// simply empty - projects without resources stay warning-free.
function resourceEntries(config: CappuConfig): ZipEntryInput[] {
  const entries: ZipEntryInput[] = [];
  for (const configured of config.compilerOptions.resourcePaths) {
    const root = resolveConfigPath(config, configured);
    for (const rel of findFilesRelative(root)) {
      // zip entry names use forward slashes whatever the platform
      entries.push({ name: rel.replaceAll("\\", "/"), bytes: readFileSync(join(root, rel)) });
    }
  }
  return entries;
}

// Filer CLASS_OUTPUT of the last annotation-processing pass (generated
// resources such as META-INF/services), as archive entries.
function generatedClassEntries(config: CappuConfig): ZipEntryInput[] {
  const root = generatedClassesDir(config);
  return findFilesRelative(root).map(rel => ({
    name: rel.replaceAll("\\", "/"),
    bytes: readFileSync(join(root, rel)),
  }));
}

/**
 * Configured classPath/sourcePaths entries that do not exist on disk. They are
 * treated as empty everywhere; this is only for warning the user, and only
 * when the paths come from an actual cappu.json (the built-in defaults are
 * allowed to be absent silently).
 */
export function missingConfiguredPaths(config: CappuConfig): string[] {
  if (!config.fromFile) return [];
  return [...config.compilerOptions.classPath, ...config.compilerOptions.sourcePaths]
    .map(p => resolveConfigPath(config, p))
    .filter(p => !existsSync(p));
}

/**
 * Register the config's classPath (.class stubs) and sourcePaths (.java
 * sources, for resolution only - they are not compiled) into a program. A
 * missing entry contributes nothing (see missingConfiguredPaths).
 */
export function loadConfiguredPaths(program: Program, config: CappuConfig): void {
  loadClassPath(
    program,
    config.compilerOptions.classPath.map(p => resolveConfigPath(config, p)),
  );
  // .cappu/generated-sources/sources (annotation-processor output, #7) is an
  // implicit extra source path; absent until the first processing compile.
  const sourceDirs = [
    ...config.compilerOptions.sourcePaths.map(p => resolveConfigPath(config, p)),
    generatedSourcesDir(config),
  ];
  for (const dir of sourceDirs) {
    try {
      for (const { uri, text } of loadJavaFiles(dir)) {
        program.addProjectFile(uri, text);
      }
    } catch {
      // a missing source path entry never breaks the build
    }
  }
}

function toCompileDiagnostic(
  d: Diagnostic,
  file: string,
  lineStarts: readonly number[],
): CompileDiagnostic {
  const { line, character } = getLineAndCharacterOfPosition(lineStarts, d.pos);
  return {
    severity: d.category === DiagnosticCategory.Error ? "error" : "warning",
    file,
    line: line + 1,
    column: character + 1,
    code: d.code,
    message: d.messageText,
  };
}

/**
 * Compile `files` (the caller has already checked the list is non-empty).
 * Parser, binder and - unless `typeCheck` is disabled - checker diagnostics of
 * every input are collected first; any error among them fails the build before
 * anything is written.
 */
export function runCompile(files: string[], options: CompileOptions): CompileResult {
  const failOnDegrade =
    options.failOnDegrade ?? options.config?.compilerOptions.failOnDegrade ?? false;
  const outDir = options.outDir ?? options.config?.compilerOptions.outDir ?? ".";
  const output = options.output ?? options.config?.compilerOptions.output ?? "classes";
  // javac is the default compiler (nikeee/cappu#17); cappu's own pipeline
  // runs only when explicitly requested (--experimental-compiler).
  const experimental =
    options.experimentalCompiler ?? options.config?.compilerOptions.experimentalCompiler ?? false;
  if (!experimental) {
    return runJavacCompile(files, outDir, output, options.config);
  }
  const typeCheck = options.typeCheck ?? true;

  // Annotation processors (nikeee/cappu#7): generation is javac's job even in
  // experimental mode (-proc:only); our compiler then compiles original +
  // generated sources. A processing error fails the build before anything
  // else runs; nothing happens at all when no processor jars are installed.
  let inputs = files;
  if (options.config) {
    const processing = runAnnotationProcessing(options.config, files);
    if (processing.diagnostics.some(d => d.severity === "error")) {
      return { success: false, diagnostics: processing.diagnostics, written: [], degraded: [] };
    }
    if (processing.ran) inputs = [...files, ...processing.generatedFiles];
  }

  // One program over all inputs (+ the JDK stub + the configured classpath and
  // source paths) so type descriptors resolve.
  const program = createProgram();
  loadJdkStub(program);
  if (options.config) loadConfiguredPaths(program, options.config);
  for (const file of inputs) program.addProjectFile(pathToUri(file), readFileSync(file, "utf8"));
  const checker = createChecker(program);

  // All diagnostics over all inputs before emitting anything (as javac does).
  const diagnostics: CompileDiagnostic[] = [];
  for (const file of inputs) {
    const sourceFile = program.getSourceFile(pathToUri(file))!;
    const fileDiagnostics = [
      ...sourceFile.parseDiagnostics,
      ...(sourceFile.bindDiagnostics ?? []),
      ...(typeCheck ? checker.getSemanticDiagnostics(sourceFile) : []),
    ];
    const lineStarts = computeLineStarts(sourceFile.text); // once per file, not per diagnostic
    diagnostics.push(...fileDiagnostics.map(d => toCompileDiagnostic(d, file, lineStarts)));
  }
  if (diagnostics.some(d => d.severity === "error")) {
    return { success: false, diagnostics, written: [], degraded: [] };
  }

  // A degraded body still produces a verifiable class, but silently behaves as
  // a stub; surface every one so the build is honest about what it emitted.
  const degraded: string[] = [];
  setDegradeListener((className, member) => {
    degraded.push(`${className.replaceAll("/", ".")}.${member}`);
  });

  const written: string[] = [];
  try {
    const classes: ZipEntryInput[] = [];
    const mainClasses: string[] = [];
    for (const file of inputs) {
      const sourceFile = program.getSourceFile(pathToUri(file))!;
      for (const cls of emitSourceFile(sourceFile, program, checker)) {
        // cls.name is the internal name (com/app/Foo); as a path it mirrors
        // the package tree.
        classes.push({ name: `${cls.name}.class`, bytes: cls.bytes });
        if (cls.hasMainMethod) mainClasses.push(cls.name.replaceAll("/", "."));
      }
    }
    // Resources are copied verbatim next to the classes (Maven-style), so
    // Class.getResource works the same from the tree and from the jar; our
    // class files win on a name collision.
    const haveNames = new Set(classes.map(c => c.name));
    const resources = [
      ...resourceEntries(options.config),
      // Filer CLASS_OUTPUT of the processing pass (generated resources)
      ...generatedClassEntries(options.config),
    ].filter(r => !haveNames.has(r.name) && (haveNames.add(r.name), true));
    if (output === "classes") {
      // A package tree directly under outDir: outDir is a valid `java -cp` root.
      for (const entry of [...classes, ...resources]) {
        const out = join(outDir, entry.name);
        mkdirSync(dirname(out), { recursive: true });
        writeFileSync(out, entry.bytes);
        written.push(out);
      }
    } else {
      // Main-Class makes `java -jar` work (nikeee/cappu#11): the configured one
      // wins; otherwise the single detected main(String[]) entry point.
      const mainClass =
        options.config.compilerOptions.mainClass ??
        (mainClasses.length === 1 ? mainClasses[0] : undefined);
      const manifest: ZipEntryInput = {
        name: "META-INF/MANIFEST.MF",
        bytes: new TextEncoder().encode(
          `Manifest-Version: 1.0\r\n${mainClass ? `Main-Class: ${mainClass}\r\n` : ""}\r\n`,
        ),
      };
      const entries = [manifest, ...classes, ...resources];
      if (output === "fat-jar") {
        const have = new Set(entries.map(e => e.name));
        for (const entry of options.config ? classPathEntries(options.config) : []) {
          if (have.has(entry.name)) continue; // our classes win over dependencies
          have.add(entry.name);
          entries.push(entry);
        }
      }
      // The archive is named after the project directory (where cappu.json lives).
      const jar = join(outDir, `${basename(resolve(options.config.baseDir))}.jar`);
      mkdirSync(outDir, { recursive: true });
      writeFileSync(jar, writeZip(entries));
      written.push(jar);
    }
  } finally {
    setDegradeListener(undefined);
  }

  if (degraded.length > 0 && failOnDegrade) {
    return {
      success: false,
      diagnostics: [
        ...diagnostics,
        {
          severity: "error" as const,
          message: `${degraded.length} method(s) degraded to a placeholder body (--fail-on-degrade)`,
        },
      ],
      written,
      degraded,
    };
  }
  return { success: true, written, degraded };
}

// javac's "path:line: error: message" diagnostics, located like our own; any
// unmatched stderr tail becomes one unlocated error.
/**
 * `cappu compile --use-javac`: the configured javac compiles and emits; none
 * of cappu's own pipeline runs. The configured classPath/sourcePaths become
 * -cp/-sourcepath, and the output kinds reuse the same packaging - with
 * Main-Class detected from javac's CLASS BYTES (via the class-file reader),
 * not from source.
 */
function runJavacCompile(
  files: string[],
  outDir: string,
  output: OutputKind,
  config: CappuConfig,
): CompileResult {
  // The provisioned JDK's javac (config "jdk", nikeee/cappu#8) wins over the
  // configured/PATH binary.
  const javacBin = provisionedJavac(config) ?? config.compilerOptions.javac;
  const tmp = mkdtempSync(join(tmpdir(), "cappu-javac-"));
  try {
    const classPath = expandedClassPath(config);
    const sourcePaths = config.compilerOptions.sourcePaths
      .map(p => resolveConfigPath(config, p))
      .filter(p => existsSync(p));
    // Annotation processors (nikeee/cappu#7): javac discovers and runs them
    // from -processorpath in this same invocation; generated sources go to
    // .cappu/generated-sources/sources so the LSP sees them too.
    const processors = processorJars(config);
    if (processors.length > 0) mkdirSync(generatedSourcesDir(config), { recursive: true });
    const args = [
      "-d",
      tmp,
      "-encoding",
      "UTF-8",
      ...(config.compilerOptions.release !== undefined
        ? ["--release", String(config.compilerOptions.release)]
        : []),
      ...(processors.length > 0
        ? ["-processorpath", processors.join(delimiter), "-s", generatedSourcesDir(config)]
        : []),
      ...(classPath.length > 0 ? ["-cp", classPath.join(delimiter)] : []),
      ...(sourcePaths.length > 0 ? ["-sourcepath", sourcePaths.join(delimiter)] : []),
      ...files,
    ];
    try {
      execFileSync(javacBin, args, { stdio: ["ignore", "pipe", "pipe"] });
    } catch (error) {
      const stderr = (error as { stderr?: Buffer }).stderr?.toString() ?? "";
      const diagnostics = parseJavacDiagnostics(stderr);
      return {
        success: false,
        diagnostics: diagnostics.length
          ? diagnostics
          : [{ severity: "error", message: `${javacBin} failed: ${(error as Error).message}` }],
        written: [],
        degraded: [],
      };
    }

    // Everything javac (and Filer CLASS_OUTPUT: generated resources like
    // META-INF/services) wrote, not just .class files.
    const outputFiles = findFilesRelative(tmp);
    // Project resources (#12) ship in the default mode too; javac's own
    // outputs win on a collision.
    const haveNames = new Set(outputFiles.map(f => f.replaceAll("\\", "/")));
    const resources = resourceEntries(config).filter(r => !haveNames.has(r.name));
    const written: string[] = [];
    if (output === "classes") {
      for (const rel of outputFiles) {
        const target = join(outDir, rel);
        mkdirSync(dirname(target), { recursive: true });
        writeFileSync(target, readFileSync(join(tmp, rel)));
        written.push(target);
      }
      for (const entry of resources) {
        const target = join(outDir, entry.name);
        mkdirSync(dirname(target), { recursive: true });
        writeFileSync(target, entry.bytes);
        written.push(target);
      }
    } else {
      const classes: ZipEntryInput[] = [];
      const mainClasses: string[] = [];
      for (const rel of outputFiles) {
        const bytes = readFileSync(join(tmp, rel));
        classes.push({ name: rel.replaceAll("\\", "/"), bytes });
        if (rel.endsWith(".class") && classDeclaresMain(bytes)) {
          mainClasses.push(
            rel
              .replace(/\.class$/, "")
              .replaceAll("/", ".")
              .replaceAll("\\", "."),
          );
        }
      }
      const mainClass =
        config.compilerOptions.mainClass ?? (mainClasses.length === 1 ? mainClasses[0] : undefined);
      const entries: ZipEntryInput[] = [
        {
          name: "META-INF/MANIFEST.MF",
          bytes: new TextEncoder().encode(
            `Manifest-Version: 1.0\r\n${mainClass ? `Main-Class: ${mainClass}\r\n` : ""}\r\n`,
          ),
        },
        ...classes,
        ...resources,
      ];
      if (output === "fat-jar") {
        const have = new Set(entries.map(e => e.name));
        for (const entry of classPathEntries(config)) {
          if (have.has(entry.name)) continue;
          have.add(entry.name);
          entries.push(entry);
        }
      }
      const jar = join(outDir, `${basename(resolve(config.baseDir))}.jar`);
      mkdirSync(outDir, { recursive: true });
      writeFileSync(jar, writeZip(entries));
      written.push(jar);
    }
    return { success: true, written, degraded: [] };
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
}
