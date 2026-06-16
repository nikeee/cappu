// `cappu self-upgrade` (nikeee/cappu#6): replace the running binary with the
// freshest build. There are no GitHub releases yet, so the source is the
// latest successful CD.yaml run's uploaded artifact for this platform
// (cappu-<os>-<arch>, a zip wrapping the single compiled binary). The GitHub
// artifact API needs an actions:read token, so a token is required.
// Self-contained, mirroring src/packages/ and src/jdks/: nothing here prints,
// the fetchers are injectable for tests; the CLI renders status.

import { spawnSync } from "node:child_process";
import { chmodSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { basename, dirname, join } from "node:path";

import { readZipEntries } from "../compiler/zipReader.ts";

const REPO = { owner: "nikeee", name: "cappu" };
const WORKFLOW = "CD.yaml";
const API = "https://api.github.com";

/** The CD artifact and the binary name inside it for a platform. */
export interface UpgradeTarget {
  artifact: string;
  binaryName: string;
}

/** The CD artifact matching a platform, or undefined when none is built. */
export function platformTarget(
  platform: NodeJS.Platform = process.platform,
  arch: string = process.arch,
): UpgradeTarget | undefined {
  const os = { linux: "linux", darwin: "darwin", win32: "windows" }[platform as string];
  const cpu = { x64: "x64", arm64: "arm64" }[arch];
  if (!os || !cpu) return undefined;
  // Of the platforms CD builds, windows is x64-only and macOS is arm64-only
  // (Node SEA does not support macOS x64) - reject the combinations we never
  // ship. See .github/workflows/CD.yaml / tsdown.config.ts.
  if (os === "windows" && cpu !== "x64") return undefined;
  if (os === "darwin" && cpu !== "arm64") return undefined;
  return { artifact: `cappu-${os}-${cpu}`, binaryName: os === "windows" ? "cappu.exe" : "cappu" };
}

export type FetchJson = (url: string) => Promise<unknown>;
export type DownloadProgress = (received: number, total: number | undefined) => void;
export type FetchBytes = (url: string, onProgress?: DownloadProgress) => Promise<Uint8Array>;

/** The build artifact to upgrade from, with the run it came from. */
export interface ArtifactRef {
  id: number;
  name: string;
  /** The commit the CD run built. */
  runSha: string;
  runCreatedAt: string;
}

/** The artifact for `target` in the latest successful CD run on main. */
export async function latestArtifact(
  target: UpgradeTarget,
  fetchJson: FetchJson,
): Promise<ArtifactRef> {
  const runs = (await fetchJson(
    `${API}/repos/${REPO.owner}/${REPO.name}/actions/workflows/${WORKFLOW}/runs?branch=main&status=success&event=push&per_page=1`,
  )) as { workflow_runs?: { id: number; head_sha: string; created_at: string }[] };
  const run = runs.workflow_runs?.[0];
  if (!run) throw new Error("no successful CD run found on main");

  const artifacts = (await fetchJson(
    `${API}/repos/${REPO.owner}/${REPO.name}/actions/runs/${run.id}/artifacts`,
  )) as { artifacts?: { id: number; name: string; expired: boolean }[] };
  const artifact = artifacts.artifacts?.find(a => a.name === target.artifact);
  if (!artifact) throw new Error(`CD run ${run.id} has no artifact '${target.artifact}'`);
  if (artifact.expired) {
    throw new Error(`artifact '${target.artifact}' from CD run ${run.id} has expired`);
  }
  return {
    id: artifact.id,
    name: artifact.name,
    runSha: run.head_sha,
    runCreatedAt: run.created_at,
  };
}

/** Download the artifact zip and extract the single binary it wraps. */
export async function downloadBinary(
  artifactId: number,
  binaryName: string,
  fetchBytes: FetchBytes,
  onProgress?: DownloadProgress,
): Promise<Uint8Array> {
  const zip = await fetchBytes(
    `${API}/repos/${REPO.owner}/${REPO.name}/actions/artifacts/${artifactId}/zip`,
    onProgress,
  );
  const entries = readZipEntries(zip);
  if (!entries) throw new Error("the downloaded artifact is not a valid zip");
  const entry =
    entries.find(e => e.name === binaryName) ?? entries.find(e => !e.name.endsWith("/"));
  if (!entry) throw new Error(`the artifact did not contain ${binaryName}`);
  return entry.read();
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

/**
 * A GitHub token from the environment, else `gh auth token`. The artifact API
 * needs actions:read, so without one self-upgrade cannot work.
 */
export function resolveToken(env: NodeJS.ProcessEnv = process.env): string | undefined {
  const fromEnv = env.CAPPU_GITHUB_TOKEN ?? env.GITHUB_TOKEN ?? env.GH_TOKEN;
  if (fromEnv) return fromEnv;
  try {
    const result = spawnSync("gh", ["auth", "token"], { encoding: "utf8" });
    const token = result.status === 0 ? result.stdout.trim() : "";
    return token || undefined;
  } catch {
    return undefined;
  }
}

// Authenticated fetchers over the GitHub API. fetch follows the artifact-zip
// 302 to blob storage and strips Authorization on that cross-origin redirect
// (WHATWG fetch), which is exactly what the signed URL wants.
function githubFetchers(token: string): { fetchJson: FetchJson; fetchBytes: FetchBytes } {
  const headers = {
    Authorization: `Bearer ${token}`,
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
  target: UpgradeTarget;
  artifact: ArtifactRef;
  targetPath: string;
}

/**
 * Replace `targetPath` (the running binary by default) with the latest CD
 * build for this platform. Fetchers are injectable; otherwise a token builds
 * authenticated ones.
 */
export async function selfUpgrade(options: {
  targetPath?: string;
  token?: string;
  platform?: NodeJS.Platform;
  arch?: string;
  fetchJson?: FetchJson;
  fetchBytes?: FetchBytes;
  onDownloadProgress?: DownloadProgress;
}): Promise<UpgradeResult> {
  const target = platformTarget(options.platform, options.arch);
  if (!target) {
    throw new Error(
      `no cappu build for ${options.platform ?? process.platform}/${options.arch ?? process.arch}`,
    );
  }
  let fetchJson = options.fetchJson;
  let fetchBytes = options.fetchBytes;
  if (!fetchJson || !fetchBytes) {
    if (!options.token) throw new Error("a GitHub token is required (set GITHUB_TOKEN)");
    ({ fetchJson, fetchBytes } = githubFetchers(options.token));
  }
  const artifact = await latestArtifact(target, fetchJson);
  const bytes = await downloadBinary(
    artifact.id,
    target.binaryName,
    fetchBytes,
    options.onDownloadProgress,
  );
  const targetPath = options.targetPath ?? process.execPath;
  replaceBinary(targetPath, bytes);
  return { target, artifact, targetPath };
}
