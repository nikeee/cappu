import { mkdirSync, writeFileSync } from "node:fs";
import TempDir from "./TempDir.ts";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import { findJavaFiles } from "./workspace.ts";

test("a missing directory yields no files instead of throwing", () => {
  expect(findJavaFiles("/definitely/not/here")).toEqual([]);
});

test("build directories (node_modules, ...) are skipped", () => {
  using dir = TempDir.create("cappu-ws-");
  mkdirSync(join(dir.path, "node_modules", "x"), { recursive: true });
  mkdirSync(join(dir.path, "src"), { recursive: true });
  writeFileSync(join(dir.path, "A.java"), "class A {}");
  writeFileSync(join(dir.path, "src", "B.java"), "class B {}");
  writeFileSync(join(dir.path, "node_modules", "x", "C.java"), "class C {}");
  expect(findJavaFiles(dir.path).sort()).toEqual([
    join(dir.path, "A.java"),
    join(dir.path, "src", "B.java"),
  ]);
});
