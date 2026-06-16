// cappu.json: project configuration for the compiler and the language server.
// JSONC (comments + trailing commas) via comment-json; shape validation and
// the exported config types both come from one zod schema. Looked up at
// $PWD/cappu.json unless an explicit path is given (--config).

import { existsSync, readFileSync } from "node:fs";
import { basename, isAbsolute, join, resolve } from "node:path";

import { parse } from "comment-json";
import { z } from "zod";

import { isValidSpdxExpression } from "./spdx.ts";

/** Maven groupId/artifactId charset. */
const MAVEN_ID = /^[A-Za-z0-9_.-]+$/;
// The canonical semver.org regex: MAJOR.MINOR.PATCH with optional -prerelease
// and +build. "1.0.0" / "1.0.0-SNAPSHOT" pass; "1.0" / "RELEASE" do not.
const SEMVER =
  /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$/;

const InlayHintsSchema = z.object({
  /** Hints like `count:` before call arguments that are not plain variables. */
  parameterNames: z.boolean().default(true),
  /** Hints like `: String` after a `var` declaration's name. */
  varTypes: z.boolean().default(true),
});

// Downloaded dependency jars live under .cappu/ - cappu-managed, gitignored
// state alongside the provisioned JDKs and generated sources, not checked in.
export const DEFAULT_CLASS_PATH = "./.cappu/lib/classes";
export const DEFAULT_SOURCE_PATH = "./src/main/java";
export const DEFAULT_RESOURCE_PATH = "./src/main/resources";
// Created by `cappu init` and used by `cappu test` (nikeee/cappu#16): test
// sources/resources and the directory test-only dependencies install to.
export const DEFAULT_TEST_SOURCE_PATH = "./src/test/java";
export const DEFAULT_TEST_RESOURCE_PATH = "./src/test/resources";
export const DEFAULT_TEST_CLASS_PATH = "./.cappu/lib/test-classes";
/** Where annotation-processor jars install to - never the compile classpath. */
export const DEFAULT_PROCESSOR_PATH = "./.cappu/lib/processors";

const CompilerOptionsSchema = z.object({
  /** Directories or .jar files scanned for .class files (resolution only). */
  classPath: z.array(z.string()).default([DEFAULT_CLASS_PATH]),
  /** Directories scanned recursively for .java sources (resolution only). */
  sourcePaths: z.array(z.string()).default([DEFAULT_SOURCE_PATH]),
  /** Directories whose files are copied verbatim into the build output. */
  resourcePaths: z.array(z.string()).default([DEFAULT_RESOURCE_PATH]),
  /** Output root for the build artifacts. */
  outDir: z.string().default("./dist"),
  /** What `cappu compile` produces in outDir (nikeee/cappu#5). */
  output: z.enum(["classes", "jar", "fat-jar"]).default("classes"),
  quiet: z.boolean().optional(),
  failOnDegrade: z.boolean().optional(),
  /** The javac binary compiles use (a provisioned "jdk" entry wins). */
  javac: z.string().default("javac"),
  /** Java release to target (javac --release); e.g. 21. Unset: javac's own. */
  release: z.number().int().min(8).optional(),
  /** Main-Class for jar outputs; default: the only main(String[]) found. */
  mainClass: z.string().optional(),
  /** Compile with cappu's own (experimental) compiler instead of javac. */
  experimentalCompiler: z.boolean().optional(),
});

const LspOptionsSchema = z.object({
  inlayHints: InlayHintsSchema.optional(),
});

