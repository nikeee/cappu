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

// MavenScope is a Maven dependency scope ("compile", "runtime", "test", ...).
type MavenScope string

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

// DependencyDeclaration is a dependency as declared by a package (before
// resolution).
type DependencyDeclaration struct {
	Coordinates
	// Scope is the Maven scope; only "compile" and "runtime" propagate.
	Scope MavenScope
	// Optional dependencies do not propagate to consumers.
	Optional bool
}

// PackageMetadata is the effective view of one package version: its declared
// dependencies and licenses.
type PackageMetadata struct {
	Coordinates  Coordinates
	Description  string
	Dependencies []DependencyDeclaration
	// Licenses as the POM declares them (free text), empty when none.
	Licenses []License
	// LicenseNormalized are the best-effort SPDX ids the licenses map to.
	LicenseNormalized []SpdxID
}

// PackageSource is one repository packages are searched and resolved from.
// Implementations: MavenRepositorySource (remote repository layout) and
// InMemoryPackageSource (tests, local overrides).
type PackageSource interface {
	// Name is a stable display name (e.g. the repository url).
	Name() string
	// Search runs a free-text query; an unsupported source returns nil.
	Search(query string) ([]Coordinates, error)
	// ListVersions returns all published versions of group:artifact, oldest first.
	ListVersions(groupID, artifactID string) ([]string, error)
	// GetMetadata returns the effective metadata, or nil if unknown here.
	GetMetadata(c Coordinates) (*PackageMetadata, error)
	// GetArtifact returns the package's jar bytes, or nil if unavailable here.
	GetArtifact(c Coordinates) ([]byte, error)
}
