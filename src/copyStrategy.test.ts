import { readFileSync, statSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import { materialize } from "./copyStrategy.ts";
import TempDir from "./TempDir.ts";

test("materialize copies content and makes the result read-only", () => {
  using dir = TempDir.create("cappu-copy-");
  const src = join(dir.path, "src.jar");
  const dest = join(dir.path, "dest.jar");
  writeFileSync(src, "jar-bytes");

  materialize(src, dest);

  expect(readFileSync(dest, "utf8")).toBe("jar-bytes");
  expect(statSync(dest).mode & 0o777).toBe(0o444);
});

test("materialize overwrites a pre-existing read-only destination", () => {
  using dir = TempDir.create("cappu-copy-");
  const dest = join(dir.path, "dest.jar");
  const first = join(dir.path, "first.jar");
  const second = join(dir.path, "second.jar");
  writeFileSync(first, "new");
  writeFileSync(second, "newer");

  materialize(first, dest); // leaves dest at 0444
  materialize(second, dest); // must not fail on the 0444 dest

  expect(readFileSync(dest, "utf8")).toBe("newer");
});

// On Linux the strategy hardlinks, so src and dest share an inode (same temp
// dir is one filesystem). Other platforms clone/copy into a fresh inode.
test("materialize hardlinks on linux", { skip: process.platform !== "linux" }, () => {
  using dir = TempDir.create("cappu-copy-");
  const src = join(dir.path, "src.jar");
  const dest = join(dir.path, "dest.jar");
  writeFileSync(src, "shared");

  materialize(src, dest);

  expect(statSync(dest).ino).toBe(statSync(src).ino);
});
