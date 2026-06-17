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
      '    "failOnDegrade": true,',
      "  },",
      '  "lspOptions": { "inlayHints": { "varTypes": false } },',
      "}",
    ].join("\n"),
  );
  const config = loadConfig(undefined, dir);
  expect(config.compilerOptions.classPath).toEqual(["lib/classes"]);
  expect(config.compilerOptions.sourcePaths).toEqual(["src/main/java"]);
  expect(config.compilerOptions.failOnDegrade).toBe(true);
  // the omitted parameterNames falls back to its schema default
  expect(config.lspOptions.inlayHints).toEqual({ parameterNames: true, varTypes: false });
  // Relative entries resolve against the config's directory.
  expect(resolveConfigPath(config, "lib/classes")).toBe(join(dir, "lib/classes"));
});

test("a missing default config yields the empty config; a missing explicit one throws", () => {
  const dir = mkdtempSync(join(tmpdir(), "cfg-"));
  const config = loadConfig(undefined, dir);
  expect(config.compilerOptions.classPath).toEqual(["./.cappu/lib/classes"]);
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
    '{ /* note */ "futureOption": true, "compilerOptions": { "quiet": true } }',
  );
  const config = loadConfig(undefined, dir);
  expect(config.compilerOptions.quiet).toBe(true);
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
  expect(config.compilerOptions.classPath).toEqual(["./.cappu/lib/classes"]);
  expect(config.compilerOptions.quiet).toBe(false);
  // the template only documents inlayHints (commented out); defaults apply downstream
  expect(config.lspOptions.inlayHints).toBeUndefined();
});

test("the generated JSON schema mirrors the config shape", () => {
  const schema = JSON.parse(configJsonSchema()) as {
    type: string;
    properties: Record<string, unknown>;
  };
  expect(schema.type).toBe("object");
  expect(Object.keys(schema.properties).sort()).toEqual([
    "artifactId",
    "compilerOptions",
    "dependencies",
    "groupId",
    "jdk",
    "license",
    "lspOptions",
    "packageSources",
    "publishRepository",
    "version",
  ]);
});

test("project version must be semver; coordinates use the Maven id charset", () => {
  const dir = mkdtempSync(join(tmpdir(), "cfg-"));
  const write = (obj: Record<string, unknown>): void =>
    writeFileSync(join(dir, DEFAULT_CONFIG_NAME), JSON.stringify(obj));

  write({ groupId: "com.example", artifactId: "lib", version: "1.0.0" });
  expect(loadConfig(undefined, dir).version).toBe("1.0.0");
  write({ version: "2.1.0-SNAPSHOT" });
  expect(loadConfig(undefined, dir).version).toBe("2.1.0-SNAPSHOT");

  write({ version: "1.0" }); // not semver
  expect(() => loadConfig(undefined, dir)).toThrow(/semver/);
  write({ version: "RELEASE" });
  expect(() => loadConfig(undefined, dir)).toThrow(/semver/);
  write({ groupId: "com example" }); // space not allowed in a Maven id
  expect(() => loadConfig(undefined, dir)).toThrow(/Maven id/);
});

test("a valid SPDX license is accepted; free text and unknown ids are rejected", () => {
  const dir = mkdtempSync(join(tmpdir(), "cfg-"));
  const write = (license: string): void =>
    writeFileSync(join(dir, DEFAULT_CONFIG_NAME), JSON.stringify({ license }));

  write("Apache-2.0");
  expect(loadConfig(undefined, dir).license).toBe("Apache-2.0");
  write("(MIT OR Apache-2.0)");
  expect(loadConfig(undefined, dir).license).toBe("(MIT OR Apache-2.0)");

  write("The Apache Software License, Version 2.0"); // free text
  expect(() => loadConfig(undefined, dir)).toThrow(/SPDX/);
  write("Definitely-Not-A-License"); // SPDX-shaped but unknown
  expect(() => loadConfig(undefined, dir)).toThrow(/SPDX/);
});
