// Package packages is the package-management domain model and the sources
// dependencies are searched and resolved from. Port of src/packages/.
package packages

import "strings"

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
	// Extension is "pom" for a <type>pom</type> dependency (metadata only, no
	// jar) and "" for a jar. Like the classifier it is part of the identity:
	// Maven keeps g:a:pom and g:a:jar as distinct nodes.
	Extension string
}

// String returns "group:artifact:version".
func (c Coordinates) String() CoordinateString {
	return CoordinateString(string(c.GroupID) + ":" + string(c.ArtifactID) + ":" + string(c.Version))
}

// Key is the conflict key: two versions of the same group:artifact conflict.
// A classified artifact ("g:a:classifier") or a pom-only one ("g:a:pom") is
// its own key, like Maven's g:a:type:classifier - the base jar, a
// native-classifier jar and a pom-typed edge coexist.
func (c Coordinates) Key() PackageKey {
	return PackageKey(string(c.GroupID) + ":" + string(c.ArtifactID) + c.qualifier())
}

// Label is the display form: "group:artifact:version", with ":classifier"
// and/or ":pom" appended. String stays the GAV (the POM identity).
func (c Coordinates) Label() string {
	return string(c.String()) + c.qualifier()
}

func (c Coordinates) qualifier() string {
	q := ""
	if c.Classifier != "" {
		q += ":" + string(c.Classifier)
	}
	if c.Extension != "" {
		q += ":" + c.Extension
	}
	return q
}

// PomOnly reports a <type>pom</type> dependency: it contributes its transitive
// dependencies but has no jar to install.
func (c Coordinates) PomOnly() bool { return c.Extension == "pom" }

// IsSnapshot reports a -SNAPSHOT version: mutable by definition, so it is never
// served from the global store, never metadata-cached and never integrity
// checked against the lock.
func (c Coordinates) IsSnapshot() bool { return strings.HasSuffix(string(c.Version), "-SNAPSHOT") }

// GAV is the plain group:artifact:version identity, stripped of classifier
// and extension: what names the POM and its metadata.
func (c Coordinates) GAV() Coordinates {
	return Coordinates{GroupID: c.GroupID, ArtifactID: c.ArtifactID, Version: c.Version}
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
	// Exclusions are the <exclusions> patterns ("group:artifact", either side
	// may be "*") pruned from this dependency's subtree.
	Exclusions []string
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
	// GetManaged returns the versions a BOM manages (its <dependencyManagement>
	// over the parent chain and imports), keyed like Coordinates.Key; nil if
	// the POM is unknown here.
	GetManaged(c Coordinates) (map[PackageKey]string, error)
}
