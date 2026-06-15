import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import { cacheRoot, cleanCache } from "./cacheDir.ts";

test("cacheRoot follows XDG_CACHE_HOME", () => {
  expect(cacheRoot({ XDG_CACHE_HOME: "/x/cache" })).toBe(join("/x/cache", "cappu"));
});

test("cleanCache removes the cache root and reports what it deleted", () => {
  const xdg = mkdtempSync(join(tmpdir(), "cappu-xdg-"));
  try {
    const root = join(xdg, "cappu");
    mkdirSync(join(root, "packages", "g"), { recursive: true });
    writeFileSync(join(root, "packages", "g", "a.jar"), "x");
    // env is fully controlled - never touches the developer's real cache
    const removed = cleanCache({ XDG_CACHE_HOME: xdg });
    expect(removed).toEqual([root]);
    expect(existsSync(root)).toBe(false);
    // a second clean has nothing to do
    expect(cleanCache({ XDG_CACHE_HOME: xdg })).toEqual([]);
  } finally {
    rmSync(xdg, { recursive: true, force: true });
  }
});

test("cleanCache also clears env-override stores outside the root", () => {
  const xdg = mkdtempSync(join(tmpdir(), "cappu-xdg-"));
  const pkg = mkdtempSync(join(tmpdir(), "cappu-pkg-"));
  try {
    mkdirSync(join(xdg, "cappu"), { recursive: true });
    writeFileSync(join(pkg, "a.jar"), "x");
    const removed = cleanCache({ XDG_CACHE_HOME: xdg, CAPPU_PACKAGE_STORE: pkg });
    expect(removed).toEqual([join(xdg, "cappu"), pkg]);
    expect(existsSync(pkg)).toBe(false);
  } finally {
    rmSync(xdg, { recursive: true, force: true });
    rmSync(pkg, { recursive: true, force: true });
  }
});
