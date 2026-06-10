// Workspace file discovery: find .java files under a directory and map between
// file paths and file:// URIs (the keys the Program and LSP use).

import { globSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const SKIP_DIRS = new Set(["node_modules", ".git", "target", "build", "out", "bin"]);

export function uriToPath(uri: string): string {
  return fileURLToPath(uri);
}

export function pathToUri(path: string): string {
  return pathToFileURL(path).href;
}

/**
 * A synthetic stub uri (jdk:/// hand stub or classpath:/// generated stub):
 * not openable by a client, and its types can never reference user code.
 */
export function isSyntheticUri(uri: string): boolean {
  return uri.startsWith("jdk:") || uri.startsWith("classpath:");
}

/** Recursively collect .java file paths under a directory, skipping build dirs. */
export function findJavaFiles(dir: string): string[] {
  // exclude() prunes matching directories before descent (it also sees plain
  // file names, but none of those can end in .java, so that is harmless).
  return globSync("**/*.java", { cwd: dir, exclude: name => SKIP_DIRS.has(name) }) //
    .map(relative => join(dir, relative));
}

/** Load every .java file under a root directory as [uri, text] pairs. */
export function loadJavaFiles(rootDir: string): Array<{ uri: string; text: string }> {
  return findJavaFiles(rootDir).map(path => ({
    uri: pathToUri(path),
    text: readFileSync(path, "utf8"),
  }));
}
