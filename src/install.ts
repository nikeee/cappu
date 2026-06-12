// `cappu install`: resolve the cappu.json dependencies section (api +
// implementation, transitively) against the configured packageSources and
// download every jar into the classPath's default lib/classes directory, where
// loadClassPath already picks them up. Print-free; the cli renders the result.
//
// cappu-lock.json (next to cappu.json) pins the outcome: it records the
// dependencies section it was resolved from plus the full resolved package
// set, each with the SHA-256 of the jar that was actually downloaded when the
// lock was written (nikeee/cappu#2) - hashing our own bytes pins the
// artifact; a repository-served checksum file would only prove transport
// integrity against the same server. `cappu install` only respects the lock:
// an existing lock is installed exactly as written and every download is
// verified against its locked hash (a mismatch fails the install); resolution
// runs only to bootstrap a missing lock - or when `cappu add` explicitly asks
// for the lock to be rewritten (updateLock).

import { hash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join } from "node:path";

import {
  type CappuConfig,
  MAVEN_CENTRAL,
  MAVEN_CENTRAL_SEARCH,
  resolveConfigPath,
} from "./config.ts";
import {
  type Coordinates,
  type CoordinateString,
  coordinatesToString,
  matchingVersions,
  MavenRepositorySource,
  type PackageSource,
  type Resolution,
  resolveTransitive,
} from "./packages/index.ts";

/** The configured packageSources as PackageSource instances (Central searchable). */
export function configuredSources(config: CappuConfig): PackageSource[] {
  return config.packageSources.map(url =>
    withVersionListCache(
      new MavenRepositorySource(
        url,
        undefined,
        undefined,
        url === MAVEN_CENTRAL ? MAVEN_CENTRAL_SEARCH : undefined,
      ),
    ),
  );
}

// --- version-list cache --------------------------------------------------------
// listVersions answers (maven-metadata.xml) are cached in the package store
// for a short while: `cappu add` resolves the same artifacts repeatedly and
// the dependency code lenses poll for newer versions. Only non-empty answers
// are cached, the TTL keeps new releases visible, and a read-only or missing
// store silently degrades to live fetches.

const VERSION_CACHE_TTL_MS = 60 * 60 * 1000;

function versionCachePath(
  sourceName: string,
  groupId: string,
  artifactId: string,
): string | undefined {
  const segments = [...groupId.split("."), artifactId];
  if (segments.some(segment => !STORE_SEGMENT.test(segment))) return undefined;
  // one directory per source: different repositories see different versions
  const sourceDir = sourceName.replace(/[^A-Za-z0-9._-]+/g, "_");
  return join(packageStoreDir(), "_metadata", sourceDir, ...segments, "versions.json");
}

export function withVersionListCache(source: PackageSource): PackageSource {
  const listVersions = async (groupId: string, artifactId: string): Promise<string[]> => {
    const cacheFile = versionCachePath(source.name, groupId, artifactId);
    if (cacheFile && existsSync(cacheFile)) {
      try {
        const cached = JSON.parse(readFileSync(cacheFile, "utf8")) as {
          fetchedAt: number;
          versions: string[];
        };
        if (Date.now() - cached.fetchedAt < VERSION_CACHE_TTL_MS) return cached.versions;
      } catch {
        // corrupt cache entry: fall through to a live fetch (and rewrite it)
      }
    }
    const versions = await source.listVersions(groupId, artifactId);
    if (cacheFile && versions.length > 0) {
      try {
        mkdirSync(dirname(cacheFile), { recursive: true });
        writeFileSync(cacheFile, JSON.stringify({ fetchedAt: Date.now(), versions }));
      } catch {
        // a read-only store never fails the lookup
      }
    }
    return versions;
  };
  return {
    name: source.name,
    search: query => source.search(query),
    listVersions,
    getMetadata: coordinates => source.getMetadata(coordinates),
    ...(source.getArtifact ? { getArtifact: c => source.getArtifact!(c) } : {}),
  };
}

export const LOCKFILE_NAME = "cappu-lock.json";

// --- global package store ----------------------------------------------------
// Downloaded jars are cached per user so other projects copy instead of
// re-downloading. It is a CACHE, so it follows XDG_CACHE_HOME (~/.cache/cappu/
// packages), not a config dir; CAPPU_PACKAGE_STORE overrides (tests, CI).
// The layout is maven2's - group segments as directories, then artifact, then
// version: a/b/c/1/c-1.jar vs a/b/c/d/1/d-1.jar - which is what keeps
// "a.b:c@1" and "a.b.c:d@1" apart; flattening group and artifact into one
// dotted path would collide them.

function packageStoreDir(): string {
  return (
    process.env.CAPPU_PACKAGE_STORE ??
    join(process.env.XDG_CACHE_HOME ?? join(homedir(), ".cache"), "cappu", "packages")
  );
}

