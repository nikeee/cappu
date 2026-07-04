import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

import { configJsonSchema } from "../config.ts";

// The Go build embeds the zod-generated schema so `cappu config-schema` (and
// `init --with-schema`) print byte-identical output on both builds. This
// guards the checked-in copy against drift; regenerate with
// `node --run schema:write` after changing src/config.ts.
test("the checked-in cappu.schema.json matches the zod schema", () => {
  const checkedIn = readFileSync(
    join(import.meta.dirname, "..", "..", "togo", "internal", "config", "cappu.schema.json"),
    "utf8",
  );
  assert.equal(checkedIn, configJsonSchema());
});
