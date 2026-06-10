import { test } from "node:test";
import { expect } from "expect";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { DEFAULT_CONFIG_NAME, loadConfig, resolveConfigPath } from "./config.ts";

test("JSONC parses with comments and trailing commas; sections map", () => {
  const dir = mkdtempSync(join(tmpdir(), "cfg-"));
  writeFileSync(
    join(dir, DEFAULT_CONFIG_NAME),
    [
      "{",
      "  // compiled dependencies",
      '  "compilerOptions": {',
      '    "classPath": ["lib/classes",],',
      '    "sourcePaths": ["src/main/java"],',
      '    "outDir": "build",',
      '    "failOnDegrade": true,',
      "  },",
      '  "lspOptions": { "inlayHints": { "varTypes": false } },',
      "}",
    ].join("\n"),
  );
  const config = loadConfig(undefined, dir);
  expect(config.compilerOptions.classPath).toEqual(["lib/classes"]);
  expect(config.compilerOptions.sourcePaths).toEqual(["src/main/java"]);
  expect(config.compilerOptions.outDir).toBe("build");
  expect(config.compilerOptions.failOnDegrade).toBe(true);
  expect(config.lspOptions.inlayHints).toEqual({ varTypes: false });
  // Relative entries resolve against the config's directory.
  expect(resolveConfigPath(config, "lib/classes")).toBe(join(dir, "lib/classes"));
});

test("a missing default config yields the empty config; a missing explicit one throws", () => {
  const dir = mkdtempSync(join(tmpdir(), "cfg-"));
  const config = loadConfig(undefined, dir);
  expect(config.compilerOptions.classPath).toEqual([]);
  expect(config.lspOptions).toEqual({});
  expect(() => loadConfig("nope.json", dir)).toThrow(/not found/);
});
