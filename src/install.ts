// `cappu install`: resolve the cappu.json dependencies section (api +
// implementation, transitively) against the configured packageSources and
// download every jar into the classPath's default .cappu/lib/classes directory,
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
import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { basename, dirname, join, relative } from "node:path";

import pLimit from "p-limit";

import { type Brand } from "./brand.ts";
import { cacheDir } from "./cacheDir.ts";
import { materialize as materializeFile } from "./copyStrategy.ts";

/** A hex SHA-256 digest (distinct from the md5/sha1 sidecars publishing emits). */
type Sha256 = Brand<string, "Sha256">;

import {
  type CappuConfig,
  DEFAULT_CLASS_PATH,
  DEFAULT_PROCESSOR_PATH,
  DEFAULT_TEST_CLASS_PATH,
  DEPENDENCY_CONFIGURATIONS,
  MAVEN_CENTRAL,
  MAVEN_CENTRAL_SEARCH,
  resolveConfigPath,
} from "./config.ts";
import {
  type Coordinates,
  type CoordinateString,
  coordinatesToString,
  artifactJarName,
  type License,
  matchingVersions,
  MavenRepositorySource,
  type PackageKey,
  packageKey,
  type PackageMetadata,
  type PackageSource,
  type Resolution,
  resolveTransitive,
  type SourceName,
  toCoordinates,
  type Version,
} from "./packages/index.ts";

/**
 * The configured packageSources as PackageSource instances (Central
 * searchable). `cache: false` skips the on-disk metadata cache, for a fresh
 * resolve that ignores everything cached (e.g. `cappu audit --no-cache`).
 */
export function configuredSources(
  config: CappuConfig,
  options: { cache?: boolean } = {},
): PackageSource[] {
  return config.packageSources.map(url => {
    const source = new MavenRepositorySource(
      url,
      undefined,
      undefined,
      url === MAVEN_CENTRAL ? MAVEN_CENTRAL_SEARCH : undefined,
    );
    return options.cache === false ? source : withMetadataCache(source);
  });
}

// --- metadata cache ------------------------------------------------------------
// Fetched metadata is cached in the package store so re-resolves and repeated
// `cappu add` runs avoid the network:
//  - listVersions (maven-metadata.xml): a short TTL, because "latest" moves.
//  - getMetadata (a released version's effective POM): cached forever, since
//    a published POM is immutable; this is what makes lockfile-less resolves
//    fast (no parent/BOM chain re-fetch).
// Only successful answers are cached; a read-only or missing store silently
// degrades to live fetches.

const VERSION_CACHE_TTL_MS = 60 * 60 * 1000;

// Bumped when PackageMetadata's shape grows, so entries from an older cappu
// (e.g. before licenses were parsed) are ignored and re-fetched rather than
// served stale. v1 was the bare PackageMetadata; v2 adds licenses.
const METADATA_CACHE_VERSION = 2;

// <store>/_metadata/<source>/<group dirs>/<artifact>[/<version>]/<file>, or
// undefined when a segment is not store-safe (then caching is skipped).
function metadataCachePath(
  sourceName: string,
  segments: readonly string[],
  file: string,
): string | undefined {
  if (segments.some(segment => !STORE_SEGMENT.test(segment))) return undefined;
  // one directory per source: different repositories see different metadata
  const sourceDir = sourceName.replace(/[^A-Za-z0-9._-]+/g, "_");
  return join(packageStoreDir(), "_metadata", sourceDir, ...segments, file);
}

