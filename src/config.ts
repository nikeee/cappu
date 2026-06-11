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

export const DEFAULT_CLASS_PATH = "./lib/classes";
export const DEFAULT_SOURCE_PATH = "./src/main/java";

const CompilerOptionsSchema = z.object({
  /** Directories or .jar files scanned for .class files (resolution only). */
  classPath: z.array(z.string()).default([DEFAULT_CLASS_PATH]),
  /** Directories scanned recursively for .java sources (resolution only). */
  sourcePaths: z.array(z.string()).default([DEFAULT_SOURCE_PATH]),
  /** Output root for the build artifacts. */
  outDir: z.string().default("./dist"),
  /** What `cappu compile` produces in outDir (nikeee/cappu#5). */
  output: z.enum(["classes", "jar", "fat-jar"]).default("classes"),
  quiet: z.boolean().optional(),
  failOnDegrade: z.boolean().optional(),
  /** The javac binary `--validate` compiles the reference output with. */
  javac: z.string().default("javac"),
});

const LspOptionsSchema = z.object({
  inlayHints: InlayHintsSchema.optional(),
});

export const MAVEN_CENTRAL = "https://repo.maven.apache.org/maven2";
export const GOOGLE_MAVEN = "https://maven.google.com";
export const GRADLE_PLUGIN_PORTAL = "https://plugins.gradle.org/m2";
/** The repositories Maven and Gradle resolve from out of the box. */
export const DEFAULT_PACKAGE_SOURCES = [MAVEN_CENTRAL, GOOGLE_MAVEN, GRADLE_PLUGIN_PORTAL];

/** "group:artifact" -> version, per configuration (gradle-style). */
const DependencyMapSchema = z.record(z.string(), z.string());

const DependenciesSchema = z.object({
  /** Dependencies that are part of this project's public API. */
  api: DependencyMapSchema.default({}),
  /** Dependencies internal to the implementation. */
  implementation: DependencyMapSchema.default({}),
});

const ConfigFileSchema = z.object({
  // prefault (not default): the empty object is parsed through the section
  // schema, so the inner defaults (classPath: [], ...) apply.
  compilerOptions: CompilerOptionsSchema.prefault({}),
  lspOptions: LspOptionsSchema.prefault({}),
  /** Package repositories dependencies are resolved from, in order. */
  packageSources: z.array(z.string()).default(DEFAULT_PACKAGE_SOURCES),
  /** What `cappu install` resolves and downloads, keyed by configuration. */
  dependencies: DependenciesSchema.prefault({}),
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
export const SCHEMA_FILE_NAME = "cappu.schema.json";

/**
 * JSON Schema for cappu.json, generated from the zod schema so the two cannot
 * drift. `cappu init` writes it next to the config; the template's $schema
 * entry points editors at it (loadConfig ignores the unknown key).
 */
export function configJsonSchema(): string {
  return `${JSON.stringify(z.toJSONSchema(ConfigFileSchema, { io: "input" }), null, 2)}\n`;
}

/**
 * The starter config `cappu init` writes: every option present, commented, and
 * valid JSONC (a test parses it against the schema so the two stay in sync).
 */
export const CONFIG_TEMPLATE = `
{
  // Project configuration for the cappu compiler and language server (JSONC:
  // comments and trailing commas are fine). Relative paths resolve against
  // this file's directory.
  "$schema": "./cappu.schema.json",

  "compilerOptions": {
    // Compiled dependencies: directories of .class files, or .jar files.
    // Types resolve against them but are not compiled. Default if unset: ["./lib/classes"].
    // "classPath": ["./lib/classes"],

    // Additional source directories whose .java files resolve (not compiled). Default if unset: ["./src/main/java"].
    // "sourcePaths": ["./src/main/java"],

    // Output root for the build artifacts (default if unset: "./dist").
    // "outDir": "./dist",

    // What \`cappu compile\` produces in outDir: "classes" (a package tree
    // usable directly as \`java -cp <outDir>\`), "jar", or "fat-jar" (the jar
    // plus the contents of every dependency jar on the classPath).
    // "output": "classes",

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

  // Package repositories dependencies are resolved from, in order. Default if
  // unset: Maven Central, Google Maven and the Gradle Plugin Portal.
  // "packageSources": [
  //   "https://repo.maven.apache.org/maven2",
  //   "https://maven.google.com",
  //   "https://plugins.gradle.org/m2",
  // ],

  // Dependencies \`cappu install\` resolves (transitively) and downloads into
  // the classPath, keyed by configuration as in gradle.
  "dependencies": {
    "api": {},
    "implementation": {
      // "com.google.code.gson:gson": "2.13.2",
    },
  },
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
