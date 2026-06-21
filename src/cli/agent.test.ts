import { test } from "node:test";

import { expect } from "expect";

import { agentEnabled } from "./agent.ts";

test("agentEnabled detects agent env vars", () => {
  expect(agentEnabled({})).toBe(false);
  // cross-tool proposal
  expect(agentEnabled({ AGENT: "goose" })).toBe(true);
  expect(agentEnabled({ AGENT: "1" })).toBe(true);
  // per-tool vars
  expect(agentEnabled({ CLAUDECODE: "1" })).toBe(true);
  expect(agentEnabled({ CURSOR_AGENT: "1" })).toBe(true);
  expect(agentEnabled({ CODEX_SANDBOX: "seatbelt" })).toBe(true);
  expect(agentEnabled({ TRAE_AI_SHELL_ID: "abc123" })).toBe(true);
  // set but empty does not count
  expect(agentEnabled({ AGENT: "" })).toBe(false);
  // unrelated var
  expect(agentEnabled({ SHELL: "/bin/zsh" })).toBe(false);
});
