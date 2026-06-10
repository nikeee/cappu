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
  classPath: z.array(z.string()).default([]),
  /** Directories scanned recursively for .java sources (resolution only). */
  sourcePaths: z.array(z.string()).default([]),
  outDir: z.string().optional(),
  quiet: z.boolean().optional(),
  failOnDegrade: z.boolean().optional(),
});

const LspOptionsSchema = z.object({
  inlayHints: InlayHintsSchema.optional(),
});

const ConfigFileSchema = z.object({
  // prefault (not default): the empty object is parsed through the section
  // schema, so the inner defaults (classPath: [], ...) apply.
  compilerOptions: CompilerOptionsSchema.prefault({}),
  lspOptions: LspOptionsSchema.prefault({}),
});

export type CompilerConfig = z.infer<typeof CompilerOptionsSchema>;
export type LspConfig = z.infer<typeof LspOptionsSchema>;

export interface CappuConfig extends z.infer<typeof ConfigFileSchema> {
  /** Directory the config file lives in; relative paths resolve against it. */
  baseDir: string;
}

export const DEFAULT_CONFIG_NAME = "cappu.json";

/**
 * The starter config `cappu init` writes: every option present, commented, and
 * valid JSONC (a test parses it against the schema so the two stay in sync).
 */
export const CONFIG_TEMPLATE = `{
  // Project configuration for the cappu compiler and language server (JSONC:
  // comments and trailing commas are fine). Relative paths resolve against
  // this file's directory.
  "compilerOptions": {
    // Compiled dependencies: directories of .class files, or .jar files.
    // Types resolve against them but are not compiled.
    "classPath": [],
    // Additional source directories whose .java files resolve (not compiled).
    "sourcePaths": [],
    // Output root for the emitted package tree (default: current directory).
    // "outDir": "build",
    // Do not print the path of each emitted .class file.
    "quiet": false,
    // Fail the build when a method body degrades to a placeholder because of
    // an unsupported construct (degradations always print a warning).
    "failOnDegrade": false,
  },
  "lspOptions": {
    "inlayHints": {
      // \`count:\` hints before call arguments that are not plain variables.
      "parameterNames": true,
      // \`: String\` hints after \`var\` declarations.
      "varTypes": true,
    },
  },
}
`;

function emptyConfig(baseDir: string): CappuConfig {
  return { ...ConfigFileSchema.parse({}), baseDir };
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
  return { ...result.data, baseDir };
}

/** Resolve a (possibly relative) config path entry against the config's directory. */
export function resolveConfigPath(config: CappuConfig, path: string): string {
  return isAbsolute(path) ? path : resolve(config.baseDir, path);
}