export function withMetadataCache(source: PackageSource): PackageSource {
  const listVersions = async (groupId: string, artifactId: string): Promise<string[]> => {
    const cacheFile = metadataCachePath(
      source.name,
      [...groupId.split("."), artifactId],
      "versions.json",
    );
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

  const getMetadata = async (coordinates: Coordinates): Promise<PackageMetadata | undefined> => {
    const cacheFile = metadataCachePath(
      source.name,
      [...coordinates.groupId.split("."), coordinates.artifactId, coordinates.version],
      "metadata.json",
    );
    if (cacheFile && existsSync(cacheFile)) {
      try {
        const cached = JSON.parse(readFileSync(cacheFile, "utf8")) as {
          v?: number;
          metadata?: PackageMetadata;
          pomSha256?: Sha256;
        };
        // older/unknown schema (e.g. a pre-licenses entry): re-fetch and rewrite
        if (cached.v === METADATA_CACHE_VERSION && cached.metadata) return cached.metadata;
      } catch {
        // corrupt entry: fall through to a live fetch (and rewrite it)
      }
    }
    const metadata = await source.getMetadata(coordinates);
    if (cacheFile && metadata) {
      try {
        mkdirSync(dirname(cacheFile), { recursive: true });
        // Persist the raw POM next to its metadata and record its SHA-256, so a
        // future `cappu cache verify` can check the cached POM on disk.
        const pom = await source.getPom?.(coordinates);
        let pomSha256: Sha256 | undefined;
        if (pom) {
          pomSha256 = sha256Of(pom);
          writeFileSync(
            join(dirname(cacheFile), `${coordinates.artifactId}-${coordinates.version}.pom`),
            pom,
          );
        }
        writeFileSync(
          cacheFile,
          JSON.stringify({
            v: METADATA_CACHE_VERSION,
            metadata,
            ...(pomSha256 ? { pomSha256 } : {}),
          }),
        );
      } catch {
        // a read-only store never fails the lookup
      }
    }
    return metadata;
  };

  return {
    name: source.name,
    search: query => source.search(query),
    listVersions,
    getMetadata,
    ...(source.getArtifact ? { getArtifact: c => source.getArtifact!(c) } : {}),
    ...(source.getPom ? { getPom: c => source.getPom!(c) } : {}),
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
  return cacheDir("packages", process.env.CAPPU_PACKAGE_STORE);
}

// One conservative charset for every path segment: anything else (path
// separators, "..", empty segments) bypasses the store entirely rather than
// risking a write outside it.
const STORE_SEGMENT = /^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*$/;

/** The store path for exact coordinates, or undefined for unsafe segments. */
export function storePathFor(coordinates: Coordinates): string | undefined {
  const segments = [...coordinates.groupId.split("."), coordinates.artifactId, coordinates.version];
  const checked = coordinates.classifier ? [...segments, coordinates.classifier] : segments;
  if (checked.some(segment => !STORE_SEGMENT.test(segment))) return undefined;
  return join(packageStoreDir(), ...segments, artifactJarName(coordinates));
}

interface LockedPackage {
  coordinates: Coordinates;
  source: SourceName;
  /** Hex SHA-256 of the jar downloaded when the lock was written. */
  sha256: Sha256;
  /** The package's licenses as the POM declares them (raw, not normalized). */
  licenses?: readonly License[];
}

interface Lockfile {
  version: 2;
  /** The dependencies section this lock was resolved from. */
  roots: CappuConfig["dependencies"];
  /** The resolved compile set (api + implementation), in install order. */
  packages: LockedPackage[];
  /** The separately resolved annotationProcessor set (absent when none). */
  processorPackages?: LockedPackage[];
  /** The separately resolved testImplementation set (absent when none). */
  testPackages?: LockedPackage[];
}

function sha256Of(bytes: Uint8Array): Sha256 {
  return hash("sha256", bytes, "hex") as Sha256;
}

// How many jars to download/verify at once. Bounded so a large tree does not
// open hundreds of sockets; the network, not the CPU, is the limit here.
const DOWNLOAD_CONCURRENCY = 6;

// Which lib directory each locked configuration installs into.
const LOCK_TARGETS = [
  { key: "packages", dir: DEFAULT_CLASS_PATH },
  { key: "processorPackages", dir: DEFAULT_PROCESSOR_PATH },
  { key: "testPackages", dir: DEFAULT_TEST_CLASS_PATH },
] as const satisfies { key: keyof Lockfile; dir: string }[];

export interface VerifyResult {
  /** False when there is no cappu-lock.json to verify against. */
  fromLock: boolean;
  /** Coordinate strings whose installed jar matches its locked SHA-256. */
  ok: string[];
  /** Installed, but the bytes do not match the lock (tampered or corrupt). */
  modified: string[];
  /** Locked but absent from the lib directory. */
  missing: string[];
}

/**
 * Every coordinate the lockfile pins (compile + processor + test sets), or
 * undefined when there is no lockfile. The resolved truth `cappu audit` scans.
 */
export function lockedCoordinates(config: CappuConfig): Coordinates[] | undefined {
  const lock = readLockfile(config);
  if (!lock) return undefined;
  return LOCK_TARGETS.flatMap(({ key }) => (lock[key] ?? []).map(p => p.coordinates));
}

/**
 * Check the jars currently in the lib directories against the SHA-256 sums in
 * cappu-lock.json. Read-only: nothing is downloaded, written or removed.
 */
export function verifyInstalled(config: CappuConfig): VerifyResult {
  const lock = readLockfile(config);
  if (!lock) return { fromLock: false, ok: [], modified: [], missing: [] };
  const result: VerifyResult = { fromLock: true, ok: [], modified: [], missing: [] };
  for (const { key, dir } of LOCK_TARGETS) {
    for (const pkg of lock[key] ?? []) {
      const id = coordinatesToString(pkg.coordinates);
      const file = join(
        resolveConfigPath(config, dir),
        `${pkg.coordinates.artifactId}-${pkg.coordinates.version}.jar`,
      );
      if (!existsSync(file)) result.missing.push(id);
      else if (sha256Of(readFileSync(file)) === pkg.sha256) result.ok.push(id);
      else result.modified.push(id);
    }
  }
  return result;
}

export interface CacheVerifyResult {
  /** Cache files whose bytes match their recorded hash (path relative to the store). */
  ok: string[];
  /** Present but the bytes do not match the recorded hash (corrupt or tampered). */
  modified: string[];
  /** A hash is recorded but the file it covers is gone. */
  missing: string[];
}

/**
 * Check every cached artifact in the package store against the hash recorded
 * beside it: each jar against its `.sha256` sidecar, each cached POM against the
 * `pomSha256` in its metadata.json. Read-only: nothing is downloaded or removed.
 * Files with no recorded hash are skipped - there is nothing to check them
 * against (e.g. jars cached before hashing was added).
 */
export function verifyCache(): CacheVerifyResult {
  const root = packageStoreDir();
  const result: CacheVerifyResult = { ok: [], modified: [], missing: [] };
  if (!existsSync(root)) return result;
  const rel = (full: string): string => relative(root, full);
  for (const entry of readdirSync(root, { recursive: true }) as string[]) {
    const full = join(root, entry);
    if (entry.endsWith(".jar.sha256")) {
      // an orphaned sidecar: its jar was deleted out from under the cache
      const jar = full.slice(0, -".sha256".length);
      if (!existsSync(jar)) result.missing.push(rel(jar));
    } else if (entry.endsWith(".jar")) {
      const sidecar = `${full}.sha256`;
      if (!existsSync(sidecar)) continue; // unverifiable: no recorded hash
      const want = readFileSync(sidecar, "utf8");
      (sha256Of(readFileSync(full)) === want ? result.ok : result.modified).push(rel(full));
    } else if (entry.endsWith("metadata.json")) {
      let cached: { pomSha256?: Sha256 };
      try {
        cached = JSON.parse(readFileSync(full, "utf8"));
      } catch {
        continue; // a corrupt cache entry is not a verification failure
      }
      if (!cached.pomSha256) continue;
      // The POM sits beside its metadata.json; the version dir and artifact dir
      // name it (<artifact>-<version>.pom), so the lookup needs no JSON shape.
      const dir = dirname(full);
      const pom = join(dir, `${basename(dirname(dir))}-${basename(dir)}.pom`);
      if (!existsSync(pom)) result.missing.push(rel(pom));
      else
        (sha256Of(readFileSync(pom)) === cached.pomSha256 ? result.ok : result.modified).push(
          rel(pom),
        );
    }
  }
  return result;
}

export interface InstallResult {
  /** Jar paths written, in resolution order. */
  installed: string[];
  /** The written jar paths split by configuration group, for per-category summaries. */
  installedByCategory: { compile: string[]; processor: string[]; test: string[] };
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

/** Plain byte-order string comparison (what the Go build's sorted maps use). */
export const compareStrings = (a: string, b: string): number => (a < b ? -1 : a > b ? 1 : 0);

/**
 * "group:artifact" -> version entries of one configuration, as Coordinates.
 * Sorted by key so resolution order (and every listing derived from it) is
 * deterministic and identical to the Go build's sorted map iteration.
 */
function rootsOf(entries: Record<string, string>): Coordinates[] {
  return Object.entries(entries)
    .sort(([a], [b]) => compareStrings(a, b))
    .map(([key, version]) => {
      const [groupId = "", artifactId = ""] = key.split(":");
      return toCoordinates(groupId, artifactId, version);
    });
}

/** Every COMPILE dependency as coordinates (api + implementation alike). */
export function configuredRoots(config: CappuConfig): Coordinates[] {
  // api and implementation are both needed at compile time; the
  // annotationProcessor configuration deliberately stays out (it resolves
  // independently - processor classpaths must not version-mediate against
  // the app classpath).
  return [...rootsOf(config.dependencies.api), ...rootsOf(config.dependencies.implementation)];
}

/** The annotationProcessor configuration's roots (resolved independently). */
export function processorRoots(config: CappuConfig): Coordinates[] {
  return rootsOf(config.dependencies.annotationProcessor);
}

/** The testImplementation configuration's roots (resolved independently). */
export function testRoots(config: CappuConfig): Coordinates[] {
  return rootsOf(config.dependencies.testImplementation);
}

/** The declared roots of one named configuration, as coordinates. */
export function configurationRoots(
  config: CappuConfig,
  configuration: UpdateConfiguration,
): Coordinates[] {
  return rootsOf(config.dependencies[configuration]);
}

// The dependency configurations `cappu update` walks.
const UPDATE_CONFIGS = DEPENDENCY_CONFIGURATIONS;
export type UpdateConfiguration = (typeof UPDATE_CONFIGS)[number];

export interface DependencyBump {
  configuration: UpdateConfiguration;
  key: PackageKey;
  from: string;
  to: string;
}

// Pre-release qualifiers we never bump to automatically (Maven conventions);
// `cappu update` only moves to stable releases.
const PRERELEASE = /(?:^|[-._])(?:alpha|beta|rc|cr|snapshot|milestone|m\d+|preview|ea|dev)/i;
const isStableVersion = (version: string): boolean => !PRERELEASE.test(version);

// The leading (major) component, for the no-major-bump rule below.
const majorOf = (version: string): string => version.split(/[.+-]/, 1)[0]!;

const UPDATE_ATTEMPTS = 5;

/**
 * The newest STABLE version each declared dependency can move to **within its
 * current major version** (no major-version bumps - those are breaking and are
 * left to the user) while keeping its configuration's transitive graph
 * conflict-free and complete; the re-resolved tree then carries the matching
 * transitive versions into the lock. api and implementation share the compile
 * graph; annotationProcessor and testImplementation each resolve independently
 * (as install does). Bumps are applied to an in-memory working set as they are
 * chosen, so later checks see the earlier ones; a dependency whose pinned
 * version is not in the published list is left alone (its ordering relative to
 * "newer" is unknown).
 */
export async function planUpdates(
  config: CappuConfig,
  sources: readonly PackageSource[] = configuredSources(config),
): Promise<DependencyBump[]> {
  const working: Record<UpdateConfiguration, Record<string, string>> = {
    api: { ...config.dependencies.api },
    implementation: { ...config.dependencies.implementation },
    annotationProcessor: { ...config.dependencies.annotationProcessor },
    testImplementation: { ...config.dependencies.testImplementation },
  };
  const graphRoots = (configuration: UpdateConfiguration): Coordinates[] =>
    configuration === "annotationProcessor"
      ? rootsOf(working.annotationProcessor)
      : configuration === "testImplementation"
        ? rootsOf(working.testImplementation)
        : [...rootsOf(working.api), ...rootsOf(working.implementation)];

  const bumps: DependencyBump[] = [];
  for (const configuration of UPDATE_CONFIGS) {
    for (const [key, current] of Object.entries(working[configuration])) {
      const [groupId = "", artifactId = ""] = key.split(":");
      let published: string[] = [];
      for (const source of sources) {
        published = await source.listVersions(groupId, artifactId);
        if (published.length > 0) break;
      }
      const order = matchingVersions(published); // newest (publish order) first
      const currentIndex = order.indexOf(current);
      if (currentIndex < 0) continue; // unknown ordering: do not risk a downgrade
      // newer, stable, and the same major (a 2.x dep never auto-bumps to 3.x).
      const newer = order
        .slice(0, currentIndex)
        .filter(v => isStableVersion(v) && majorOf(v) === majorOf(current));

      for (const candidate of newer.slice(0, UPDATE_ATTEMPTS)) {
        const roots = graphRoots(configuration).map(c =>
          `${c.groupId}:${c.artifactId}` === key ? { ...c, version: candidate as Version } : c,
        );
        const resolution = await resolveTransitive(roots, sources);
        if (resolution.conflicts.length === 0 && resolution.missing.length === 0) {
          working[configuration][key] = candidate;
          bumps.push({ configuration, key: key as PackageKey, from: current, to: candidate });
          break;
        }
      }
    }
  }
  return bumps;
}

export interface OutdatedDependency {
  configuration: UpdateConfiguration;
  key: PackageKey;
  current: string;
  /** Newest stable within the current major (the safe `cappu update` target). */
  wanted?: string;
  /** Newest stable overall - a major bump when it differs from `wanted`. */
  latest?: string;
}

/**
 * Every declared dependency that has a newer published stable version, with the
 * newest in-major version (`wanted`, what `cappu update` would move to) and the
 * newest overall (`latest`, possibly a major bump). A dependency whose pinned
 * version is not in the published list, or that is already newest, is omitted.
 * Read-only - never writes the config or lock.
 */
export async function planOutdated(
  config: CappuConfig,
  sources: readonly PackageSource[] = configuredSources(config),
): Promise<OutdatedDependency[]> {
  const sections: [UpdateConfiguration, Record<string, string>][] = [
    ["api", config.dependencies.api ?? {}],
    ["implementation", config.dependencies.implementation ?? {}],
    ["annotationProcessor", config.dependencies.annotationProcessor ?? {}],
    ["testImplementation", config.dependencies.testImplementation ?? {}],
  ];
  const out: OutdatedDependency[] = [];
  for (const [configuration, deps] of sections) {
    for (const [key, current] of Object.entries(deps)) {
      const [groupId = "", artifactId = ""] = key.split(":");
      let published: string[] = [];
      for (const source of sources) {
        published = await source.listVersions(groupId, artifactId);
        if (published.length > 0) break;
      }
      const order = matchingVersions(published); // newest (publish order) first
      const currentIndex = order.indexOf(current);
      if (currentIndex < 0) continue; // unknown ordering: do not guess
      // "newer" = strictly ahead of current in the newest-first order and stable.
      const newer = order.slice(0, currentIndex).filter(isStableVersion);
      if (newer.length === 0) continue;
      const latest = newer[0];
      const wanted = newer.find(v => majorOf(v) === majorOf(current));
      out.push({
        configuration,
        key: key as PackageKey,
        current,
        ...(wanted !== undefined && wanted !== current ? { wanted } : {}),
        ...(latest !== undefined && latest !== current ? { latest } : {}),
      });
    }
  }
  return out;
}

function lockfilePath(config: CappuConfig): string {
  return join(config.baseDir, LOCKFILE_NAME);
}

/** How `group:artifact` relates to the current project (for `cappu show`). */
export interface ProjectContext {
  /** The cappu.json configuration(s) declaring it (e.g. ["implementation"]). */
  readonly configurations: readonly string[];
  /** The version range declared in cappu.json, if any. */
  readonly declared?: string;
  /** The exact version pinned in cappu-lock.json, if locked. */
  readonly installed?: string;
}

/** Where (if anywhere) this project depends on `group:artifact`, and at what version. */
export function projectContext(config: CappuConfig, key: string): ProjectContext {
  const configurations: string[] = [];
  let declared: string | undefined;
  for (const name of DEPENDENCY_CONFIGURATIONS) {
    const version = config.dependencies[name][key];
    if (version !== undefined) {
      configurations.push(name);
      declared ??= version;
    }
  }
  let installed: string | undefined;
  const lock = readLockfile(config);
  if (lock) {
    const all = [...lock.packages, ...(lock.processorPackages ?? []), ...(lock.testPackages ?? [])];
    installed = all.find(p => packageKey(p.coordinates) === key)?.coordinates.version;
  }
  return {
    configurations,
    ...(declared !== undefined ? { declared } : {}),
    ...(installed !== undefined ? { installed } : {}),
  };
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

/**
 * Whether the lock was resolved from exactly this dependencies section.
 * Empty configurations are dropped before comparing, so locks written before
 * a new configuration existed (e.g. annotationProcessor) do not all turn
 * stale when the schema grows.
 */
function lockMatches(lock: Lockfile, config: CappuConfig): boolean {
  // Key order is irrelevant on both levels (the Go build's map marshaling
  // sorts keys); only the actual entries decide staleness.
  const normalized = (roots: CappuConfig["dependencies"]): string =>
    JSON.stringify(
      Object.fromEntries(
        Object.entries(roots ?? {})
          .filter(([, entries]) => Object.keys(entries).length > 0)
          .sort(([a], [b]) => compareStrings(a, b))
          .map(([name, entries]) => [
            name,
            Object.fromEntries(Object.entries(entries).sort(([a], [b]) => compareStrings(a, b))),
          ]),
      ),
    );
  return normalized(lock.roots) === normalized(config.dependencies);
}

/**
 * Whether the lockfile is consistent with cappu.json, for `cappu install
 * --locked` (the CI guarantee, like `uv --locked` / `cargo --locked`). Checked
 * before any download so a stale or missing lock fails fast without touching the
 * network: a lock that was resolved from a different dependencies section is
 * `stale`; declared dependencies with no lock at all are `missing`.
 */
export function checkLocked(config: CappuConfig): { ok: true } | { ok: false; reason: string } {
  const lock = readLockfile(config);
  if (lock === undefined) {
    const declared = Object.values(config.dependencies).some(m => Object.keys(m).length > 0);
    return declared
      ? { ok: false, reason: "no cappu-lock.json found; run `cappu install` to create it" }
      : { ok: true };
  }
  return lockMatches(lock, config)
    ? { ok: true }
    : {
        ok: false,
        reason: "cappu.json and cappu-lock.json disagree; run `cappu install` to re-resolve",
      };
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
          // A sidecar SHA-256 of the cached jar, so a future `cappu cache verify`
          // can check the store on disk without any project lockfile.
          writeFileSync(`${storePath}.sha256`, sha256Of(bytes));
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
    // Notified once per package while resolving (no lockfile): the set is not
    // yet known, so the CLI shows a count-up indicator.
    onResolve?: (current: CoordinateString) => void;
  } = {},
): Promise<InstallResult> {
  const lock = options.updateLock ? undefined : readLockfile(config);
  const fromLock = lock !== undefined;
  const lockStale = lock !== undefined && !lockMatches(lock, config);

  type PendingPackage = {
    coordinates: Coordinates;
    source: SourceName;
    sha256?: Sha256;
    licenses?: readonly License[];
  };
  const pending = (resolved: Resolution): PendingPackage[] =>
    resolved.packages.map(p => ({
      coordinates: p.coordinates,
      source: p.source,
      ...(p.metadata.licenses ? { licenses: p.metadata.licenses } : {}),
    }));

  // The compile set (api + implementation) and the annotationProcessor set
  // resolve INDEPENDENTLY and install to different directories - a processor's
  // transitive closure must not version-mediate against the app's.
  const NONE: Resolution = { packages: [], conflicts: [], missing: [] };
  let resolution: Resolution;
  let toInstall: PendingPackage[];
  let processorInstall: PendingPackage[];
  let testInstall: PendingPackage[];
  if (lock) {
    resolution = NONE;
    toInstall = lock.packages;
    processorInstall = lock.processorPackages ?? [];
    testInstall = lock.testPackages ?? [];
  } else {
    const onResolve = options.onResolve
      ? (c: Coordinates) => options.onResolve!(coordinatesToString(c))
      : undefined;
    const main = await resolveTransitive(configuredRoots(config), sources, onResolve);
    const processors =
      processorRoots(config).length > 0
        ? await resolveTransitive(processorRoots(config), sources, onResolve)
        : NONE;
    const tests =
      testRoots(config).length > 0
        ? await resolveTransitive(testRoots(config), sources, onResolve)
        : NONE;
    resolution = {
      packages: [...main.packages, ...processors.packages, ...tests.packages],
      conflicts: [...main.conflicts, ...processors.conflicts, ...tests.conflicts],
      missing: [...main.missing, ...processors.missing, ...tests.missing],
    };
    toInstall = pending(main);
    processorInstall = pending(processors);
    testInstall = pending(tests);
  }

  const targetDir = resolveConfigPath(config, DEFAULT_CLASS_PATH);
  const installed: string[] = [];
  const noArtifact: string[] = [];
  const integrityFailures: string[] = [];
  const fromStore: string[] = [];
  const total = toInstall.length + processorInstall.length + testInstall.length;
  let progressed = 0;

  interface Outcome {
    locked?: LockedPackage;
    installed?: string;
    noArtifact?: string;
    integrity?: string;
    fromStore?: string;
  }

  // Download, verify and write one package. Each is independent, so the set is
  // fetched with bounded concurrency below (the lockfile - or a completed
  // resolution - gives the whole set up front, so every download starts at once
  // instead of one-at-a-time).
  const fetchOne = async (pkg: PendingPackage, dir: string): Promise<Outcome> => {
    const id = coordinatesToString(pkg.coordinates);
    const artifact = await artifactFrom(sources, pkg.source, pkg.coordinates);
    options.onProgress?.(++progressed, total, id);
    if (!artifact) return { noArtifact: id };
    const digest = sha256Of(artifact.bytes);
    if (pkg.sha256 !== undefined && pkg.sha256 !== digest) {
      // A locked install must produce the locked bytes: do not write the jar,
      // and evict the bad copy from the global store (it is store-first, so a
      // poisoned entry would otherwise re-fail every install until a manual
      // `cappu cache clean`).
      const stored = storePathFor(pkg.coordinates);
      if (stored) {
        rmSync(stored, { force: true });
        rmSync(`${stored}.sha256`, { force: true });
      }
      return { integrity: id };
    }
    const file = join(dir, `${pkg.coordinates.artifactId}-${pkg.coordinates.version}.jar`);
    // CoW-clone/hardlink from the store instead of writing a second copy
    // (nikeee/cappu#35). When the store entry is missing (read-only or full
    // store), fall back to writing the bytes we already have in hand.
    const stored = storePathFor(pkg.coordinates);
    if (stored && existsSync(stored)) materializeFile(stored, file);
    else writeFileSync(file, artifact.bytes);
    return {
      locked: {
        coordinates: pkg.coordinates,
        source: pkg.source,
        sha256: digest,
        ...(pkg.licenses ? { licenses: pkg.licenses } : {}),
      },
      installed: file,
      ...(artifact.cached ? { fromStore: id } : {}),
    };
  };

  // One limiter shared across all three sets: they download concurrently
  // (Promise.all below), so a per-set limit would let the real ceiling reach
  // 3*DOWNLOAD_CONCURRENCY and hammer the registry into rate-limiting it. The
  // Go port serializes the sets to the same effect. (nikeee/cappu#31.)
  const limit = pLimit(DOWNLOAD_CONCURRENCY);
  const materialize = (set: PendingPackage[], dir: string): Promise<Outcome[]> => {
    if (set.length === 0) return Promise.resolve([]);
    mkdirSync(dir, { recursive: true });
    return Promise.all(set.map(pkg => limit(() => fetchOne(pkg, dir))));
  };

  // The three sets download concurrently; their outcomes are assembled in a
  // fixed group order (compile, processor, test) and input order within each,
  // so the lock and the result lists stay deterministic whatever finishes first.
  const [mainOut, procOut, testOut] = await Promise.all([
    materialize(toInstall, targetDir),
    materialize(processorInstall, resolveConfigPath(config, DEFAULT_PROCESSOR_PATH)),
    materialize(testInstall, resolveConfigPath(config, DEFAULT_TEST_CLASS_PATH)),
  ]);
  const assemble = (outcomes: Outcome[]): { locked: LockedPackage[]; installed: string[] } => {
    const locked: LockedPackage[] = [];
    const groupInstalled: string[] = [];
    for (const o of outcomes) {
      if (o.noArtifact) noArtifact.push(o.noArtifact);
      if (o.integrity) integrityFailures.push(o.integrity);
      if (o.fromStore) fromStore.push(o.fromStore);
      if (o.installed) {
        installed.push(o.installed);
        groupInstalled.push(o.installed);
      }
      if (o.locked) locked.push(o.locked);
    }
    return { locked, installed: groupInstalled };
  };
  const main = assemble(mainOut);
  const processors = assemble(procOut);
  const tests = assemble(testOut);
  const locked = main.locked;
  const lockedProcessors = processors.locked;
  const lockedTests = tests.locked;

  if (total > 0) {
    options.onProgress?.(total, total, "" as CoordinateString);
  }

  // Sort each set by coordinate so the lock is deterministic regardless of
  // install/download order, keeping diffs minimal across runs.
  const byCoordinate = (a: LockedPackage, b: LockedPackage) =>
    compareStrings(coordinatesToString(a.coordinates), coordinatesToString(b.coordinates));
  // Inner keys sorted like the Go build's map marshaling, so the two builds
  // write byte-identical roots for the same cappu.json.
  const sortedSection = (entries: Record<string, string>) =>
    Object.fromEntries(Object.entries(entries).sort(([a], [b]) => compareStrings(a, b)));

  // The lock pins what was VERIFIABLY materialized, so it is written after the
  // downloads - and only when the whole set arrived.
  if (!fromLock && config.fromFile && resolution.missing.length === 0 && noArtifact.length === 0) {
    const newLock: Lockfile = {
      version: 2,
      roots: {
        api: sortedSection(config.dependencies.api),
        implementation: sortedSection(config.dependencies.implementation),
        annotationProcessor: sortedSection(config.dependencies.annotationProcessor),
        testImplementation: sortedSection(config.dependencies.testImplementation),
      },
      packages: locked.toSorted(byCoordinate),
      ...(lockedProcessors.length > 0
        ? { processorPackages: lockedProcessors.toSorted(byCoordinate) }
        : {}),
      ...(lockedTests.length > 0 ? { testPackages: lockedTests.toSorted(byCoordinate) } : {}),
    };
    writeFileSync(lockfilePath(config), `${JSON.stringify(newLock, null, 2)}\n`);
  }
  return {
    installed,
    installedByCategory: {
      compile: main.installed,
      processor: processors.installed,
      test: tests.installed,
    },
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
      [...existing, toCoordinates(groupId, artifactId, version)],
      sources,
    );
    if (resolution.conflicts.length === 0 && resolution.missing.length === 0) {
      return { version, compatible: true };
    }
  }
  return { version: candidates[0]!, compatible: false };
}
