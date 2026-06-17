package config

import "regexp"

// Defaults and well-known constants, ported from src/config.ts.

const (
	DefaultConfigName = "cappu.json"
	SchemaFileName    = "cappu.schema.json"

	// Downloaded dependency jars live under .cappu/ - cappu-managed, gitignored.
	DefaultClassPath        = "./.cappu/lib/classes"
	DefaultSourcePath       = "./src/main/java"
	DefaultResourcePath     = "./src/main/resources"
	DefaultTestSourcePath   = "./src/test/java"
	DefaultTestResourcePath = "./src/test/resources"
	DefaultTestClassPath    = "./.cappu/lib/test-classes"
	DefaultProcessorPath    = "./.cappu/lib/processors"

	MavenCentral       = "https://repo.maven.apache.org/maven2"
	MavenCentralSearch = "https://search.maven.org/solrsearch/select"
	GoogleMaven        = "https://maven.google.com"
	GradlePluginPortal = "https://plugins.gradle.org/m2"
)

// ExternalClassPaths are the conventional Maven/Gradle dirs added to the
// default classPath so the language server resolves tool-managed jars.
var ExternalClassPaths = []string{
	"./target/dependency", // Maven: mvn dependency:copy-dependencies
	"./build/libs",        // Gradle build output
	"./lib",               // a commonly used manually-managed jar folder
	"./libs",
}

// DefaultPackageSources are the repositories Maven and Gradle resolve from out
// of the box.
var DefaultPackageSources = []string{MavenCentral, GoogleMaven, GradlePluginPortal}

// MavenID is the Maven groupId/artifactId charset.
var MavenID = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// Semver is the canonical semver.org regex: MAJOR.MINOR.PATCH with optional
// -prerelease and +build. "1.0.0" / "1.0.0-SNAPSHOT" pass; "1.0" / "RELEASE" do not.
var Semver = regexp.MustCompile(
	`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`,
)
