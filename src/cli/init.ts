// `cappu init`: write the starter cappu.json plus the JSON schema its $schema
// entry points at. Runs before loadConfig - bootstrapping must not depend on
// (or be blocked by) an existing, possibly broken config.

import { writeFileSync } from "node:fs";
import { resolve } from "node:path";

import {
  CONFIG_TEMPLATE,
  configJsonSchema,
  DEFAULT_CONFIG_NAME,
  SCHEMA_FILE_NAME,
} from "../config.ts";

export function runInit(configPath: string | undefined): never {
  const target = resolve(configPath ?? DEFAULT_CONFIG_NAME);
  try {
    // wx: create only if absent - atomic, no exists/write race
    writeFileSync(target, CONFIG_TEMPLATE, { flag: "wx" });
  } catch (e) {
    if ((e as NodeJS.ErrnoException).code !== "EEXIST") throw e;
    process.stderr.write(`cappu: ${target} already exists, not overwriting\n`);
    process.exit(1);
  }
  // The schema the template's $schema entry points at; regenerated freely
  // (it is derived from the zod schema, not user-edited).
  const schemaTarget = resolve(target, "..", SCHEMA_FILE_NAME);
  writeFileSync(schemaTarget, configJsonSchema());
  process.stdout.write(`${target}\n${schemaTarget}\n`);
  process.exit(0);
}
