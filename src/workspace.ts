// Workspace file discovery: find .java files under a directory and map between
// file paths and file:// URIs (the keys the Program and LSP use). The two
// string spaces are branded so a filesystem path cannot land where a uri is
// expected (and vice versa): pathToUri/uriToPath convert, the LSP boundary and
// synthetic-stub registrations cast.

import { globSync, readFileSync, statSync } from "node:fs";
import { basename, join, matchesGlob, relative } from "node:path";
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
  // globSync returns [] for a missing cwd and `exclude` prunes whole excluded
  // subtrees; the try/catch only guards an unreadable directory (EACCES).
  let matches: string[];
  try {
    matches = globSync("**/*.java", { cwd: dir, exclude: name => SKIP_DIRS.has(name) });
  } catch {
    return [];
  }
  return matches.map(rel => join(dir, rel) as FsPath);
}

/**
 * Every regular file under `dir`, as paths relative to it. A missing or
 * unreadable directory is empty.
 */
export function findFilesRelative(dir: string): string[] {
  try {
    return globSync("**/*", { cwd: dir, withFileTypes: true })
      .filter(entry => entry.isFile())
      .map(entry => relative(dir, join(entry.parentPath, entry.name)));
  } catch {
    return [];
  }
}

/** Load every .java file under a root directory as [uri, text] pairs. */
export function loadJavaFiles(rootDir: string): Array<{ uri: Uri; text: string }> {
  return findJavaFiles(rootDir).map(path => ({
    uri: pathToUri(path),
    text: readFileSync(path, "utf8"),
  }));
}

/**
 * Map every .jar/.class file reachable from the config's classPath entries to
 * its mtime - exactly the set loadClassPath reads. Map inequality means the
 * classpath changed (add, remove, replace); directory mtimes are deliberately
 * not used (unreliable for nested changes).
 */
export function classpathFingerprint(config: CappuConfig): Map<string, number> {
  const fp = new Map<string, number>();
  const stat = (path: string): void => {
    try {
      fp.set(path, statSync(path).mtimeMs);
    } catch {
      // vanished between listing and stat: contributes nothing
    }
  };
  for (const p of config.compilerOptions.classPath) {
    const entry = resolveConfigPath(config, p);
    if (entry.endsWith(".jar")) {
      stat(entry);
      continue;
    }
    let matches: string[];
    try {
      matches = globSync("**/*.{jar,class}", { cwd: entry });
    } catch {
      continue;
    }
    for (const rel of matches) stat(join(entry, rel));
  }
  return fp;
}

/**
 * The LSP client watch set: source .java files, the loaded cappu.json (by
 * name), and every configured classPath entry (a dir as a jar/class glob, a
 * .jar as itself). Computed once from the startup config.
 * ponytail: a config edit that changes classPath keeps the old watch set until
 * restart; re-register after a config reload if that ever matters. Absolute
 * globs only fire inside the workspace folders, so classpath entries outside
 * the root are not watched (the MCP server's per-call polling has no such
 * limit).
 */
export function configWatchGlobs(config: CappuConfig | undefined): string[] {
  const globs = ["**/*.java"];
  if (config?.configPath === undefined) return globs;
  globs.push("**/" + basename(config.configPath));
  for (const p of config.compilerOptions.classPath) {
    const entry = resolveConfigPath(config, p).replaceAll("\\", "/");
    globs.push(entry.endsWith(".jar") ? entry : entry + "/**/*.{jar,class}");
  }
  return globs;
}

/** Every .java file under the configured sourcePaths (a project build's inputs). */
export function findSourceJavaFiles(config: CappuConfig): FsPath[] {
  return config.compilerOptions.sourcePaths.flatMap(p =>
    findJavaFiles(resolveConfigPath(config, p)),
  );
}

/**
 * The .java files `cappu format` operates on: every source file under the
 * configured sourcePaths, minus any matching a `formatterOptions.ignore` glob.
 * Ignore globs are matched against the path relative to the config directory.
 */
export function findFormattableFiles(config: CappuConfig): FsPath[] {
  const ignore = config.formatterOptions.ignore;
  const files = findSourceJavaFiles(config);
  if (ignore.length === 0) return files;
  return files.filter(p => {
    const rel = relative(config.baseDir, p);
    return !ignore.some(pattern => matchesGlob(rel, pattern));
  });
}
