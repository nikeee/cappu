// The whole `cappu format` path, from cappu.json to the bytes on disk. The unit
// tests cover the grouping itself (src/format/import-order.test.ts); what only
// this level can catch is the command dropping formatterOptions.importOrder on
// the way in.
//
// runFormat ends the process, so it is exercised through a real cli invocation.

import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import TempDir from "../TempDir.ts";

const here = import.meta.dirname;
const cli = join(here, "main.ts");
const tsx = join(here, "..", "..", "node_modules", ".bin", "tsx");

const unformatted =
  "package app;\n\nimport org.junit.Test;\nimport java.util.List;\n\nclass T {\n  List<String> xs;\n  Test t;\n}\n";

/** A project whose one source file is grouped the way `importOrder` does not want. */
function project(dir: string, config: string): string {
  writeFileSync(join(dir, "cappu.json"), config);
  const pkg = join(dir, "src", "main", "java", "app");
  mkdirSync(pkg, { recursive: true });
  const file = join(pkg, "T.java");
  writeFileSync(file, unformatted);
  return file;
}

/** Run `cappu format`, returning the exit code (0 ok, 1 unformatted). */
function runFormat(dir: string, ...args: string[]): number {
  try {
    execFileSync(tsx, [cli, "format", ...args], { cwd: dir, encoding: "utf8", stdio: "pipe" });
    return 0;
  } catch (e) {
    return (e as { status: number }).status;
  }
}

test("cappu format applies the configured importOrder", () => {
  using dir = TempDir.create("cappu-format-");
  const file = project(
    dir.path,
    '{ "formatterOptions": { "importOrder": ["org.*", "", "java.*"] } }',
  );

  // Check mode reports the file and changes nothing: a different grouping is
  // exactly what makes a file unformatted.
  expect(runFormat(dir.path)).toBe(1);
  expect(readFileSync(file, "utf8")).toBe(unformatted);

  expect(runFormat(dir.path, "--write")).toBe(0);
  expect(readFileSync(file, "utf8")).toBe(
    "package app;\n\nimport org.junit.Test;\n\nimport java.util.List;\n\nclass T {\n  List<String> xs;\n  Test t;\n}\n",
  );

  // The written file is formatted, so a second check passes.
  expect(runFormat(dir.path)).toBe(0);
});

// Without importOrder the command keeps google-java-format's single block, so
// the config's absence is as meaningful as its presence.
test("cappu format without importOrder keeps one block", () => {
  using dir = TempDir.create("cappu-format-");
  const file = project(dir.path, "{}");
  expect(runFormat(dir.path, "--write")).toBe(0);
  expect(readFileSync(file, "utf8")).toBe(
    "package app;\n\nimport java.util.List;\nimport org.junit.Test;\n\nclass T {\n  List<String> xs;\n  Test t;\n}\n",
  );
});
