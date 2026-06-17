// Package packages is the package-management domain model and the sources
// dependencies are searched and resolved from. Port of src/packages/.
package packages

// Branded (named) domain primitives. Unlike the TypeScript original's
// type-only brands - and unlike tsgo's aliases - these are real Go named types,
// so the compiler refuses to mix a GroupId with an ArtifactId. Conversions at
// the producing boundary are explicit (e.g. GroupId(s)).

// GroupID is a Maven groupId ("org.apache.commons"), distinct from an artifactId.
type GroupID string

// ArtifactID is a Maven artifactId ("commons-lang3"), distinct from a groupId.
type ArtifactID string

// Version is a Maven version ("2.13.1"), distinct from a version spec or a groupId.
type Version string

// CoordinateString is "group:artifact:version" - one exact package version.
type CoordinateString string

// PackageKey is "group:artifact" - all versions of a package; the conflict key.
type PackageKey string

// Coordinates are the exact maven-style coordinates of one package version.
type Coordinates struct {
	GroupID    GroupID
	ArtifactID ArtifactID
	Version    Version
}

// String returns "group:artifact:version".
func (c Coordinates) String() CoordinateString {
	return CoordinateString(string(c.GroupID) + ":" + string(c.ArtifactID) + ":" + string(c.Version))
}

// Key is the conflict key: two versions of the same group:artifact conflict.
func (c Coordinates) Key() PackageKey {
	return PackageKey(string(c.GroupID) + ":" + string(c.ArtifactID))
}

// NewCoordinates builds coordinates from raw strings - the single conversion
// boundary for the ids (mirrors toCoordinates in the TS source).
func NewCoordinates(groupID, artifactID, version string) Coordinates {
	return Coordinates{GroupID: GroupID(groupID), ArtifactID: ArtifactID(artifactID), Version: Version(version)}
}

// PackageSource is one repository packages are searched and resolved from.
// Milestone 1 implements only the search-capable Maven Central source; the
// resolution methods (listVersions/getMetadata/getArtifact) arrive with the
// install command.
type PackageSource interface {
	// Name is a stable display name (e.g. the repository url).
	Name() string
	// Search runs a free-text query; an unsupported source returns nil.
	Search(query string) ([]Coordinates, error)
}