// One conservative charset for every path segment: anything else (path
// separators, "..", empty segments) bypasses the store entirely rather than
// risking a write outside it.
const STORE_SEGMENT = /^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*$/;

/** The store path for exact coordinates, or undefined for unsafe segments. */
export function storePathFor(coordinates: Coordinates): string | undefined {
  const segments = [...coordinates.groupId.split("."), coordinates.artifactId, coordinates.version];
  if (segments.some(segment => !STORE_SEGMENT.test(segment))) return undefined;
  return join(
    packageStoreDir(),
    ...segments,
    `${coordinates.artifactId}-${coordinates.version}.jar`,
  );
}

interface LockedPackage {
  coordinates: Coordinates;
  source: string;
  /** Hex SHA-256 of the jar downloaded when the lock was written. */
  sha256: string;
}

interface Lockfile {
  version: 2;
  /** The dependencies section this lock was resolved from. */
  roots: CappuConfig["dependencies"];
  /** The resolved set, in install order. */
  packages: LockedPackage[];
}

function sha256Of(bytes: Uint8Array): string {
  return hash("sha256", bytes, "hex");
}

export interface InstallResult {
  /** Jar paths written, in resolution order. */
  installed: string[];
  /** Resolved packages whose source could not provide a jar. */
  noArtifact: string[];
  resolution: Resolution;
  /** The directory the jars were written to. */
  targetDir: string;
  /** True when the locked package set was reused (no resolution ran). */
  fromLock: boolean;
  /** True when the lock was used although cappu.json's dependencies changed. */
  lockStale: boolean;
  /** Locked packages whose downloaded jar did not match its locked SHA-256. */
  integrityFailures: string[];
  /** Packages served from the local package store instead of a download. */
  fromStore: string[];
}

/** "group:artifact" -> version entries of one configuration, as Coordinates. */
function rootsOf(entries: Record<string, string>): Coordinates[] {
  return Object.entries(entries).map(([key, version]) => {
    const [groupId = "", artifactId = ""] = key.split(":");
    return { groupId, artifactId, version };
  });
}

/** Every configured dependency as coordinates (api + implementation alike). */
export function configuredRoots(config: CappuConfig): Coordinates[] {
  // Only the api and implementation configurations exist so far; both are
  // needed at compile time, so install treats them alike.
  return [...rootsOf(config.dependencies.api), ...rootsOf(config.dependencies.implementation)];
}

function lockfilePath(config: CappuConfig): string {
  return join(config.baseDir, LOCKFILE_NAME);
}

function readLockfile(config: CappuConfig): Lockfile | undefined {
  const path = lockfilePath(config);
  if (!existsSync(path)) return undefined;
  try {
    const lock = JSON.parse(readFileSync(path, "utf8")) as Lockfile;
    // Older versions carry no hashes; re-resolving rewrites them as v2.
    return lock.version === 2 && Array.isArray(lock.packages) ? lock : undefined;
  } catch {
    return undefined; // a corrupt lock is ignored, not fatal: install re-resolves
  }
}

/** Whether the lock was resolved from exactly this dependencies section. */
function lockMatches(lock: Lockfile, config: CappuConfig): boolean {
  return JSON.stringify(lock.roots) === JSON.stringify(config.dependencies);
}

async function artifactFrom(
  sources: readonly PackageSource[],
  preferred: string,
  coordinates: Coordinates,
): Promise<{ bytes: Uint8Array; cached: boolean } | undefined> {
  // The store first: a hit needs no network at all. Locked installs verify
  // the SHA-256 afterwards either way, so a poisoned store entry is refused
  // exactly like a tampered download.
  const storePath = storePathFor(coordinates);
  if (storePath && existsSync(storePath)) {
    try {
      return { bytes: readFileSync(storePath), cached: true };
    } catch {
      // unreadable store entry: fall through to the sources
    }
  }
  // Prefer the source that resolved the package; fall back to the others (the
  // configured source list may have changed since the lock was written).
  const ordered = [
    ...sources.filter(s => s.name === preferred),
    ...sources.filter(s => s.name !== preferred),
  ];
  for (const source of ordered) {
    const bytes = await source.getArtifact?.(coordinates);
    if (bytes) {
      if (storePath) {
        try {
          mkdirSync(dirname(storePath), { recursive: true });
          writeFileSync(storePath, bytes);
        } catch {
          // a read-only or full store never fails the install
        }
      }
      return { bytes, cached: false };
    }
  }
  return undefined;
}

