import { execFileSync } from "node:child_process";
import TempDir from "../TempDir.ts";
import { rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import { runCompile } from "./compiler.ts";
import { loadConfig } from "../config.ts";
import { validateAgainstJavac } from "./validateJavac.ts";

function hasJdk(): boolean {
  try {
    execFileSync("javac", ["-version"], { stdio: "ignore" });
    execFileSync("javap", ["-version"], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
}
const jdk = hasJdk();

function inTempDir(files: Record<string, string>, body: (dir: string, paths: string[]) => void) {
  using dir = TempDir.create("cappu-validate-test-");
  try {
    const paths = Object.entries(files).map(([name, text]) => {
      const p = join(dir.path, name);
      writeFileSync(p, text);
      return p;
    });
    body(dir.path, paths);
  } finally {
    rmSync(dir.path, { recursive: true, force: true });
  }
}

test("a clean class validates against javac", { skip: !jdk }, () => {
  inTempDir({ "V.java": "class V { int add(int a, int b) { return a + b; } }" }, (dir, paths) => {
    const result = runCompile(paths, { outDir: dir, config: loadConfig(undefined, dir) });
    expect(result.success).toBe(true);
    const validation = validateAgainstJavac(paths, result.written);
    expect(validation).toEqual({ ok: true, compared: 1 });
  });
});

test("a corrupted class file is reported as a mismatch", { skip: !jdk }, () => {
  inTempDir({ "W.java": "class W { int one() { return 1; } }" }, (dir, paths) => {
    const result = runCompile(paths, { outDir: dir, config: loadConfig(undefined, dir) });
    expect(result.success).toBe(true);
    // sabotage our output: claim a different body than javac will produce
    writeFileSync(
      join(dir, "W.java"),
      "class W { int one() { return 2; } }", // javac now compiles a different constant
    );
    const validation = validateAgainstJavac(paths, result.written);
    expect(validation.ok).toBe(false);
    if (!validation.ok && "mismatches" in validation) {
      expect(validation.mismatches[0]!.className).toBe("W");
    }
  });
});

test("an unavailable javac yields an error result, not a throw", () => {
  inTempDir({ "X.java": "class X { }" }, (dir, paths) => {
    const validation = validateAgainstJavac(paths, [], "cappu-no-such-javac");
    expect(validation.ok).toBe(false);
    if (!validation.ok) expect("error" in validation).toBe(true);
  });
});
