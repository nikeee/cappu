import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import { findJavaFiles } from "./workspace.ts";

test("a missing directory is empty, never a throw (Bun's globSync ENOENTs)", () => {
  expect(findJavaFiles("/definitely/not/here")).toEqual([]);
});

test("build directories are skipped even when globSync's exclude is ignored", () => {
  const dir = mkdtempSync(join(tmpdir(), "cappu-ws-"));
  try {
    mkdirSync(join(dir, "node_modules", "x"), { recursive: true });
    mkdirSync(join(dir, "src"), { recursive: true });
    writeFileSync(join(dir, "A.java"), "class A {}");
    writeFileSync(join(dir, "src", "B.java"), "class B {}");
    writeFileSync(join(dir, "node_modules", "x", "C.java"), "class C {}");
    expect(findJavaFiles(dir).sort()).toEqual([join(dir, "A.java"), join(dir, "src", "B.java")]);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
