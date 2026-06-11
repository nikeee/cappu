// `cappu init`: write the starter cappu.json and create the default project
// directories (lib/classes for dependencies, src/main/java for sources);
// --with-schema also writes the JSON schema the $schema entry points at. Runs
// before loadConfig - bootstrapping must not depend on (or be blocked by) an
// existing, possibly broken config.

import { mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

import {
  CONFIG_TEMPLATE,
  configJsonSchema,
  DEFAULT_CLASS_PATH,
  DEFAULT_CONFIG_NAME,
  DEFAULT_SOURCE_PATH,
  SCHEMA_FILE_NAME,
} from "../config.ts";

export function runInit(configPath: string | undefined, withSchema: boolean): never {
  const target = resolve(configPath ?? DEFAULT_CONFIG_NAME);
  try {
    // wx: create only if absent - atomic, no exists/write race
    writeFileSync(target, CONFIG_TEMPLATE, { flag: "wx" });
  } catch (e) {
    if ((e as NodeJS.ErrnoException).code !== "EEXIST") throw e;
    process.stderr.write(`cappu: ${target} already exists, not overwriting\n`);
    process.exit(1);
  }
  // The default classPath and sourcePaths directories (nikeee/cappu#3), so a
  // fresh project compiles without "configured path not found" warnings.
  for (const dir of [DEFAULT_CLASS_PATH, DEFAULT_SOURCE_PATH]) {
    mkdirSync(resolve(target, "..", dir), { recursive: true });
  }
  process.stdout.write(`${target}\n`);
  if (withSchema) {
    // The schema the template's $schema entry points at; regenerated freely
    // (it is derived from the zod schema, not user-edited).
    const schemaTarget = resolve(target, "..", SCHEMA_FILE_NAME);
    writeFileSync(schemaTarget, configJsonSchema());
    process.stdout.write(`${schemaTarget}\n`);
  }
  process.exit(0);
}
