// JDK provisioning (nikeee/cappu#8): a cappu.json "jdk" entry like
// "temurin-21" or "corretto-17" is downloaded once into the per-user cache
// (same idea as the package store) and unpacked into the project-local
// .cappu/jdks/<spec> directory. Self-contained, mirroring src/packages/:
// nothing here prints; the CLI renders progress from the onProgress callback.

import { spawnSync } from "node:child_process";
import { createWriteStream, existsSync, mkdirSync, renameSync, rmSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join } from "node:path";
import { Readable } from "node:stream";
import { pipeline } from "node:stream/promises";

import type { CappuConfig } from "../config.ts";
import { resolveConfigPath } from "../config.ts";

const DISTRIBUTIONS = ["temurin", "corretto"] as const;
export type JdkDistribution = (typeof DISTRIBUTIONS)[number];

export interface JdkSpec {
  readonly distribution: JdkDistribution;
  /** The major feature version ("21", "17"). */
  readonly version: string;
}

/** Parse "temurin-21" / "corretto-17"; undefined if it is not that shape. */
export function parseJdkSpec(spec: string): JdkSpec | undefined {
  const dash = spec.lastIndexOf("-");
  if (dash < 0) return undefined;
  const distribution = spec.slice(0, dash) as JdkDistribution;
  const version = spec.slice(dash + 1);
  if (!DISTRIBUTIONS.includes(distribution)) return undefined;
  if (!/^\d+$/.test(version)) return undefined;
  return { distribution, version };
}

/**
 * The (redirecting) download url of the latest GA build for a spec on this
 * platform, or undefined when the distribution does not publish for it.
 */
export function jdkDownloadUrl(
  spec: JdkSpec,
  platform: NodeJS.Platform = process.platform,
  arch: string = process.arch,
): string | undefined {
  if (spec.distribution === "temurin") {
    const os = { linux: "linux", darwin: "mac", win32: "windows" }[platform as string];
    const cpu = { x64: "x64", arm64: "aarch64" }[arch];
    if (!os || !cpu) return undefined;
    return `https://api.adoptium.net/v3/binary/latest/${spec.version}/ga/${os}/${cpu}/jdk/hotspot/normal/eclipse`;
  }
  // corretto: stable "latest" urls per os/arch; windows ships a .zip, which
  // bsdtar also unpacks, so the extension only matters for the cache name
  const os = { linux: "linux", darwin: "macos", win32: "windows" }[platform as string];
  const cpu = { x64: "x64", arm64: "aarch64" }[arch];
  if (!os || !cpu) return undefined;
  const extension = platform === "win32" ? "zip" : "tar.gz";
  return `https://corretto.aws/downloads/latest/amazon-corretto-${spec.version}-${cpu}-${os}-jdk.${extension}`;
}

// The per-user archive cache. A CACHE (XDG_CACHE_HOME), like the package
// store; CAPPU_JDK_STORE overrides (tests, CI).
function jdkStoreDir(): string {
  return (
    process.env.CAPPU_JDK_STORE ??
    join(process.env.XDG_CACHE_HOME ?? join(homedir(), ".cache"), "cappu", "jdks")
  );
}

/** Where a provisioned spec lives inside the project: .cappu/jdks/<spec>. */
export function projectJdkDir(config: CappuConfig, spec: string): string {
  return resolveConfigPath(config, join(".cappu", "jdks", spec));
}

// A binary of the provisioned JDK for config's "jdk" entry, or undefined
// when no jdk is configured or it has not been unpacked yet (callers fall
// back to PATH binaries).
function provisionedBin(config: CappuConfig, name: string): string | undefined {
  if (config.jdk === undefined) return undefined;
  const bin = join(
    projectJdkDir(config, config.jdk),
    "bin",
    process.platform === "win32" ? `${name}.exe` : name,
  );
  return existsSync(bin) ? bin : undefined;
}

/** The provisioned JDK's javac (callers fall back to compilerOptions.javac). */
export function provisionedJavac(config: CappuConfig): string | undefined {
  return provisionedBin(config, "javac");
}

/** The provisioned JDK's java launcher. */
export function provisionedJava(config: CappuConfig): string | undefined {
  return provisionedBin(config, "java");
}

export interface ProvisionResult {
  /** The unpacked JDK root (contains bin/, lib/, ...). */
  jdkDir: string;
  /** True when nothing was downloaded or unpacked (already provisioned). */
  alreadyProvisioned: boolean;
  /** True when the archive came from the per-user cache. */
  fromCache: boolean;
}

/** Streams `url` (following redirects) to `file`, reporting byte progress. */
async function downloadTo(
  url: string,
  file: string,
  onProgress?: (received: number, total: number | undefined) => void,
): Promise<void> {
  const response = await fetch(url);
  if (!response.ok || !response.body) {
    throw new Error(`download failed: HTTP ${response.status} for ${url}`);
  }
  const length = response.headers.get("content-length");
  const total = length ? Number(length) : undefined;
  let received = 0;
  const progress = new TransformStream<Uint8Array, Uint8Array>({
    transform(chunk, controller) {
      received += chunk.byteLength;
      onProgress?.(received, total);
      controller.enqueue(chunk);
    },
  });
  mkdirSync(dirname(file), { recursive: true });
  await pipeline(
    Readable.fromWeb(response.body.pipeThrough(progress)),
    createWriteStream(`${file}.part`),
  );
  renameSync(`${file}.part`, file);
}

// Unpack with the system tar (bsdtar on windows/mac, GNU tar on linux - both
// handle .tar.gz and .zip): one top-level directory is stripped so the target
// directly contains bin/, lib/, ...
function unpack(archive: string, target: string): void {
  mkdirSync(target, { recursive: true });
  const result = spawnSync("tar", ["-xf", archive, "--strip-components=1", "-C", target], {
    stdio: ["ignore", "ignore", "pipe"],
  });
  if (result.status !== 0) {
    rmSync(target, { recursive: true, force: true });
    throw new Error(`unpacking ${archive} failed: ${result.stderr?.toString().trim()}`);
  }
}

/**
 * Ensure config's "jdk" spec is unpacked under .cappu/jdks/<spec>. The
 * archive is downloaded into the per-user cache at most once; an already
 * unpacked project JDK short-circuits entirely.
 */
export async function provisionJdk(
  config: CappuConfig,
  specText: string,
  onProgress?: (received: number, total: number | undefined) => void,
): Promise<ProvisionResult> {
  const spec = parseJdkSpec(specText);
  if (!spec) {
    throw new Error(
      `unknown jdk '${specText}' (expected <distribution>-<version>, e.g. temurin-21)`,
    );
  }
  const url = jdkDownloadUrl(spec);
  if (!url) {
    throw new Error(`${specText} has no download for ${process.platform}/${process.arch}`);
  }

  const jdkDir = projectJdkDir(config, specText);
  // The java launcher doubles as the "fully unpacked" marker: a torn unpack
  // (interrupted tar) leaves no bin/java because unpack() removes the target.
  const launcher = join(jdkDir, "bin", process.platform === "win32" ? "java.exe" : "java");
  if (existsSync(launcher)) return { jdkDir, alreadyProvisioned: true, fromCache: false };

  const archive = join(
    jdkStoreDir(),
    `${specText}-${process.platform}-${process.arch}${url.endsWith(".zip") ? ".zip" : ".tar.gz"}`,
  );
  const fromCache = existsSync(archive);
  if (!fromCache) await downloadTo(url, archive, onProgress);
  unpack(archive, jdkDir);
  return { jdkDir, alreadyProvisioned: false, fromCache };
}
