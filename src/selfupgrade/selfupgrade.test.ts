import { chmodSync, readFileSync, statSync, writeFileSync } from "node:fs";
import TempDir from "../TempDir.ts";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import {
  downloadBinary,
  latestRelease,
  platformTarget,
  replaceBinary,
  sameVersion,
  selfUpgrade,
} from "./selfupgrade.ts";

test("platform targets match the release asset names", () => {
  expect(platformTarget("linux", "x64")).toBe("cappu-linux-x64");
  expect(platformTarget("linux", "arm64")).toBe("cappu-linux-arm64");
  expect(platformTarget("darwin", "arm64")).toBe("cappu-darwin-arm64");
  expect(platformTarget("win32", "x64")).toBe("cappu-win-x64.exe");
  // CD builds no windows-arm64, no macOS x64 (Node SEA limitation), no other platforms
  expect(platformTarget("win32", "arm64")).toBeUndefined();
  expect(platformTarget("darwin", "x64")).toBeUndefined();
  expect(platformTarget("freebsd", "x64")).toBeUndefined();
});

function fakeRelease(release: unknown): (url: string) => Promise<unknown> {
  return () => Promise.resolve(release);
}

test("the matching asset of the latest release is selected", async () => {
  const ref = await latestRelease(
    "cappu-linux-x64",
    fakeRelease({
      tag_name: "v1.2.3",
      published_at: "2026-06-13T00:00:00Z",
      assets: [
        { name: "cappu-darwin-arm64", browser_download_url: "https://example/darwin" },
        { name: "cappu-linux-x64", browser_download_url: "https://example/linux" },
      ],
    }),
  );
  expect(ref).toEqual({
    assetName: "cappu-linux-x64",
    assetUrl: "https://example/linux",
    tag: "v1.2.3",
    publishedAt: "2026-06-13T00:00:00Z",
  });
});

test("missing release and missing asset are clear errors", async () => {
  await expect(latestRelease("cappu-linux-x64", fakeRelease({}))).rejects.toThrow(
    "no published release",
  );
  await expect(
    latestRelease("cappu-linux-x64", fakeRelease({ tag_name: "v1.0.0", assets: [] })),
  ).rejects.toThrow("has no asset 'cappu-linux-x64'");
});

test("the binary is downloaded from the asset url", async () => {
  const bytes = await downloadBinary("https://example/linux", () =>
    Promise.resolve(new TextEncoder().encode("ELF-ish bytes")),
  );
  expect(new TextDecoder().decode(bytes)).toBe("ELF-ish bytes");
});

test("downloadBinary forwards the progress callback to the fetcher", async () => {
  const calls: [number, number | undefined][] = [];
  await downloadBinary(
    "https://example/linux",
    (_url, onProgress) => {
      onProgress?.(50, 100);
      onProgress?.(100, 100);
      return Promise.resolve(new TextEncoder().encode("x"));
    },
    (received, total) => calls.push([received, total]),
  );
  expect(calls).toEqual([
    [50, 100],
    [100, 100],
  ]);
});

test("replaceBinary swaps the file in place and keeps it executable", () => {
  using dir = TempDir.create("cappu-upgrade-");
  const target = join(dir.path, "cappu");
  writeFileSync(target, "old");
  chmodSync(target, 0o755);
  replaceBinary(target, new TextEncoder().encode("new binary"));
  expect(readFileSync(target, "utf8")).toBe("new binary");
  expect(statSync(target).mode & 0o111).not.toBe(0);
});

test("selfUpgrade downloads and replaces the target binary end to end", async () => {
  using dir = TempDir.create("cappu-upgrade-");
  const target = join(dir.path, "cappu");
  writeFileSync(target, "v1");

  const result = await selfUpgrade({
    targetPath: target,
    platform: "linux",
    arch: "x64",
    fetchJson: fakeRelease({
      tag_name: "v2.0.0",
      published_at: "2026-06-13T12:00:00Z",
      assets: [{ name: "cappu-linux-x64", browser_download_url: "https://example/linux" }],
    }),
    fetchBytes: () => Promise.resolve(new TextEncoder().encode("v2")),
  });

  expect(readFileSync(target, "utf8")).toBe("v2");
  expect(result.release.tag).toBe("v2.0.0");
  expect(result.targetPath).toBe(target);
});

test("sameVersion compares the tag against the running version", () => {
  expect(sameVersion("v1.2.3", "1.2.3")).toBe(true);
  expect(sameVersion("1.2.3", "1.2.3")).toBe(true);
  expect(sameVersion("v1.2.4", "1.2.3")).toBe(false);
  expect(sameVersion("v1.2.3", "")).toBe(false); // unknown version never counts as up to date
});

test("selfUpgrade skips the download when already on the latest version", async () => {
  using dir = TempDir.create("cappu-upgrade-");
  const target = join(dir.path, "cappu");
  writeFileSync(target, "v1");

  const result = await selfUpgrade({
    targetPath: target,
    currentVersion: "2.0.0",
    platform: "linux",
    arch: "x64",
    fetchJson: fakeRelease({
      tag_name: "v2.0.0",
      published_at: "2026-06-13T12:00:00Z",
      assets: [{ name: "cappu-linux-x64", browser_download_url: "https://example/linux" }],
    }),
    fetchBytes: () => Promise.reject(new Error("should not download when already up to date")),
  });

  expect(result.upToDate).toBe(true);
  expect(readFileSync(target, "utf8")).toBe("v1"); // binary untouched
});

test("an unbuilt platform fails before any fetch", async () => {
  await expect(selfUpgrade({ platform: "win32", arch: "arm64" })).rejects.toThrow(
    "no cappu build for win32/arm64",
  );
});
