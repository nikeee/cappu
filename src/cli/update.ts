// `cappu update`: bump every declared dependency to the newest stable version
// that keeps its configuration's transitive graph conflict-free, rewrite
// cappu.json (preserving comments), then refresh the lock via install.

import { readFileSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";

import { type CappuConfig, DEFAULT_CONFIG_NAME, loadConfig } from "../config.ts";
import { type DependencyBump, planUpdates } from "../install.ts";
import { type PackageSource } from "../packages/index.ts";
import { emitAnnotation } from "./annotations.ts";
import { runInstall } from "./install.ts";
import { hasJsoncKey, setJsoncValue } from "./jsoncEdit.ts";
import { painter } from "./style.ts";

/** Overwrite the bumped versions in the JSONC config text, comments intact. */
export function applyBumpsToJsonc(text: string, bumps: readonly DependencyBump[]): string {
  for (const bump of bumps) {
    const path = ["dependencies", bump.configuration, bump.key];
    // Only overwrite an entry that is still declared (a vanished section is
    // skipped, never recreated).
    if (hasJsoncKey(text, path)) text = setJsoncValue(text, path, bump.to);
  }
  return text;
}

export async function runUpdate(
  configPathArg: string | undefined,
  config: CappuConfig,
  // Injectable for tests; defaults to the configured remote repositories.
  sources?: readonly PackageSource[],
): Promise<never> {
  if (!config.fromFile) {
    process.stderr.write("cappu: no cappu.json found - run `cappu init` first\n");
    emitAnnotation("error", "no cappu.json found - run `cappu init` first");
    process.exit(1);
  }

  const err = painter(process.stderr);
  process.stderr.write(err("cyan", "checking for updates...\n"));

  let bumps: DependencyBump[];
  try {
    bumps = await planUpdates(config, sources);
  } catch (e) {
    process.stderr.write(`cappu: update failed: ${(e as Error).message}\n`);
    emitAnnotation("error", `update failed: ${(e as Error).message}`);
    process.exit(2);
  }

  if (bumps.length === 0) {
    process.stdout.write("all dependencies are up to date\n");
    process.exit(0);
  }

  const configPath = configPathArg
    ? resolve(configPathArg)
    : join(config.baseDir, DEFAULT_CONFIG_NAME);
  writeFileSync(configPath, applyBumpsToJsonc(readFileSync(configPath, "utf8"), bumps));
  for (const b of bumps) {
    process.stderr.write(`updated ${b.key}: ${b.from} -> ${b.to}\n`);
  }

  // Re-resolve and rewrite the lock against the bumped versions.
  return runInstall(loadConfig(configPath), { updateLock: true });
}
