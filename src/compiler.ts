// Minimal compiler core: javac-lite. Reads .java files, parses them and
// writes one .class file per top-level class under the output root, mirroring
// each class's package as a directory path (com.app.Foo -> com/app/Foo.class) so
// the tree can be packed straight into a jar. Output root defaults to the cwd.
// Code generation is at an early stage - see emitter.ts. Invoked via cli.ts.
//
// runCompile never prints: it returns what was written, what degraded and the
// diagnostics; the caller decides how to render them.

import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";

import { setDegradeListener } from "./bytecode.ts";
import { createChecker } from "./checker.ts";
import { loadClassPath } from "./classfileReader.ts";
import { type CappuConfig, resolveConfigPath } from "./config.ts";
import { emitSourceFile } from "./emitter.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { computeLineStarts, getLineAndCharacterOfPosition } from "./lineMap.ts";
import { createProgram, type Program } from "./program.ts";
import { type Diagnostic, DiagnosticCategory, type SourceFile } from "./types.ts";
import { loadJavaFiles, pathToUri } from "./workspace.ts";

export interface CompileOptions {
  outDir?: string;
  /** Treat degraded (placeholder) method bodies as a build failure. */
  failOnDegrade?: boolean;
  /** Run the type checker over the inputs and fail on semantic errors. Default: true. */
  typeCheck?: boolean;
  /** Project configuration (cappu.json); explicit options take precedence. */
  config: CappuConfig;
}

/** A source diagnostic located for display (1-based line/column). */
export interface CompileDiagnostic {
  severity: "error" | "warning";
  /** The input file the diagnostic belongs to, if it has one. */
  file?: string;
  line?: number;
  column?: number;
  code?: number;
  message: string;
}

interface CompileOutput {
  /** Paths of the .class files written, in emission order. */
  written: string[];
  /** Members emitted with a placeholder body (binary name + member). */
  degraded: string[];
}

export type CompileResult =
  | ({ success: true } & CompileOutput)
  | ({ success: false; diagnostics: CompileDiagnostic[] } & CompileOutput);

/**
 * Register the config's classPath (.class stubs) and sourcePaths (.java
 * sources, for resolution only - they are not compiled) into a program.
 */
export function loadConfiguredPaths(program: Program, config: CappuConfig): void {
  loadClassPath(
    program,
    config.compilerOptions.classPath.map(p => resolveConfigPath(config, p)),
  );
  for (const dir of config.compilerOptions.sourcePaths) {
    try {
      for (const { uri, text } of loadJavaFiles(resolveConfigPath(config, dir))) {
        program.addProjectFile(uri, text);
      }
    } catch {
      // a missing source path entry never breaks the build
    }
  }
}

function toCompileDiagnostic(d: Diagnostic, file: string, sourceFile: SourceFile): CompileDiagnostic {
  const { line, character } = getLineAndCharacterOfPosition(computeLineStarts(sourceFile.text), d.pos);
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
  const typeCheck = options.typeCheck ?? true;

  // One program over all inputs (+ the JDK stub + the configured classpath and
  // source paths) so type descriptors resolve.
  const program = createProgram();
  loadJdkStub(program);
  if (options.config) loadConfiguredPaths(program, options.config);
  for (const file of files) program.addProjectFile(pathToUri(file), readFileSync(file, "utf8"));
  const checker = createChecker(program);

  // All diagnostics over all inputs before emitting anything (as javac does).
  const diagnostics: CompileDiagnostic[] = [];
  for (const file of files) {
    const sourceFile = program.getSourceFile(pathToUri(file))!;
    const fileDiagnostics = [
      ...sourceFile.parseDiagnostics,
      ...(sourceFile.bindDiagnostics ?? []),
      ...(typeCheck ? checker.getSemanticDiagnostics(sourceFile) : []),
    ];
    diagnostics.push(...fileDiagnostics.map(d => toCompileDiagnostic(d, file, sourceFile)));
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
    // Single output root so every class lands in one coherent package tree.
    for (const file of files) {
      const sourceFile = program.getSourceFile(pathToUri(file))!;
      for (const cls of emitSourceFile(sourceFile, program, checker)) {
        // cls.name is the internal name (com/app/Foo); mirror it as a directory path.
        const out = join(outDir, `${cls.name}.class`);
        mkdirSync(dirname(out), { recursive: true });
        writeFileSync(out, cls.bytes);
        written.push(out);
      }
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
