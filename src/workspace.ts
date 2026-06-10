// Workspace file discovery: find .java files under a directory and map between
// file paths and file:// URIs (the keys the Program and LSP use). The two
// string spaces are branded so a filesystem path cannot land where a uri is
// expected (and vice versa): pathToUri/uriToPath convert, the LSP boundary and
// synthetic-stub registrations cast.

import { globSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

import { type Brand } from "./brand.ts";

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
  // exclude() prunes matching directories before descent (it also sees plain
  // file names, but none of those can end in .java, so that is harmless).
  return globSync("**/*.java", { cwd: dir, exclude: name => SKIP_DIRS.has(name) }) //
    .map(relative => join(dir, relative) as FsPath);
}

/** Load every .java file under a root directory as [uri, text] pairs. */
export function loadJavaFiles(rootDir: string): Array<{ uri: Uri; text: string }> {
  return findJavaFiles(rootDir).map(path => ({
    uri: pathToUri(path),
    text: readFileSync(path, "utf8"),
  }));
}
