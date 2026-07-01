// Package management API (self-contained; nothing else imports this yet).
// Resolve and search dependencies - including transitive ones - from an
// ordered set of package sources.

export { type License, normalizeLicense, normalizeLicenses, type SpdxId } from "./license.ts";
export { MavenRepositorySource, parseMetadataVersions, parsePom } from "./maven.ts";
export {
  dependencyPath,
  InMemoryPackageSource,
  latestVersion,
  type Resolution,
  type ResolvedPackage,
  resolveTransitive,
  searchPackages,
  type VersionConflict,
} from "./resolver.ts";
export { matchesVersionSpec, matchingVersions } from "./versions.ts";
export { artifactJarName, coordinatesToString, packageKey, toCoordinates } from "./types.ts";
export type {
  ArtifactId,
  CoordinateString,
  Coordinates,
  DependencyDeclaration,
  GroupId,
  MavenScope,
  PackageKey,
  PackageMetadata,
  PackageSource,
  SearchHit,
  SourceName,
  Version,
} from "./types.ts";
