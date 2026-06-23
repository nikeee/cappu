import { existsSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import TempDir from "./TempDir.ts";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import { cacheRoot, cleanCache } from "./cacheDir.ts";

test("cacheRoot follows XDG_CACHE_HOME", () => {
  expect(cacheRoot({ XDG_CACHE_HOME: "/x/cache" })).toBe(join("/x/cache", "cappu"));
});

test("cleanCache removes the cache root and reports what it deleted", () => {
  using xdg = TempDir.create("cappu-xdg-");
  try {
    const root = join(xdg.path, "cappu");
    mkdirSync(join(root, "packages", "g"), { recursive: true });
    writeFileSync(join(root, "packages", "g", "a.jar"), "x");
    // env is fully controlled - never touches the developer's real cache
    const removed = cleanCache({ XDG_CACHE_HOME: xdg.path });
    expect(removed).toEqual([root]);
    expect(existsSync(root)).toBe(false);
    // a second clean has nothing to do
    expect(cleanCache({ XDG_CACHE_HOME: xdg.path })).toEqual([]);
  } finally {
    rmSync(xdg.path, { recursive: true, force: true });
  }
});

test("cleanCache also clears env-override stores outside the root", () => {
  using xdg = TempDir.create("cappu-xdg-");
  using pkg = TempDir.create("cappu-pkg-");
  try {
    mkdirSync(join(xdg.path, "cappu"), { recursive: true });
    writeFileSync(join(pkg.path, "a.jar"), "x");
    const removed = cleanCache({ XDG_CACHE_HOME: xdg.path, CAPPU_PACKAGE_STORE: pkg.path });
    expect(removed).toEqual([join(xdg.path, "cappu"), pkg.path]);
    expect(existsSync(pkg.path)).toBe(false);
  } finally {
    rmSync(xdg.path, { recursive: true, force: true });
    rmSync(pkg.path, { recursive: true, force: true });
  }
});
