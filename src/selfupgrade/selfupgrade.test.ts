import { chmodSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import TempDir from "../TempDir.ts";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import { writeZip } from "../compiler/zipWriter.ts";
import {
  downloadBinary,
  latestArtifact,
  platformTarget,
  replaceBinary,
  resolveToken,
  selfUpgrade,
  type UpgradeTarget,
} from "./selfupgrade.ts";

test("platform targets match the CD artifact names", () => {
  expect(platformTarget("linux", "x64")).toEqual({
    artifact: "cappu-linux-x64",
    binaryName: "cappu",
  });
  expect(platformTarget("linux", "arm64")).toEqual({
    artifact: "cappu-linux-arm64",
    binaryName: "cappu",
  });
  expect(platformTarget("darwin", "arm64")).toEqual({
    artifact: "cappu-darwin-arm64",
    binaryName: "cappu",
  });
  expect(platformTarget("win32", "x64")).toEqual({
    artifact: "cappu-windows-x64",
    binaryName: "cappu.exe",
  });
  // CD builds no windows-arm64, no macOS x64 (Node SEA limitation), no other platforms
  expect(platformTarget("win32", "arm64")).toBeUndefined();
  expect(platformTarget("darwin", "x64")).toBeUndefined();
  expect(platformTarget("freebsd", "x64")).toBeUndefined();
});

const LINUX: UpgradeTarget = { artifact: "cappu-linux-x64", binaryName: "cappu" };

function fakeJson(runs: unknown, artifacts: unknown): (url: string) => Promise<unknown> {
  return url => Promise.resolve(url.endsWith("/artifacts") ? artifacts : runs);
}

test("the latest successful CD run's matching artifact is selected", async () => {
  const ref = await latestArtifact(
    LINUX,
    fakeJson(
      { workflow_runs: [{ id: 42, head_sha: "abc1234def", created_at: "2026-06-13T00:00:00Z" }] },
      {
        artifacts: [
          { id: 7, name: "cappu-darwin-arm64", expired: false },
          { id: 9, name: "cappu-linux-x64", expired: false },
        ],
      },
    ),
  );
  expect(ref).toEqual({
    id: 9,
    name: "cappu-linux-x64",
    runSha: "abc1234def",
    runCreatedAt: "2026-06-13T00:00:00Z",
  });
});

test("missing run, missing artifact and expired artifact are clear errors", async () => {
  await expect(latestArtifact(LINUX, fakeJson({ workflow_runs: [] }, {}))).rejects.toThrow(
    "no successful CD run",
  );
  await expect(
    latestArtifact(
      LINUX,
      fakeJson({ workflow_runs: [{ id: 1, head_sha: "a", created_at: "t" }] }, { artifacts: [] }),
    ),
  ).rejects.toThrow("has no artifact 'cappu-linux-x64'");
  await expect(
    latestArtifact(
      LINUX,
      fakeJson(
        { workflow_runs: [{ id: 1, head_sha: "a", created_at: "t" }] },
        { artifacts: [{ id: 9, name: "cappu-linux-x64", expired: true }] },
      ),
    ),
  ).rejects.toThrow("has expired");
});

test("the binary is extracted from the artifact zip", async () => {
  const zip = writeZip([{ name: "cappu", bytes: new TextEncoder().encode("ELF-ish bytes") }]);
  const bytes = await downloadBinary(9, "cappu", () => Promise.resolve(zip));
  expect(new TextDecoder().decode(bytes)).toBe("ELF-ish bytes");

  await expect(
    downloadBinary(9, "cappu", () => Promise.resolve(new Uint8Array([1, 2]))),
  ).rejects.toThrow("not a valid zip");
  const wrong = writeZip([{ name: "readme.txt", bytes: new Uint8Array([1]) }]);
  // a single non-directory entry is accepted even under a different name
  expect((await downloadBinary(9, "cappu", () => Promise.resolve(wrong))).length).toBe(1);
});

test("downloadBinary forwards the progress callback to the fetcher", async () => {
  const zip = writeZip([{ name: "cappu", bytes: new TextEncoder().encode("x") }]);
  const calls: [number, number | undefined][] = [];
  await downloadBinary(
    9,
    "cappu",
    (_url, onProgress) => {
      onProgress?.(50, 100);
      onProgress?.(100, 100);
      return Promise.resolve(zip);
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
  try {
    const target = join(dir.path, "cappu");
    writeFileSync(target, "old");
    chmodSync(target, 0o755);
    replaceBinary(target, new TextEncoder().encode("new binary"));
    expect(readFileSync(target, "utf8")).toBe("new binary");
    expect(statSync(target).mode & 0o111).not.toBe(0); // still executable
  } finally {
    rmSync(dir.path, { recursive: true, force: true });
  }
});

test("selfUpgrade downloads and replaces the target binary end to end", async () => {
  using dir = TempDir.create("cappu-upgrade-");
  try {
    const target = join(dir.path, "cappu");
    writeFileSync(target, "v1");
    const zip = writeZip([{ name: "cappu", bytes: new TextEncoder().encode("v2") }]);

    const result = await selfUpgrade({
      targetPath: target,
      platform: "linux",
      arch: "x64",
      fetchJson: fakeJson(
        { workflow_runs: [{ id: 5, head_sha: "deadbee", created_at: "2026-06-13T12:00:00Z" }] },
        { artifacts: [{ id: 3, name: "cappu-linux-x64", expired: false }] },
      ),
      fetchBytes: () => Promise.resolve(zip),
    });

    expect(readFileSync(target, "utf8")).toBe("v2");
    expect(result.artifact.runSha).toBe("deadbee");
    expect(result.targetPath).toBe(target);
  } finally {
    rmSync(dir.path, { recursive: true, force: true });
  }
});

test("an unbuilt platform fails before any fetch", async () => {
  await expect(selfUpgrade({ platform: "win32", arch: "arm64", token: "x" })).rejects.toThrow(
    "no cappu build for win32/arm64",
  );
});

test("the token comes from the environment, in precedence order", () => {
  expect(resolveToken({ CAPPU_GITHUB_TOKEN: "a", GITHUB_TOKEN: "b", GH_TOKEN: "c" })).toBe("a");
  expect(resolveToken({ GITHUB_TOKEN: "b", GH_TOKEN: "c" })).toBe("b");
  expect(resolveToken({ GH_TOKEN: "c" })).toBe("c");
});
