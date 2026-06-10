// Package-management domain model. Self-contained: nothing outside
// src/packages/ imports this yet (the cappu.json "packageSources" entries will
// eventually be turned into PackageSource instances).

import { type Brand } from "../brand.ts";

/** Exact maven-style coordinates of one package version. */
export interface Coordinates {
  readonly groupId: string;
  readonly artifactId: string;
  readonly version: string;
}

/** A dependency as declared by a package (before resolution). */
export interface DependencyDeclaration extends Coordinates {
  /** Maven scope; only "compile" and "runtime" propagate transitively. */
  readonly scope?: string;
  /** Optional dependencies do not propagate to consumers. */
  readonly optional?: boolean;
}

export interface PackageMetadata {
  readonly coordinates: Coordinates;
  readonly description?: string;
  readonly dependencies: readonly DependencyDeclaration[];
}

/**
 * One repository packages are searched and resolved from. Implementations:
 * MavenRepositorySource (remote repository layout) and InMemoryPackageSource
 * (tests, local overrides).
 */
export interface PackageSource {
  /** A stable display name (e.g. the repository url). */
  readonly name: string;
  /** Free-text search; implementations may return an empty list if unsupported. */
  search(query: string): Promise<Coordinates[]>;
  /** All published versions of group:artifact, oldest first. */
  listVersions(groupId: string, artifactId: string): Promise<string[]>;
  /** Metadata (including declared dependencies), or undefined if unknown here. */
  getMetadata(coordinates: Coordinates): Promise<PackageMetadata | undefined>;
}

/** "group:artifact:version" - one exact package version. */
export type CoordinateString = Brand<string, "CoordinateString">;
/** "group:artifact" - all versions of a package; the version-conflict key. */
export type PackageKey = Brand<string, "PackageKey">;

export function coordinatesToString(c: Coordinates): CoordinateString {
  return `${c.groupId}:${c.artifactId}:${c.version}` as CoordinateString;
}

/** The conflict key: two versions of the same group:artifact conflict. */
export function packageKey(c: { groupId: string; artifactId: string }): PackageKey {
  return `${c.groupId}:${c.artifactId}` as PackageKey;
}
