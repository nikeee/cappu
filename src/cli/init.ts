// `cappu init`: write the starter cappu.json and create the default project
// directories (.cappu/lib/classes for dependencies, src/main/java for sources);
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
  DEFAULT_RESOURCE_PATH,
  DEFAULT_SOURCE_PATH,
  DEFAULT_TEST_CLASS_PATH,
  DEFAULT_TEST_RESOURCE_PATH,
  DEFAULT_TEST_SOURCE_PATH,
  SCHEMA_FILE_NAME,
} from "../config.ts";

// What `cappu init` puts into a fresh .gitignore: everything cappu itself
// (re)generates - installed dependencies and build output (nikeee/cappu#12).
const GITIGNORE_TEMPLATE = `# installed dependencies, provisioned JDKs, generated sources, local state
/.cappu/

# build output of \`cappu compile\`
/dist/
`;

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
  // The standard project layout (nikeee/cappu#3, #12): dependency and source
  // directories plus their resource/test counterparts, so a fresh project
  // compiles warning-free and the layout is visible from the start.
  for (const dir of [
    DEFAULT_CLASS_PATH,
    DEFAULT_TEST_CLASS_PATH,
    DEFAULT_SOURCE_PATH,
    DEFAULT_RESOURCE_PATH,
    DEFAULT_TEST_SOURCE_PATH,
    DEFAULT_TEST_RESOURCE_PATH,
  ]) {
    mkdirSync(resolve(target, "..", dir), { recursive: true });
  }
  // A .gitignore covering what cappu generates; an existing one is left alone
  // but flagged, so the user knows cappu's ignores (/.cappu/, /dist/) were
  // not added and downloaded deps / build output could otherwise get committed.
  try {
    writeFileSync(resolve(target, "..", ".gitignore"), GITIGNORE_TEMPLATE, { flag: "wx" });
  } catch (e) {
    if ((e as NodeJS.ErrnoException).code !== "EEXIST") throw e;
    process.stderr.write(
      ".gitignore already exists, left unchanged - add /.cappu/ and /dist/ if missing\n",
    );
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
