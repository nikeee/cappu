// Package-management domain model. Self-contained: nothing outside
// src/packages/ imports this yet (the cappu.json "packageSources" entries will
// eventually be turned into PackageSource instances).

import { type Brand } from "../brand.ts";
import { type License, type SpdxId } from "./license.ts";

/** A Maven groupId ("org.apache.commons"), distinct from an artifactId. */
export type GroupId = Brand<string, "GroupId">;
/** A Maven artifactId ("commons-lang3"), distinct from a groupId. */
export type ArtifactId = Brand<string, "ArtifactId">;
/** A Maven version ("2.13.1"), distinct from a version spec/range or a groupId. */
export type Version = Brand<string, "Version">;
/** A Maven dependency scope ("compile", "runtime", "test", "provided", "import"). */
export type MavenScope = Brand<string, "MavenScope">;

/** Exact maven-style coordinates of one package version. */
export interface Coordinates {
  readonly groupId: GroupId;
  readonly artifactId: ArtifactId;
  readonly version: Version;
}

/** A dependency as declared by a package (before resolution). */
export interface DependencyDeclaration extends Coordinates {
  /** Maven scope; only "compile" and "runtime" propagate transitively. */
  readonly scope?: MavenScope;
  /** Optional dependencies do not propagate to consumers. */
  readonly optional?: boolean;
}

export interface PackageMetadata {
  readonly coordinates: Coordinates;
  readonly description?: string;
  readonly dependencies: readonly DependencyDeclaration[];
  /** Licenses as the POM declares them (free text), absent when none. */
  readonly licenses?: readonly License[];
  /** Best-effort SPDX ids the licenses map to (the unmapped ones are dropped). */
  readonly licenseNormalized?: readonly SpdxId[];
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
  /** The package's jar bytes, or undefined if this source cannot provide them. */
  getArtifact?(coordinates: Coordinates): Promise<Uint8Array | undefined>;
}

/** "group:artifact:version" - one exact package version. */
export type CoordinateString = Brand<string, "CoordinateString">;
/** "group:artifact" - all versions of a package; the version-conflict key. */
export type PackageKey = Brand<string, "PackageKey">;

/** Build coordinates from raw strings - the single cast boundary for the ids. */
export function toCoordinates(groupId: string, artifactId: string, version: string): Coordinates {
  return { groupId, artifactId, version } as Coordinates;
}

export function coordinatesToString(c: Coordinates): CoordinateString {
  return `${c.groupId}:${c.artifactId}:${c.version}` as CoordinateString;
}

/** The conflict key: two versions of the same group:artifact conflict. */
export function packageKey(c: { groupId: string; artifactId: string }): PackageKey {
  return `${c.groupId}:${c.artifactId}` as PackageKey;
}
