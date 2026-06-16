// Package management API (self-contained; nothing else imports this yet).
// Resolve and search dependencies - including transitive ones - from an
// ordered set of package sources.

export { type License, normalizeLicense, normalizeLicenses } from "./license.ts";
export { MavenRepositorySource, parseMetadataVersions, parsePom } from "./maven.ts";
export {
  InMemoryPackageSource,
  latestVersion,
  type Resolution,
  type ResolvedPackage,
  resolveTransitive,
  searchPackages,
  type VersionConflict,
} from "./resolver.ts";
export { matchesVersionSpec, matchingVersions } from "./versions.ts";
export { coordinatesToString, packageKey } from "./types.ts";
export type {
  CoordinateString,
  Coordinates,
  DependencyDeclaration,
  PackageKey,
  PackageMetadata,
  PackageSource,
} from "./types.ts";
