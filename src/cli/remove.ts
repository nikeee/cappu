// `cappu remove <configuration> <group:artifact>`: drop the entry from the
// cappu.json dependencies section (preserving comments - the file is JSONC) and
// then re-resolve and rewrite the lock + `.cappu/lib`, exactly like `cappu add`
// in reverse. The version segment of a `group:artifact[:version]` coordinate is
// ignored - a dependency is removed by its `group:artifact` key.

import { readFileSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";

import {
  type CappuConfig,
  DEFAULT_CONFIG_NAME,
  DEPENDENCY_CONFIGURATIONS,
  loadConfig,
} from "../config.ts";
import { emitAnnotation } from "./annotations.ts";
import { parseAddCoordinate, resolveConfiguration } from "./add.ts";
import { runInstall } from "./install.ts";
import { removeJsoncKey } from "./jsoncEdit.ts";

const CONFIGURATIONS = DEPENDENCY_CONFIGURATIONS;
type Configuration = (typeof CONFIGURATIONS)[number];

/**
 * Delete the dependency from the JSONC config text (comments intact). Returns
 * the new text and whether the key was actually present.
 */
export function removeDependencyFromJsonc(
  text: string,
  configuration: Configuration,
  key: string,
): { text: string; removed: boolean } {
  return removeJsoncKey(text, ["dependencies", configuration, key]);
}

export async function runRemove(
  configurationArg: string | undefined,
  specs: readonly string[],
  configPathArg: string | undefined,
  config: CappuConfig,
): Promise<never> {
  const configuration = resolveConfiguration(configurationArg);
  // Accept `group:artifact` or `group:artifact:version` (the version is ignored).
  const keys = specs.map(s => parseAddCoordinate(s)?.key);
  const invalid = specs.filter((_, i) => keys[i] === undefined);
  if (!configuration || keys.length === 0 || invalid.length > 0) {
    for (const spec of invalid) {
      process.stderr.write(`cappu: not a coordinate: '${spec}'\n`);
      emitAnnotation("error", `not a coordinate: '${spec}'`);
    }
    process.stderr.write(
      `usage: cappu remove <${CONFIGURATIONS.join("|")}> <group:artifact> [more...]\n` +
        "       aliases: a=api, i=implementation, ap=annotationProcessor, ti=testImplementation\n",
    );
    process.exit(2);
  }
  if (!config.fromFile) {
    process.stderr.write("cappu: no cappu.json found - run `cappu init` first\n");
    emitAnnotation("error", "no cappu.json found - run `cappu init` first");
    process.exit(1);
  }

  const configPath = configPathArg
    ? resolve(configPathArg)
    : join(config.baseDir, DEFAULT_CONFIG_NAME);

  let text = readFileSync(configPath, "utf8");
  let any = false;
  for (const key of keys as string[]) {
    const result = removeDependencyFromJsonc(text, configuration, key);
    if (result.removed) {
      text = result.text;
      any = true;
      process.stderr.write(`removed ${configuration} ${key}\n`);
    } else {
      process.stderr.write(`warning: ${key} is not a ${configuration} dependency\n`);
      emitAnnotation("warning", `${key} is not a ${configuration} dependency`);
    }
  }

  if (!any) process.exit(1);
  writeFileSync(configPath, text);

  // Re-resolve and rewrite the lock against the reduced dependency set.
  return runInstall(loadConfig(configPath), { updateLock: true });
}
