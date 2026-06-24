import { test } from "node:test";

import { expect } from "expect";
import { parse } from "comment-json";

import { removeDependencyFromJsonc } from "./remove.ts";

const withDeps = `{
  // keep me
  "dependencies": {
    "implementation": {
      "org.kept:kept": "1.0", // and me
      "com.google.code.gson:gson": "2.14.0",
    },
  },
}
`;

test("removing a dependency drops only that key and reports it", () => {
  const { text, removed } = removeDependencyFromJsonc(
    withDeps,
    "implementation",
    "com.google.code.gson:gson",
  );
  expect(removed).toBe(true);
  const parsed = parse(text) as unknown as {
    dependencies: { implementation: Record<string, string> };
  };
  expect(parsed.dependencies.implementation).toEqual({ "org.kept:kept": "1.0" });
});

test("removing preserves the JSONC comments on the surviving entries", () => {
  const { text } = removeDependencyFromJsonc(
    withDeps,
    "implementation",
    "com.google.code.gson:gson",
  );
  expect(text).toContain("// keep me");
  expect(text).toContain("// and me"); // the comment on the kept entry stays
});

test("removing an absent key is a no-op flagged as not removed", () => {
  const { text, removed } = removeDependencyFromJsonc(withDeps, "implementation", "org.absent:x");
  expect(removed).toBe(false);
  expect(text).toBe(withDeps);
});

test("removing from a missing configuration section is a no-op", () => {
  const { removed } = removeDependencyFromJsonc(withDeps, "testImplementation", "org.kept:kept");
  expect(removed).toBe(false);
});
