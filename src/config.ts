// cappu.json: project configuration for the compiler and the language server.
// JSONC (comments + trailing commas) via comment-json; shape validation and
// the exported config types both come from one zod schema. Looked up at
// $PWD/cappu.json unless an explicit path is given (--config).

import { existsSync, readFileSync } from "node:fs";
import { isAbsolute, join, resolve } from "node:path";

import { parse } from "comment-json";
import { z } from "zod";

const InlayHintsSchema = z.object({
  /** Hints like `count:` before call arguments that are not plain variables. */
  parameterNames: z.boolean().optional(),
  /** Hints like `: String` after a `var` declaration's name. */
  varTypes: z.boolean().optional(),
});

const CompilerOptionsSchema = z.object({
  /** Directories or .jar files scanned for .class files (resolution only). */
  classPath: z.array(z.string()).default(["./lib/classes"]),
  /** Directories scanned recursively for .java sources (resolution only). */
  sourcePaths: z.array(z.string()).default(["./src/main/java"]),
  /** Output root for the emitted package tree. */
  outDir: z.string().default("./build"),
  quiet: z.boolean().optional(),
  failOnDegrade: z.boolean().optional(),
  /** The javac binary `--validate` compiles the reference output with. */
  javac: z.string().default("javac"),
});

const LspOptionsSchema = z.object({
  inlayHints: InlayHintsSchema.optional(),
});

export const MAVEN_CENTRAL = "https://repo.maven.apache.org/maven2";

const ConfigFileSchema = z.object({
  // prefault (not default): the empty object is parsed through the section
  // schema, so the inner defaults (classPath: [], ...) apply.
  compilerOptions: CompilerOptionsSchema.prefault({}),
  lspOptions: LspOptionsSchema.prefault({}),
  /** Package repositories dependencies are resolved from, in order. */
  packageSources: z.array(z.string()).default([MAVEN_CENTRAL]),
});

export type CompilerConfig = z.infer<typeof CompilerOptionsSchema>;
export type LspConfig = z.infer<typeof LspOptionsSchema>;

export interface CappuConfig extends z.infer<typeof ConfigFileSchema> {
  /** Directory the config file lives in; relative paths resolve against it. */
  baseDir: string;
  /** Whether an actual cappu.json was read (false: pure defaults). */
  fromFile: boolean;
}

export const DEFAULT_CONFIG_NAME = "cappu.json";

/**
 * The starter config `cappu init` writes: every option present, commented, and
 * valid JSONC (a test parses it against the schema so the two stay in sync).
 */
export const CONFIG_TEMPLATE = `
{
  // Project configuration for the cappu compiler and language server (JSONC:
  // comments and trailing commas are fine). Relative paths resolve against
  // this file's directory.
  "compilerOptions": {
    // Compiled dependencies: directories of .class files, or .jar files.
    // Types resolve against them but are not compiled. Default if unset: ["./lib/classes"].
    // "classPath": ["./lib/classes"],

    // Additional source directories whose .java files resolve (not compiled). Default if unset: ["./src/main/java"].
    // "sourcePaths": ["./src/main/java"],

    // Output root for the emitted package tree (default if unset: "./build").
    // "outDir": "./build",

    // Do not print the path of each emitted .class file.
    "quiet": false,

    // Fail the build when a method body degrades to a placeholder because of
    // an unsupported construct (degradations always print a warning).
    "failOnDegrade": false,

    // The javac binary used by \`cappu compile --validate\` (default: "javac"
    // from $PATH).
    // "javac": "javac",
  },
  "lspOptions": {
    "inlayHints": {
      // \`count:\` hints before call arguments that are not plain variables.
      "parameterNames": true,
      // \`: String\` hints after \`var\` declarations.
      "varTypes": true,
    },
  },

  // Package repositories dependencies are resolved from, in order.
  // Default if unset: maven central.
  // "packageSources": ["https://repo.maven.apache.org/maven2"],
}
`.trimStart();

function emptyConfig(baseDir: string): CappuConfig {
  return { ...ConfigFileSchema.parse({}), baseDir, fromFile: false };
}

/**
 * Load the config from `explicitPath`, or from `cwd`/cappu.json. A missing
 * default file yields the empty config; a missing explicit path, a JSONC parse
 * error or a shape violation throws with the offending path in the message.
 */
export function loadConfig(explicitPath?: string, cwd = process.cwd()): CappuConfig {
  const path = explicitPath ? resolve(cwd, explicitPath) : join(cwd, DEFAULT_CONFIG_NAME);
  if (!existsSync(path)) {
    if (explicitPath) throw new Error(`config file not found: ${path}`);
    return emptyConfig(cwd);
  }
  const baseDir = resolve(path, "..");
  const raw = parse(readFileSync(path, "utf8"));
  const result = ConfigFileSchema.safeParse(raw ?? {});
  if (!result.success) {
    throw new Error(`invalid ${path}:\n${z.prettifyError(result.error)}`);
  }
  return { ...result.data, baseDir, fromFile: true };
}

/** Resolve a (possibly relative) config path entry against the config's directory. */
export function resolveConfigPath(config: CappuConfig, path: string): string {
  return isAbsolute(path) ? path : resolve(config.baseDir, path);
}
