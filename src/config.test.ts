import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import {
  configJsonSchema,
  CONFIG_TEMPLATE,
  DEFAULT_CONFIG_NAME,
  loadConfig,
  resolveConfigPath,
} from "./config.ts";

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
  expect(config.compilerOptions.classPath).toEqual(["./lib/classes"]);
  expect(config.lspOptions).toEqual({});
  expect(() => loadConfig("nope.json", dir)).toThrow(/not found/);
});

test("a shape violation throws with the offending path", () => {
  const dir = mkdtempSync(join(tmpdir(), "cfg-"));
  writeFileSync(
    join(dir, DEFAULT_CONFIG_NAME),
    '{ "compilerOptions": { "classPath": "not-an-array", "quiet": 1 } }',
  );
  expect(() => loadConfig(undefined, dir)).toThrow(/classPath/);
  expect(() => loadConfig(undefined, dir)).toThrow(/quiet/);
});

test("unknown keys are ignored, comment-json metadata does not leak", () => {
  const dir = mkdtempSync(join(tmpdir(), "cfg-"));
  writeFileSync(
    join(dir, DEFAULT_CONFIG_NAME),
    '{ /* note */ "futureOption": true, "compilerOptions": { "outDir": "o" } }',
  );
  const config = loadConfig(undefined, dir);
  expect(config.compilerOptions.outDir).toBe("o");
  expect(Object.keys(config).sort()).toEqual([
    "baseDir",
    "compilerOptions",
    "dependencies",
    "fromFile",
    "lspOptions",
    "packageSources",
  ]);
  expect(config.packageSources).toEqual([
    "https://repo.maven.apache.org/maven2",
    "https://maven.google.com",
    "https://plugins.gradle.org/m2",
  ]);
});

test("the init template parses and validates against the schema", () => {
  const dir = mkdtempSync(join(tmpdir(), "cfg-"));
  writeFileSync(join(dir, DEFAULT_CONFIG_NAME), CONFIG_TEMPLATE);
  const config = loadConfig(undefined, dir);
  expect(config.compilerOptions.classPath).toEqual(["./lib/classes"]);
  expect(config.compilerOptions.quiet).toBe(false);
  expect(config.lspOptions.inlayHints).toEqual({ parameterNames: true, varTypes: true });
});

test("the generated JSON schema mirrors the config shape", () => {
  const schema = JSON.parse(configJsonSchema()) as {
    type: string;
    properties: Record<string, unknown>;
  };
  expect(schema.type).toBe("object");
  expect(Object.keys(schema.properties).sort()).toEqual([
    "compilerOptions",
    "dependencies",
    "lspOptions",
    "packageSources",
  ]);
});
