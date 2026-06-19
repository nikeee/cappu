// `cappu config-schema`: print the JSON Schema for cappu.json to stdout. Useful
// for tooling (and agents) that want to validate or understand the config
// without a project present - it is derived from the zod schema, same as the
// cappu.schema.json that `cappu init --with-schema` writes.

import { configJsonSchema } from "../config.ts";

export function runConfigSchema(): never {
  process.stdout.write(configJsonSchema());
  process.exit(0);
}
