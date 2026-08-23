// `cappu decompile` end to end: the command runs without a project config and
// reports unreadable input. The disassembly itself is covered by
// src/compiler/disasm.test.ts.
//
// runDecompile ends the process, so it is exercised through a real cli invocation.

import { execFileSync } from "node:child_process";
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import TempDir from "../TempDir.ts";

const here = import.meta.dirname;
const cli = join(here, "main.ts");
const tsx = join(here, "..", "..", "node_modules", ".bin", "tsx");
const classFile = join(
  here,
  "..",
  "..",
  "test-fixtures",
  "emitter",
  "emit-baselines",
  "Arithmetic.class",
);

function run(cwd: string, ...args: string[]): { status: number; stdout: string; stderr: string } {
  try {
    const stdout = execFileSync(tsx, [cli, "decompile", ...args], {
      cwd,
      encoding: "utf8",
      stdio: "pipe",
    });
    return { status: 0, stdout, stderr: "" };
  } catch (e) {
    const failure = e as { status: number; stdout: string; stderr: string };
    return { status: failure.status, stdout: failure.stdout, stderr: failure.stderr };
  }
}

test("prints a javap-style listing without a project config", () => {
  using dir = TempDir.create("cappu-decompile-cli-");
  const { status, stdout } = run(dir.path, classFile);
  expect(status).toBe(0);
  expect(stdout).toContain("class Arithmetic {");
  expect(stdout).toContain("0: iload_1");
  expect(stdout).toContain("iadd");
});

test("exits 2 with a usage line when no file is given", () => {
  using dir = TempDir.create("cappu-decompile-cli-");
  const { status, stderr } = run(dir.path);
  expect(status).toBe(2);
  expect(stderr).toContain("usage: cappu decompile");
});

test("exits 1 on input that is not a class file", () => {
  using dir = TempDir.create("cappu-decompile-cli-");
  const notAClass = join(dir.path, "notes.txt");
  writeFileSync(notAClass, "hello");
  const { status, stderr } = run(dir.path, notAClass);
  expect(status).toBe(1);
  expect(stderr).toContain("not a class file");
});

test("reports a directory argument the same way the Go build does", () => {
  using dir = TempDir.create("cappu-decompile-cli-");
  const { status, stderr } = run(dir.path, dir.path);
  expect(status).toBe(1);
  expect(stderr).toContain("is a directory");
});

test("keeps going after a bad file and still exits 1", () => {
  using dir = TempDir.create("cappu-decompile-cli-");
  const { status, stdout, stderr } = run(dir.path, join(dir.path, "missing.class"), classFile);
  expect(status).toBe(1);
  expect(stderr).toContain("missing.class");
  expect(stdout).toContain("class Arithmetic {");
});
