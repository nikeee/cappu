// Workspace file discovery: find .java files under a directory and map between
// file paths and file:// URIs (the keys the Program and LSP use). The two
// string spaces are branded so a filesystem path cannot land where a uri is
// expected (and vice versa): pathToUri/uriToPath convert, the LSP boundary and
// synthetic-stub registrations cast.

import { existsSync, globSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

import { type Brand } from "./brand.ts";
import { type CappuConfig, resolveConfigPath } from "./config.ts";

/** A document uri (file://, or synthetic jdk:/// / classpath:///). */
export type Uri = Brand<string, "Uri">;
/** An absolute or cwd-relative filesystem path. */
export type FsPath = Brand<string, "FsPath">;

const SKIP_DIRS = new Set(["node_modules", ".git", "target", "build", "out", "bin"]);

export function uriToPath(uri: Uri): FsPath {
  return fileURLToPath(uri) as FsPath;
}

export function pathToUri(path: string): Uri {
  return pathToFileURL(path).href as Uri;
}

/**
 * A synthetic stub uri (jdk:/// hand stub or classpath:/// generated stub):
 * not openable by a client, and its types can never reference user code.
 */
export function isSyntheticUri(uri: string): boolean {
  return uri.startsWith("jdk:") || uri.startsWith("classpath:");
}

/** Recursively collect .java file paths under a directory, skipping build dirs. */
export function findJavaFiles(dir: string): FsPath[] {
  // Bun's fs.globSync (the compiled cappu binaries run under Bun) throws
  // ENOENT for a missing cwd where Node returns [] - a missing directory is
  // always "empty" here. It also ignores the exclude option entirely, hence
  // the re-filter below; under Node, exclude() still prunes the walk early
  // (it also sees plain file names, but none of those can end in .java, so
  // that is harmless).
  if (!existsSync(dir)) return [];
  let matches: string[];
  try {
    matches = globSync("**/*.java", { cwd: dir, exclude: name => SKIP_DIRS.has(name) });
  } catch {
    return []; // unreadable directory: also treated as empty
  }
  return matches
    .filter(relative => !relative.split(/[\\/]/).some(segment => SKIP_DIRS.has(segment)))
    .map(relative => join(dir, relative) as FsPath);
}

/**
 * Every regular file under `dir`, as paths relative to it. The glob's
 * withFileTypes option would filter directories in one call, but Bun (the
 * compiled cappu binaries run under it) does not support it yet, so the
 * directories `**` matches are pruned with statSync instead. A missing or
 * unreadable directory is empty.
 */
export function findFilesRelative(dir: string): string[] {
  if (!existsSync(dir)) return [];
  let matches: string[];
  try {
    matches = globSync("**/*", { cwd: dir });
  } catch {
    return [];
  }
  return matches.filter(rel => {
    try {
      return statSync(join(dir, rel)).isFile();
    } catch {
      return false;
    }
  });
}

/** Load every .java file under a root directory as [uri, text] pairs. */
export function loadJavaFiles(rootDir: string): Array<{ uri: Uri; text: string }> {
  return findJavaFiles(rootDir).map(path => ({
    uri: pathToUri(path),
    text: readFileSync(path, "utf8"),
  }));
}

/** Every .java file under the configured sourcePaths (a project build's inputs). */
export function findSourceJavaFiles(config: CappuConfig): FsPath[] {
  return config.compilerOptions.sourcePaths.flatMap(p =>
    findJavaFiles(resolveConfigPath(config, p)),
  );
}
