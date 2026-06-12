// Classpath expansion for javac invocations. A leaf module shared by the
// compiler driver, the annotation-processing runner and the test runner.

import { existsSync, globSync } from "node:fs";
import { join } from "node:path";

import { type CappuConfig, resolveConfigPath } from "../config.ts";

/**
 * javac's -cp treats a DIRECTORY entry as a .class tree only; the dependency
 * jars cappu install puts inside it must be listed individually.
 */
export function expandedClassPath(config: CappuConfig): string[] {
  return expandedJarDirs(config.compilerOptions.classPath.map(p => resolveConfigPath(config, p)));
}

/** Each existing dir plus the jars directly inside it; jars pass through. */
export function expandedJarDirs(roots: readonly string[]): string[] {
  const out: string[] = [];
  for (const root of roots) {
    if (!existsSync(root)) continue;
    out.push(root);
    if (root.endsWith(".jar")) continue;
    try {
      out.push(
        ...globSync("*.jar", { cwd: root })
          .toSorted()
          .map(jar => join(root, jar)),
      );
    } catch {
      // unreadable directories contribute only themselves
    }
  }
  return out;
}
