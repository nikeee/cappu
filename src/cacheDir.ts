// The per-user cache root for cappu's downloaded artifacts (packages, JDKs,
// resolved POM metadata). A CACHE, so it follows XDG_CACHE_HOME
// (~/.cache/cappu/<subdir>); each caller's env var (CAPPU_PACKAGE_STORE, ...)
// overrides for tests and CI.

import { existsSync, rmSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

/** The cappu cache root: XDG_CACHE_HOME/cappu (or ~/.cache/cappu). */
export function cacheRoot(env: NodeJS.ProcessEnv = process.env): string {
  return join(env.XDG_CACHE_HOME ?? join(homedir(), ".cache"), "cappu");
}

export function cacheDir(subdir: string, envOverride?: string): string {
  if (envOverride) return envOverride;
  return join(cacheRoot(), subdir);
}

/**
 * Remove the global download cache. Returns the directories actually removed.
 * The per-domain env overrides (CAPPU_PACKAGE_STORE, CAPPU_JDK_STORE), when
 * set, are cleaned too since they may point outside the cache root.
 */
export function cleanCache(env: NodeJS.ProcessEnv = process.env): string[] {
  const targets = new Set([cacheRoot(env), env.CAPPU_PACKAGE_STORE, env.CAPPU_JDK_STORE]);
  const removed: string[] = [];
  for (const dir of targets) {
    if (dir && existsSync(dir)) {
      rmSync(dir, { recursive: true, force: true });
      removed.push(dir);
    }
  }
  return removed;
}
