// cappu.json: project configuration for the compiler and the language
// server. JSONC (comments + trailing commas) via comment-json. Looked up at
// $PWD/cappu.json unless an explicit path is given (--config).

import { existsSync, readFileSync } from "node:fs";
import { isAbsolute, join, resolve } from "node:path";

import { parse } from "comment-json";

import type { InlayHintsSettings } from "./inlayHints.ts";

export interface CompilerConfig {
  /** Directories scanned recursively for .class files (resolution only). */
  classPath: string[];
  /** Directories scanned recursively for .java sources (resolution only). */
  sourcePaths: string[];
  outDir?: string;
  quiet?: boolean;
  failOnDegrade?: boolean;
}

export interface LspConfig {
  inlayHints?: Partial<InlayHintsSettings>;
}

export interface CappuConfig {
  compilerOptions: CompilerConfig;
  lspOptions: LspConfig;
  /** Directory the config file lives in; relative paths resolve against it. */
  baseDir: string;
}

export const DEFAULT_CONFIG_NAME = "cappu.json";

function emptyConfig(baseDir: string): CappuConfig {
  return { compilerOptions: { classPath: [], sourcePaths: [] }, lspOptions: {}, baseDir };
}

function stringArray(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((v): v is string => typeof v === "string") : [];
}

/**
 * Load the config from `explicitPath`, or from `cwd`/cappu.json. A
 * missing default file yields the empty config; a missing explicit path or a
 * parse error throws (the caller asked for that file specifically).
 */
export function loadConfig(explicitPath?: string, cwd = process.cwd()): CappuConfig {
  const path = explicitPath ? resolve(cwd, explicitPath) : join(cwd, DEFAULT_CONFIG_NAME);
  if (!existsSync(path)) {
    if (explicitPath) throw new Error(`config file not found: ${path}`);
    return emptyConfig(cwd);
  }
  const baseDir = resolve(path, "..");
  const raw = parse(readFileSync(path, "utf8")) as {
    compilerOptions?: Record<string, unknown>;
    lspOptions?: { inlayHints?: Record<string, unknown> };
  } | null;
  if (raw === null || typeof raw !== "object") return emptyConfig(baseDir);

  const co = raw.compilerOptions ?? {};
  const config = emptyConfig(baseDir);
  config.compilerOptions.classPath = stringArray(co["classPath"]);
  config.compilerOptions.sourcePaths = stringArray(co["sourcePaths"]);
  if (typeof co["outDir"] === "string") config.compilerOptions.outDir = co["outDir"];
  if (typeof co["quiet"] === "boolean") config.compilerOptions.quiet = co["quiet"];
  if (typeof co["failOnDegrade"] === "boolean") {
    config.compilerOptions.failOnDegrade = co["failOnDegrade"];
  }
  const hints = raw.lspOptions?.inlayHints;
  if (hints && typeof hints === "object") {
    config.lspOptions.inlayHints = {};
    if (typeof hints["parameterNames"] === "boolean") {
      config.lspOptions.inlayHints.parameterNames = hints["parameterNames"];
    }
    if (typeof hints["varTypes"] === "boolean") {
      config.lspOptions.inlayHints.varTypes = hints["varTypes"];
    }
  }
  return config;
}

/** Resolve a (possibly relative) config path entry against the config's directory. */
export function resolveConfigPath(config: CappuConfig, path: string): string {
  return isAbsolute(path) ? path : resolve(config.baseDir, path);
}
