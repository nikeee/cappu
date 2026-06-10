// Package management API (self-contained; nothing else imports this yet).
// Resolve and search dependencies - including transitive ones - from an
// ordered set of package sources.

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
export type {
  Coordinates,
  DependencyDeclaration,
  PackageMetadata,
  PackageSource,
} from "./types.ts";
