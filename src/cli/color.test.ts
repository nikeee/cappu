import { test } from "node:test";

import { expect } from "expect";

import { colorEnabled } from "./color.ts";

test("coloured output needs a TTY and respects NO_COLOR", () => {
  expect(colorEnabled(true, {})).toBe(true);
  expect(colorEnabled(false, {})).toBe(false);
  expect(colorEnabled(undefined, {})).toBe(false);
  // NO_COLOR (https://no-color.org): set and non-empty disables colour...
  expect(colorEnabled(true, { NO_COLOR: "1" })).toBe(false);
  expect(colorEnabled(true, { NO_COLOR: "anything" })).toBe(false);
  // ...but an empty value does not count as set
  expect(colorEnabled(true, { NO_COLOR: "" })).toBe(true);
  // an AI agent driving cappu implies NO_COLOR
  expect(colorEnabled(true, { CLAUDECODE: "1" })).toBe(false);
  expect(colorEnabled(true, { AGENT: "goose" })).toBe(false);
});
