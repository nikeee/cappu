import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import { loadConfig } from "../config.ts";
import { jdkDownloadUrl, parseJdkSpec, provisionJdk } from "./jdks.ts";

test("jdk specs parse as <distribution>-<major>", () => {
  expect(parseJdkSpec("temurin-21")).toEqual({ distribution: "temurin", version: "21" });
  expect(parseJdkSpec("corretto-17")).toEqual({ distribution: "corretto", version: "17" });
  expect(parseJdkSpec("zulu-21")).toBeUndefined(); // unknown distribution
  expect(parseJdkSpec("temurin-21.0.1")).toBeUndefined(); // only major versions
  expect(parseJdkSpec("temurin")).toBeUndefined();
});

test("download urls target the right distribution endpoints", () => {
  expect(jdkDownloadUrl({ distribution: "temurin", version: "21" }, "linux", "x64")).toBe(
    "https://api.adoptium.net/v3/binary/latest/21/ga/linux/x64/jdk/hotspot/normal/eclipse",
  );
  expect(jdkDownloadUrl({ distribution: "temurin", version: "17" }, "darwin", "arm64")).toBe(
    "https://api.adoptium.net/v3/binary/latest/17/ga/mac/aarch64/jdk/hotspot/normal/eclipse",
  );
  expect(jdkDownloadUrl({ distribution: "corretto", version: "21" }, "linux", "x64")).toBe(
    "https://corretto.aws/downloads/latest/amazon-corretto-21-x64-linux-jdk.tar.gz",
  );
  expect(jdkDownloadUrl({ distribution: "corretto", version: "17" }, "win32", "x64")).toBe(
    "https://corretto.aws/downloads/latest/amazon-corretto-17-x64-windows-jdk.zip",
  );
  expect(
    jdkDownloadUrl({ distribution: "temurin", version: "21" }, "freebsd", "x64"),
  ).toBeUndefined();
});

test("a cached archive provisions without any network", async () => {
  const store = mkdtempSync(join(tmpdir(), "cappu-jdkstore-"));
  const project = mkdtempSync(join(tmpdir(), "cappu-jdkproj-"));
  const previous = process.env.CAPPU_JDK_STORE;
  process.env.CAPPU_JDK_STORE = store;
  try {
    // a minimal fake JDK archive: <top>/bin/java, as real JDK tarballs ship
    const stage = mkdtempSync(join(tmpdir(), "cappu-jdkstage-"));
    mkdirSync(join(stage, "jdk-21.0.1+10", "bin"), { recursive: true });
    writeFileSync(join(stage, "jdk-21.0.1+10", "bin", "java"), "#!/bin/sh\n");
    const archive = join(store, `temurin-21-${process.platform}-${process.arch}.tar.gz`);
    execFileSync("tar", ["-czf", archive, "-C", stage, "jdk-21.0.1+10"]);
    rmSync(stage, { recursive: true, force: true });

    const config = loadConfig(undefined, project);
    const first = await provisionJdk(config, "temurin-21");
    expect(first.fromCache).toBe(true);
    expect(first.alreadyProvisioned).toBe(false);
    // the top-level archive directory is stripped: bin/java sits directly
    // under .cappu/jdks/temurin-21
    expect(first.jdkDir).toBe(join(project, ".cappu", "jdks", "temurin-21"));
    expect(readFileSync(join(first.jdkDir, "bin", "java"), "utf8")).toBe("#!/bin/sh\n");

    const second = await provisionJdk(config, "temurin-21");
    expect(second.alreadyProvisioned).toBe(true);

    await expect(provisionJdk(config, "not-a-jdk")).rejects.toThrow("unknown jdk");
    expect(existsSync(join(project, ".cappu", "jdks", "not-a-jdk"))).toBe(false);
  } finally {
    if (previous === undefined) delete process.env.CAPPU_JDK_STORE;
    else process.env.CAPPU_JDK_STORE = previous;
    rmSync(store, { recursive: true, force: true });
    rmSync(project, { recursive: true, force: true });
  }
});
