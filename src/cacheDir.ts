// The per-user cache root for cappu's downloaded artifacts (packages, JDKs,
// resolved POM metadata). A CACHE, so it follows XDG_CACHE_HOME
// (~/.cache/cappu/<subdir>); each caller's env var (CAPPU_PACKAGE_STORE, ...)
// overrides for tests and CI.

import { homedir } from "node:os";
import { join } from "node:path";

export function cacheDir(subdir: string, envOverride?: string): string {
  if (envOverride) return envOverride;
  return join(process.env.XDG_CACHE_HOME ?? join(homedir(), ".cache"), "cappu", subdir);
}