export const MAVEN_CENTRAL = "https://repo.maven.apache.org/maven2";
/** Central's index service; a maven2 repository itself has no search endpoint. */
export const MAVEN_CENTRAL_SEARCH = "https://search.maven.org/solrsearch/select";
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
  /** JSR-269 annotation processors (resolved separately into .cappu/lib/processors). */
  annotationProcessor: DependencyMapSchema.default({}),
  /** Test-only dependencies (resolved separately into .cappu/lib/test-classes). */
  testImplementation: DependencyMapSchema.default({}),
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
  /** JDK to provision into .cappu/jdks on install, e.g. "temurin-21". */
  jdk: z.string().optional(),
  /**
   * This project's own license, as an SPDX expression (npm-style): a single id
   * like "MIT" or a compound like "(MIT OR Apache-2.0)". SPDX only - a
   * free-text license name is rejected.
   */
  license: z
    .string()
    .refine(isValidSpdxExpression, {
      message:
        'not a valid SPDX license expression (e.g. "MIT", "Apache-2.0", "(MIT OR Apache-2.0)")',
    })
    .optional(),
  // --- publishing (`cappu publish`) ---------------------------------------
  // This project's Maven coordinates. Optional in general, but all three are
  // required to generate a POM / publish. groupId+artifactId use the Maven id
  // charset; version must be semver.
  groupId: z.string().regex(MAVEN_ID, "must be a Maven id (letters, digits, . _ -)").optional(),
  artifactId: z.string().regex(MAVEN_ID, "must be a Maven id (letters, digits, . _ -)").optional(),
  version: z
    .string()
    .regex(SEMVER, "must be a semver version, e.g. 1.0.0 or 2.1.0-SNAPSHOT")
    .optional(),
  /** Default registry `cappu publish` uploads to (overridable by --repo). */
  publishRepository: z.string().url().optional(),
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
  "$schema": "./cappu.schema.json",

  "compilerOptions": {
    // Compiled dependencies: directories of .class files, or .jar files.
    // Types resolve against them but are not compiled. Default if unset:
    // ["./.cappu/lib/classes"] (where cappu install downloads them).
    // "classPath": ["./.cappu/lib/classes"],

    // Sources to be compiled. Default if unset: ["./src/main/java"].
    // "sourcePaths": ["./src/main/java"],

    // Resource directories: their files are copied verbatim into the build
    // output (the classes tree / the jar). Default if unset: ["./src/main/resources"].
    // "resourcePaths": ["./src/main/resources"],

    // Output root for the build artifacts (default if unset: "./dist").
    // "outDir": "./dist",

    // What \`cappu compile\` produces in outDir:
    // - "classes": a package tree usable directly as \`java -cp <outDir>\`
    // - "jar": same as "classes", but as a jar
    // - "fat-jar": the jar plus the contents of every dependency jar on the classPath
    // "output": "classes",

    // Do not print the path of each emitted .class file.
    "quiet": false,

    // Fail the build when a method body degrades to a placeholder because of
    // an unsupported construct (degradations always print a warning).
    "failOnDegrade": false,

    // The javac binary compiles run with (default: "javac" from $PATH; a
    // provisioned "jdk" entry wins).
    // "javac": "javac",

    // Java release to compile FOR (javac --release): language level and class
    // file version, e.g. 21 even under a newer JDK. Default if unset: the
    // javac binary's own release. Same name as Maven/Gradle use.
    // "release": 21,

    // Main-Class of jar outputs (java -jar). Default if unset: the single
    // class declaring public static void main(String[]), if exactly one.
    // "mainClass": "com.example.Main",

    // Compile with cappu's own (experimental) compiler instead of javac
    // (same as \`cappu compile --experimental-compiler\`).
    // "experimentalCompiler": false,
  },

  "lspOptions": {
    // "inlayHints": {
    //   "parameterNames": true,
    //   "varTypes": true,
    // },
  },

  // Package repositories dependencies are resolved from, in order. Default if unset:
  // "packageSources": [
  //   "https://repo.maven.apache.org/maven2",
  //   "https://maven.google.com",
  //   "https://plugins.gradle.org/m2",
  // ],

  // JDK provisioned by \`cappu install\` into ./.cappu/jdks/<spec>.
  // Supported distributions: temurin, corretto.
  // "jdk": "temurin-21",

  // This project's own license, as an SPDX expression (SPDX only):
  // a single id like "MIT", or a compound like "(MIT OR Apache-2.0)".
  // "license": "MIT",

  // This project's Maven coordinates - required to generate a POM and
  // \`cappu publish\` to a registry. version must be semver (e.g. 1.0.0).
  // "groupId": "com.example",
  // "artifactId": "my-library",
  // "version": "1.0.0",

  // Registry \`cappu publish\` uploads to (overridable with --repo).
  // "publishRepository": "https://maven.example.com/releases",

  "dependencies": {
    "api": {},
    "implementation": {
      // "com.google.code.gson:gson": "2.13.2",
    },
    // JSR-269 annotation processors; \`cappu compile\` runs them via javac.
    "annotationProcessor": {
      // "org.mapstruct:mapstruct-processor": "1.6.3",
    },
    // Test-only dependencies for \`cappu test\` (src/test/java).
    "testImplementation": {
      // "org.junit.jupiter:junit-jupiter": "5.12.2",
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

/**
 * The base name (no extension) of the build artifacts: `<artifactId>-<version>`
 * when both coordinates are set (so the jar is the publishable name a Maven
 * registry expects), otherwise the project directory name (cappu's original
 * behaviour, kept for non-publishing projects).
 */
export function artifactBaseName(config: CappuConfig): string {
  return config.artifactId && config.version
    ? `${config.artifactId}-${config.version}`
    : basename(resolve(config.baseDir));
}
