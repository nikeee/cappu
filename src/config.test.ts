import { mkdirSync, writeFileSync } from "node:fs";
import TempDir from "./TempDir.ts";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import {
  configJsonSchema,
  CONFIG_TEMPLATE,
  DEFAULT_CLASS_PATH,
  DEFAULT_CONFIG_NAME,
  EXTERNAL_CLASS_PATHS,
  loadConfig,
  resolveConfigPath,
} from "./config.ts";

const DEFAULT_CLASS_PATHS = [DEFAULT_CLASS_PATH, ...EXTERNAL_CLASS_PATHS];

test("JSONC parses with comments and trailing commas; sections map", () => {
  using dir = TempDir.create("cfg-");
  writeFileSync(
    join(dir.path, DEFAULT_CONFIG_NAME),
    [
      "{",
      "  // compiled dependencies",
      '  "compilerOptions": {',
      '    "classPath": ["lib/classes",],',
      '    "sourcePaths": ["src/main/java"],',
      '    "experimentalCompiler": { "enabled": true },',
      "  },",
      '  "lspOptions": { "inlayHints": { "varTypes": false } },',
      "}",
    ].join("\n"),
  );
  const config = loadConfig(undefined, dir.path);
  expect(config.compilerOptions.classPath).toEqual(["lib/classes"]);
  expect(config.compilerOptions.sourcePaths).toEqual(["src/main/java"]);
  // nested experimentalCompiler: the set field plus its defaults
  expect(config.compilerOptions.experimentalCompiler).toEqual({
    enabled: true,
    failOnDegrade: true,
    validate: false,
    debugInfo: false,
  });
  // the omitted parameterNames falls back to its schema default
  expect(config.lspOptions.inlayHints).toEqual({ parameterNames: true, varTypes: false });
  // Relative entries resolve against the config's directory.
  expect(resolveConfigPath(config, "lib/classes")).toBe(join(dir.path, "lib/classes"));
});

test("a missing default config yields the empty config; a missing explicit one throws", () => {
  using dir = TempDir.create("cfg-");
  const config = loadConfig(undefined, dir.path);
  expect(config.compilerOptions.classPath).toEqual(DEFAULT_CLASS_PATHS);
  // generated production sources (src/generated/java) are a default source root
  expect(config.compilerOptions.sourcePaths).toEqual([
    "./src/main/java",
    "./src/generated/java",
  ]);
  expect(config.lspOptions).toEqual({});
  expect(() => loadConfig("nope.json", dir.path)).toThrow(/not found/);
});

test("the default config is discovered from a subdirectory (walks up to the project root)", () => {
  using dir = TempDir.create("cfg-");
  writeFileSync(
    join(dir.path, DEFAULT_CONFIG_NAME),
    '{ "compilerOptions": { "sourcePaths": ["src/main/java"] } }',
  );
  const nested = join(dir.path, "src", "main", "java");
  mkdirSync(nested, { recursive: true });
  const config = loadConfig(undefined, nested);
  expect(config.fromFile).toBe(true);
  // baseDir is the project root (where cappu.json lives), not the cwd, so
  // relative paths still resolve against the project.
  expect(config.baseDir).toBe(dir.path);
  expect(config.compilerOptions.sourcePaths).toEqual(["src/main/java"]);
});

test("a shape violation throws with the offending path", () => {
  using dir = TempDir.create("cfg-");
  writeFileSync(
    join(dir.path, DEFAULT_CONFIG_NAME),
    '{ "compilerOptions": { "classPath": "not-an-array", "quiet": 1 } }',
  );
  expect(() => loadConfig(undefined, dir.path)).toThrow(/classPath/);
  expect(() => loadConfig(undefined, dir.path)).toThrow(/quiet/);
});

test("unknown keys are ignored, comment-json metadata does not leak", () => {
  using dir = TempDir.create("cfg-");
  writeFileSync(
    join(dir.path, DEFAULT_CONFIG_NAME),
    '{ /* note */ "futureOption": true, "compilerOptions": { "quiet": true } }',
  );
  const config = loadConfig(undefined, dir.path);
  expect(config.compilerOptions.quiet).toBe(true);
  expect(Object.keys(config).sort()).toEqual([
    "baseDir",
    "compilerOptions",
    "dapOptions",
    "dependencies",
    "formatterOptions",
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
  using dir = TempDir.create("cfg-");
  writeFileSync(join(dir.path, DEFAULT_CONFIG_NAME), CONFIG_TEMPLATE);
  const config = loadConfig(undefined, dir.path);
  expect(config.compilerOptions.classPath).toEqual(DEFAULT_CLASS_PATHS);
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
    "dapOptions",
    "dependencies",
    "formatterOptions",
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
  using dir = TempDir.create("cfg-");
  const write = (obj: Record<string, unknown>): void =>
    writeFileSync(join(dir.path, DEFAULT_CONFIG_NAME), JSON.stringify(obj));

  write({ groupId: "com.example", artifactId: "lib", version: "1.0.0" });
  expect(loadConfig(undefined, dir.path).version).toBe("1.0.0");
  write({ version: "2.1.0-SNAPSHOT" });
  expect(loadConfig(undefined, dir.path).version).toBe("2.1.0-SNAPSHOT");

  write({ version: "1.0" }); // not semver
  expect(() => loadConfig(undefined, dir.path)).toThrow(/semver/);
  write({ version: "RELEASE" });
  expect(() => loadConfig(undefined, dir.path)).toThrow(/semver/);
  write({ groupId: "com example" }); // space not allowed in a Maven id
  expect(() => loadConfig(undefined, dir.path)).toThrow(/Maven id/);
});

test("a valid SPDX license is accepted; free text and unknown ids are rejected", () => {
  using dir = TempDir.create("cfg-");
  const write = (license: string): void =>
    writeFileSync(join(dir.path, DEFAULT_CONFIG_NAME), JSON.stringify({ license }));

  write("Apache-2.0");
  expect(loadConfig(undefined, dir.path).license).toBe("Apache-2.0");
  write("(MIT OR Apache-2.0)");
  expect(loadConfig(undefined, dir.path).license).toBe("(MIT OR Apache-2.0)");

  write("The Apache Software License, Version 2.0"); // free text
  expect(() => loadConfig(undefined, dir.path)).toThrow(/SPDX/);
  write("Definitely-Not-A-License"); // SPDX-shaped but unknown
  expect(() => loadConfig(undefined, dir.path)).toThrow(/SPDX/);
});

test("compiler output is an enum, release has a floor, publishRepository is a URL", () => {
  using dir = TempDir.create("cfg-");
  const write = (obj: Record<string, unknown>): void =>
    writeFileSync(join(dir.path, DEFAULT_CONFIG_NAME), JSON.stringify(obj));

  write({ compilerOptions: { output: "fat-jar" } });
  expect(loadConfig(undefined, dir.path).compilerOptions.output).toBe("fat-jar");
  write({ compilerOptions: { output: "exe" } }); // not in the enum
  expect(() => loadConfig(undefined, dir.path)).toThrow();

  write({ compilerOptions: { release: 21 } });
  expect(loadConfig(undefined, dir.path).compilerOptions.release).toBe(21);
  write({ compilerOptions: { release: 5 } }); // below the floor
  expect(() => loadConfig(undefined, dir.path)).toThrow();

  write({ publishRepository: "https://repo.example.com/maven2" });
  expect(loadConfig(undefined, dir.path).publishRepository).toBe("https://repo.example.com/maven2");
  write({ publishRepository: "not a url" });
  expect(() => loadConfig(undefined, dir.path)).toThrow();
});
