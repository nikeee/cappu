// `cappu self-upgrade` (nikeee/cappu#6): replace the running binary with the
// latest published GitHub release. The release API and asset downloads are
// public (no token), and each platform's binary is uploaded as a raw release
// asset named cappu-<os>-<arch>. Self-contained, mirroring src/packages/ and
// src/jdks/: nothing here prints, the fetchers are injectable for tests; the
// CLI renders status.

import { chmodSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { basename, dirname, join } from "node:path";

const REPO = { owner: "nikeee", name: "cappu" };
const API = "https://api.github.com";

/** The release asset name to download for a platform, or undefined when none is built. */
export function platformTarget(
  platform: NodeJS.Platform = process.platform,
  arch: string = process.arch,
): string | undefined {
  const os = { linux: "linux", darwin: "darwin", win32: "windows" }[platform as string];
  const cpu = { x64: "x64", arm64: "arm64" }[arch];
  if (!os || !cpu) return undefined;
  // Every linux/darwin/windows x x64/arm64 combination is built; see the Go
  // Makefile build-all targets and .github/workflows/CD.yaml.
  // Asset names match `make build-all` output (the dist filenames), so CD can
  // upload dist/* with no renames. Windows keeps the .exe so the downloaded
  // asset is runnable as-is.
  return os === "windows" ? `cappu-win-${cpu}.exe` : `cappu-${os}-${cpu}`;
}

export type FetchJson = (url: string) => Promise<unknown>;
export type DownloadProgress = (received: number, total: number | undefined) => void;
export type FetchBytes = (url: string, onProgress?: DownloadProgress) => Promise<Uint8Array>;

/** The release asset to upgrade from, with the release it belongs to. */
export interface ReleaseRef {
  assetName: string;
  assetUrl: string;
  tag: string;
  publishedAt: string;
}

/** The asset matching `assetName` in the latest published release. */
export async function latestRelease(assetName: string, fetchJson: FetchJson): Promise<ReleaseRef> {
  const release = (await fetchJson(`${API}/repos/${REPO.owner}/${REPO.name}/releases/latest`)) as {
    tag_name?: string;
    published_at?: string;
    assets?: { name: string; browser_download_url: string }[];
  };
  if (!release.tag_name) throw new Error("no published release found");
  const asset = release.assets?.find(a => a.name === assetName);
  if (!asset) {
    throw new Error(`release ${release.tag_name} has no asset '${assetName}'`);
  }
  return {
    assetName: asset.name,
    assetUrl: asset.browser_download_url,
    tag: release.tag_name,
    publishedAt: release.published_at ?? "",
  };
}

/**
 * Whether the release tag names the running version. Tags are vX.Y.Z;
 * pkg.version is X.Y.Z. Plain equality, not a semver comparison - enough to skip
 * a redundant re-download; swap in a compare if downgrade protection is ever needed.
 */
export function sameVersion(tag: string, currentVersion: string): boolean {
  return currentVersion !== "" && tag.replace(/^v/, "") === currentVersion;
}

/** Download the raw binary release asset. */
export async function downloadBinary(
  assetUrl: string,
  fetchBytes: FetchBytes,
  onProgress?: DownloadProgress,
): Promise<Uint8Array> {
  return fetchBytes(assetUrl, onProgress);
}

/**
 * Replace `targetPath` with `bytes`, executable. POSIX renames over the
 * running binary (the process keeps the old inode); Windows cannot overwrite
 * a running .exe, so the old one is moved aside first and restored on failure.
 */
export function replaceBinary(targetPath: string, bytes: Uint8Array): void {
  // staged in the SAME directory so the rename is atomic (one filesystem)
  const staged = join(dirname(targetPath), `.${basename(targetPath)}.upgrade-${process.pid}`);
  writeFileSync(staged, bytes);
  chmodSync(staged, 0o755);
  if (process.platform === "win32") {
    const old = `${targetPath}.old-${process.pid}`;
    renameSync(targetPath, old);
    try {
      renameSync(staged, targetPath);
    } catch (e) {
      renameSync(old, targetPath); // put the working binary back
      throw e;
    }
    rmSync(old, { force: true }); // may stay until the process exits; best effort
  } else {
    renameSync(staged, targetPath);
  }
}

// Public GitHub API fetchers (no auth - the repo and its release assets are
// public). GitHub requires a User-Agent. fetch follows the asset 302 to blob
// storage on its own.
function githubFetchers(): { fetchJson: FetchJson; fetchBytes: FetchBytes } {
  const headers = {
    "User-Agent": "cappu-self-upgrade",
    "X-GitHub-Api-Version": "2022-11-28",
  };
  return {
    fetchJson: async url => {
      const response = await fetch(url, {
        headers: { ...headers, Accept: "application/vnd.github+json" },
      });
      if (!response.ok) throw new Error(`GitHub API ${response.status} for ${url}`);
      return response.json();
    },
    fetchBytes: async (url, onProgress) => {
      const response = await fetch(url, { headers });
      if (!response.ok || !response.body) {
        throw new Error(`download failed: HTTP ${response.status} for ${url}`);
      }
      const length = response.headers.get("content-length");
      const total = length ? Number(length) : undefined;
      const reader = response.body.getReader();
      const chunks: Uint8Array[] = [];
      let received = 0;
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        chunks.push(value);
        received += value.byteLength;
        onProgress?.(received, total);
      }
      const out = new Uint8Array(received);
      let offset = 0;
      for (const chunk of chunks) {
        out.set(chunk, offset);
        offset += chunk.byteLength;
      }
      return out;
    },
  };
}

export interface UpgradeResult {
  assetName: string;
  release: ReleaseRef;
  targetPath: string;
  /** True when the running version already matched and nothing was replaced. */
  upToDate: boolean;
}

/**
 * Replace `targetPath` (the running binary by default) with the latest release
 * build for this platform, unless it is already current. Fetchers are
 * injectable; otherwise the public GitHub fetchers are used.
 */
export async function selfUpgrade(options: {
  targetPath?: string;
  currentVersion?: string;
  platform?: NodeJS.Platform;
  arch?: string;
  fetchJson?: FetchJson;
  fetchBytes?: FetchBytes;
  onDownloadProgress?: DownloadProgress;
}): Promise<UpgradeResult> {
  const assetName = platformTarget(options.platform, options.arch);
  if (!assetName) {
    throw new Error(
      `no cappu build for ${options.platform ?? process.platform}/${options.arch ?? process.arch}`,
    );
  }
  let { fetchJson, fetchBytes } = options;
  if (!fetchJson || !fetchBytes) {
    ({ fetchJson, fetchBytes } = githubFetchers());
  }
  const release = await latestRelease(assetName, fetchJson);
  const targetPath = options.targetPath ?? process.execPath;
  if (sameVersion(release.tag, options.currentVersion ?? "")) {
    return { assetName, release, targetPath, upToDate: true };
  }
  const bytes = await downloadBinary(release.assetUrl, fetchBytes, options.onDownloadProgress);
  replaceBinary(targetPath, bytes);
  return { assetName, release, targetPath, upToDate: false };
}
