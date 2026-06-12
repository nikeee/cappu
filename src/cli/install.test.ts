import { test } from "node:test";

import { expect } from "expect";

import { progressEnabled } from "./install.ts";

test("the progress bar needs a TTY and respects NO_COLOR", () => {
  expect(progressEnabled(true, {})).toBe(true);
  expect(progressEnabled(false, {})).toBe(false);
  expect(progressEnabled(undefined, {})).toBe(false);
  // NO_COLOR (https://no-color.org): set and non-empty disables the bar...
  expect(progressEnabled(true, { NO_COLOR: "1" })).toBe(false);
  expect(progressEnabled(true, { NO_COLOR: "anything" })).toBe(false);
  // ...but an empty value does not count as set
  expect(progressEnabled(true, { NO_COLOR: "" })).toBe(true);
});
