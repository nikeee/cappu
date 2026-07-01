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

// SourceName is a package source's stable display name (e.g. its repository url).
type SourceName string

// Classifier is a Maven artifact classifier ("runtime", "sources", "javadoc").
type Classifier string

// Coordinates are the exact maven-style coordinates of one package version.
type Coordinates struct {
	GroupID    GroupID
	ArtifactID ArtifactID
	Version    Version
	// Classifier selects a qualified artifact of the same GAV; "" when absent.
	Classifier Classifier
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

// WithClassifier returns a copy of these coordinates carrying the given
// classifier (Go has no optional param; mirrors toCoordinates's 4th arg in TS).
func (c Coordinates) WithClassifier(classifier string) Coordinates {
	c.Classifier = Classifier(classifier)
	return c
}

// ArtifactJarName is the jar filename: "artifact-version[-classifier].jar".
func (c Coordinates) ArtifactJarName() string {
	suffix := ""
	if c.Classifier != "" {
		suffix = "-" + string(c.Classifier)
	}
	return string(c.ArtifactID) + "-" + string(c.Version) + suffix + ".jar"
}

// SearchHit is a search match: coordinates plus whatever extra facts the index
// reported. Port of SearchHit in src/packages/types.ts. The optional extras use
// pointers / "" so an absent field stays distinguishable and is omitted from
// --json output (matching JSON.stringify dropping undefined keys).
type SearchHit struct {
	Coordinates
	// Packaging is the Maven packaging ("jar", "pom", "aar", ...), "" if unknown.
	Packaging string
	// VersionCount is the total number of published versions, nil if unknown.
	VersionCount *int
	// LastUpdated is the last-published time as epoch ms, nil if unknown.
	LastUpdated *int64
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
	Coordinates Coordinates
	Description string
	// Homepage is the project homepage (POM <url>), empty when not declared.
	Homepage string
	// ScmURL is the source repository url (POM <scm><url>/<connection>), empty when none.
	ScmURL       string
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
	Name() SourceName
	// Search runs a free-text query; an unsupported source returns nil.
	Search(query string) ([]SearchHit, error)
	// ListVersions returns all published versions of group:artifact, oldest first.
	ListVersions(groupID, artifactID string) ([]string, error)
	// GetMetadata returns the effective metadata, or nil if unknown here.
	GetMetadata(c Coordinates) (*PackageMetadata, error)
	// GetArtifact returns the package's jar bytes, or nil if unavailable here.
	GetArtifact(c Coordinates) ([]byte, error)
	// GetPom returns the package's own POM bytes (not the merged parent chain),
	// or nil if unavailable here.
	GetPom(c Coordinates) ([]byte, error)
}
