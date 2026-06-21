// Transitive dependency resolution over an ordered list of package sources.
// Maven semantics where they matter: breadth-first "nearest wins" version
// selection (the version closest to a root is used; farther declarations of
// the same group:artifact are recorded as conflicts), and only non-optional
// compile/runtime dependencies propagate.

import {
  type Coordinates,
  coordinatesToString,
  type PackageKey,
  type PackageMetadata,
  type PackageSource,
  packageKey,
  type SearchHit,
  type SourceName,
} from "./types.ts";

export interface ResolvedPackage {
  readonly coordinates: Coordinates;
  readonly metadata: PackageMetadata;
  /** 0 for the requested roots, 1 for their direct dependencies, ... */
  readonly depth: number;
  /** Which package declared it (undefined for a root). */
  readonly requestedBy?: Coordinates;
  /** The source that provided the metadata. */
  readonly source: SourceName;
}

export interface VersionConflict {
  readonly key: PackageKey;
  readonly selected: string;
  readonly rejected: string;
  readonly rejectedBy: Coordinates;
}

export interface Resolution {
  /** Every selected package, in breadth-first discovery order (roots first). */
  readonly packages: ResolvedPackage[];
  /** Same-package version clashes; the nearest (earlier) version won. */
  readonly conflicts: VersionConflict[];
  /** Coordinates no source could provide (with the declaring package, if any). */
  readonly missing: { coordinates: Coordinates; requestedBy?: Coordinates }[];
}

/** Whether a declared dependency propagates to its consumer (Maven rules). */
function propagates(d: { scope?: string; optional?: boolean }): boolean {
  return !d.optional && (d.scope === undefined || d.scope === "compile" || d.scope === "runtime");
}

async function metadataFrom(
  sources: readonly PackageSource[],
  coordinates: Coordinates,
): Promise<{ metadata: PackageMetadata; source: SourceName } | undefined> {
  for (const source of sources) {
    const metadata = await source.getMetadata(coordinates);
    if (metadata) return { metadata, source: source.name };
  }
  return undefined;
}

/**
 * Resolve `roots` and their transitive dependencies against `sources`
 * (consulted in order; the first source that knows a package provides it).
 */
export async function resolveTransitive(
  roots: readonly Coordinates[],
  sources: readonly PackageSource[],
  // Notified once per package as it is about to be fetched: resolution makes a
  // network call per package and the total is unknown until it finishes, so
  // the CLI shows a count-up rather than a bar.
  onResolve?: (coordinates: Coordinates) => void,
): Promise<Resolution> {
  const packages: ResolvedPackage[] = [];
  const conflicts: VersionConflict[] = [];
  const missing: { coordinates: Coordinates; requestedBy?: Coordinates }[] = [];
  // group:artifact -> selected version (nearest wins: BFS reaches near first)
  const selected = new Map<PackageKey, string>();

  let frontier: { coordinates: Coordinates; requestedBy?: Coordinates }[] = roots.map(
    coordinates => ({ coordinates }),
  );
  for (let depth = 0; frontier.length > 0; depth++) {
    const next: typeof frontier = [];
    for (const { coordinates, requestedBy } of frontier) {
      const key = packageKey(coordinates);
      const winner = selected.get(key);
      if (winner !== undefined) {
        if (winner !== coordinates.version) {
          conflicts.push({
            key,
            selected: winner,
            rejected: coordinates.version,
            rejectedBy: requestedBy ?? coordinates,
          });
        }
        continue; // already resolved (or conflicting): never descend twice
      }
      onResolve?.(coordinates);
      const found = await metadataFrom(sources, coordinates);
      if (!found) {
        selected.set(key, coordinates.version); // do not retry / re-report
        missing.push({ coordinates, requestedBy });
        continue;
      }
      selected.set(key, coordinates.version);
      packages.push({
        coordinates,
        metadata: found.metadata,
        depth,
        requestedBy,
        source: found.source,
      });
      for (const dependency of found.metadata.dependencies) {
        if (!propagates(dependency)) continue;
        next.push({
          coordinates: {
            groupId: dependency.groupId,
            artifactId: dependency.artifactId,
            version: dependency.version,
          },
          requestedBy: coordinates,
        });
      }
    }
    frontier = next;
  }
  return { packages, conflicts, missing };
}

/**
 * Search every source and merge the hits: the first source (in order) wins a
 * group:artifact; later duplicates are dropped.
 */
export async function searchPackages(
  query: string,
  sources: readonly PackageSource[],
): Promise<SearchHit[]> {
  const seen = new Set<PackageKey>();
  const result: SearchHit[] = [];
  for (const source of sources) {
    for (const hit of await source.search(query)) {
      const key = packageKey(hit);
      if (seen.has(key)) continue;
      seen.add(key);
      result.push(hit);
    }
  }
  return result;
}

/**
 * The chain of coordinates from a declared root down to `target`, following
 * each resolved package's `requestedBy` edge (nearest-wins records one parent
 * per package). Returns [root, ..., target]; just [target] for a direct
 * dependency, and is cycle-guarded.
 */
export function dependencyPath(
  byKey: ReadonlyMap<string, ResolvedPackage>,
  target: Coordinates,
): Coordinates[] {
  const path: Coordinates[] = [];
  const seen = new Set<string>();
  let current: Coordinates | undefined = target;
  while (current) {
    const key = packageKey(current);
    if (seen.has(key)) break;
    seen.add(key);
    path.unshift(current);
    current = byKey.get(key)?.requestedBy;
  }
  return path;
}

/** The newest published version of group:artifact across the sources. */
export async function latestVersion(
  groupId: string,
  artifactId: string,
  sources: readonly PackageSource[],
): Promise<string | undefined> {
  for (const source of sources) {
    const versions = await source.listVersions(groupId, artifactId);
    if (versions.length > 0) return versions.at(-1);
  }
  return undefined;
}

/** An in-memory source: fixtures in tests, local overrides later. */
export class InMemoryPackageSource implements PackageSource {
  private readonly byKey = new Map<PackageKey, PackageMetadata[]>();
  readonly name: SourceName;

  constructor(name: string, packages: readonly PackageMetadata[]) {
    this.name = name as SourceName;
    for (const pkg of packages) {
      const key = packageKey(pkg.coordinates);
      const list = this.byKey.get(key);
      if (list) list.push(pkg);
      else this.byKey.set(key, [pkg]);
    }
  }

  search(query: string): Promise<SearchHit[]> {
    const q = query.toLowerCase();
    const hits: SearchHit[] = [];
    for (const [key, list] of this.byKey) {
      // the version count is the one extra fact an in-memory source knows
      if (key.toLowerCase().includes(q)) {
        hits.push({ ...list.at(-1)!.coordinates, versionCount: list.length });
      }
    }
    return Promise.resolve(hits);
  }

  listVersions(groupId: string, artifactId: string): Promise<string[]> {
    const list = this.byKey.get(packageKey({ groupId, artifactId })) ?? [];
    return Promise.resolve(list.map(p => p.coordinates.version));
  }

  getMetadata(coordinates: Coordinates): Promise<PackageMetadata | undefined> {
    const list = this.byKey.get(packageKey(coordinates)) ?? [];
    return Promise.resolve(
      list.find(p => coordinatesToString(p.coordinates) === coordinatesToString(coordinates)),
    );
  }
}