export async function installDependencies(
  config: CappuConfig,
  // Injectable for tests; defaults to the configured remote repositories.
  sources: readonly PackageSource[] = configuredSources(config),
  // `cappu add` changed the dependencies section: re-resolve and rewrite the
  // lock instead of consuming the (now outdated) one. onProgress is notified
  // per materialized package (the CLI renders a progress bar from it);
  // installDependencies itself never prints.
  options: {
    updateLock?: boolean;
    onProgress?: (done: number, total: number, current: CoordinateString) => void;
  } = {},
): Promise<InstallResult> {
  const lock = options.updateLock ? undefined : readLockfile(config);
  const fromLock = lock !== undefined;
  const lockStale = lock !== undefined && !lockMatches(lock, config);

  let resolution: Resolution;
  let toInstall: { coordinates: Coordinates; source: string; sha256?: string }[];
  if (lock) {
    resolution = { packages: [], conflicts: [], missing: [] };
    toInstall = lock.packages;
  } else {
    resolution = await resolveTransitive(configuredRoots(config), sources);
    toInstall = resolution.packages.map(p => ({ coordinates: p.coordinates, source: p.source }));
  }

  const targetDir = resolveConfigPath(config, "./lib/classes");
  const installed: string[] = [];
  const noArtifact: string[] = [];
  const integrityFailures: string[] = [];
  const fromStore: string[] = [];
  const locked: LockedPackage[] = [];
  if (toInstall.length > 0) mkdirSync(targetDir, { recursive: true });
  let progressed = 0;
  for (const pkg of toInstall) {
    options.onProgress?.(progressed++, toInstall.length, coordinatesToString(pkg.coordinates));
    const artifact = await artifactFrom(sources, pkg.source, pkg.coordinates);
    if (!artifact) {
      noArtifact.push(coordinatesToString(pkg.coordinates));
      continue;
    }
    const digest = sha256Of(artifact.bytes);
    if (pkg.sha256 !== undefined && pkg.sha256 !== digest) {
      // A locked install must produce the locked bytes: do not write the jar.
      integrityFailures.push(coordinatesToString(pkg.coordinates));
      continue;
    }
    if (artifact.cached) fromStore.push(coordinatesToString(pkg.coordinates));
    const file = join(targetDir, `${pkg.coordinates.artifactId}-${pkg.coordinates.version}.jar`);
    writeFileSync(file, artifact.bytes);
    installed.push(file);
    locked.push({ coordinates: pkg.coordinates, source: pkg.source, sha256: digest });
  }

  if (toInstall.length > 0) {
    options.onProgress?.(toInstall.length, toInstall.length, "" as CoordinateString);
  }

  // The lock pins what was VERIFIABLY materialized, so it is written after the
  // downloads - and only when the whole set arrived.
  if (!fromLock && config.fromFile && resolution.missing.length === 0 && noArtifact.length === 0) {
    const newLock: Lockfile = { version: 2, roots: config.dependencies, packages: locked };
    writeFileSync(lockfilePath(config), `${JSON.stringify(newLock, null, 2)}\n`);
  }
  return {
    installed,
    noArtifact,
    resolution,
    targetDir,
    fromLock,
    lockStale,
    integrityFailures,
    fromStore,
  };
}

export interface PickedVersion {
  version: string;
  /** False when no candidate resolved conflict-free and the newest match won. */
  compatible: boolean;
}

// Trying a candidate means a full transitive resolution (network in the real
// case), so the newest-first search is capped; in practice the first
// candidate usually wins.
const PICK_ATTEMPTS = 5;

/**
 * The version `cappu add` should pick for `key` given an absent or partial
 * spec: the newest published version matching the spec whose transitive
 * resolution - together with everything already configured - is free of
 * version conflicts and missing packages. When no candidate is clean, the
 * newest match is returned flagged incompatible (the caller warns; install's
 * nearest-wins still produces a usable tree).
 */
export async function pickAddVersion(
  config: CappuConfig,
  key: string,
  spec: string | undefined,
  sources: readonly PackageSource[],
): Promise<PickedVersion | undefined> {
  const [groupId = "", artifactId = ""] = key.split(":");
  let published: string[] = [];
  for (const source of sources) {
    published = await source.listVersions(groupId, artifactId);
    if (published.length > 0) break;
  }
  const candidates = matchingVersions(published, spec);
  if (candidates.length === 0) return undefined;

  // The new dependency joins the existing roots (any previous entry for the
  // same key is superseded by the candidate).
  const existing = configuredRoots(config).filter(c => `${c.groupId}:${c.artifactId}` !== key);
  for (const version of candidates.slice(0, PICK_ATTEMPTS)) {
    const resolution = await resolveTransitive(
      [...existing, { groupId, artifactId, version }],
      sources,
    );
    if (resolution.conflicts.length === 0 && resolution.missing.length === 0) {
      return { version, compatible: true };
    }
  }
  return { version: candidates[0]!, compatible: false };
}
