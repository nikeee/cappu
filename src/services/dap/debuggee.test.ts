import assert from "node:assert/strict";
import { test } from "node:test";

import type { CappuConfig } from "../../config.ts";
import { debuggeeVmArgs } from "./debuggee.ts";

// debuggeeVmArgs only reads config.dapOptions.enableAssertions.
const cfg = (enableAssertions: boolean) =>
  ({ dapOptions: { enableAssertions } }) as unknown as CappuConfig;

test("debuggeeVmArgs prepends -ea when enableAssertions is set", () => {
  assert.deepEqual(debuggeeVmArgs(cfg(true), { vmArgs: ["-Xmx32m"] }), ["-ea", "-Xmx32m"]);
  assert.deepEqual(debuggeeVmArgs(cfg(true), {}), ["-ea"]);
});

test("debuggeeVmArgs omits -ea when disabled", () => {
  assert.deepEqual(debuggeeVmArgs(cfg(false), { vmArgs: ["-Xmx32m"] }), ["-Xmx32m"]);
  assert.deepEqual(debuggeeVmArgs(cfg(false), {}), []);
});

test("the project -ea precedes launch vmArgs so a launch -da overrides it", () => {
  assert.deepEqual(debuggeeVmArgs(cfg(true), { vmArgs: ["-da"] }), ["-ea", "-da"]);
});
