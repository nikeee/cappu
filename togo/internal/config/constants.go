package config

import "regexp"

// Defaults and well-known constants, ported from src/config.ts.

const (
	DefaultConfigName = "cappu.json"
	SchemaFileName    = "cappu.schema.json"

	// Downloaded dependency jars live under .cappu/ - cappu-managed, gitignored.
	DefaultClassPath        = "./.cappu/lib/classes"
	DefaultSourcePath       = "./src/main/java"
	// DefaultGeneratedSourcePath: some build tools (Gradle sourceSets) and code
	// generators (sdmlib) keep generated production sources in their own root
	// alongside the hand-written ones. It is a default source root so such
	// projects build without extra config; a missing dir is simply ignored.
	DefaultGeneratedSourcePath = "./src/generated/java"
	DefaultResourcePath        = "./src/main/resources"
	DefaultTestSourcePath   = "./src/test/java"
	DefaultTestResourcePath = "./src/test/resources"
	DefaultTestClassPath    = "./.cappu/lib/test-classes"
	DefaultProcessorPath    = "./.cappu/lib/processors"

	// DefaultOutputDir is what `cappu compile` produces its output in; the build
	// output is always this.
	DefaultOutputDir = "dist"

	MavenCentral       = "https://repo.maven.apache.org/maven2"
	MavenCentralSearch = "https://search.maven.org/solrsearch/select"
	GoogleMaven        = "https://maven.google.com"
	GradlePluginPortal = "https://plugins.gradle.org/m2"

	// DefaultPublishRegistry is where `cappu publish` uploads when nothing else
	// is configured (npm-style).
	DefaultPublishRegistry = MavenCentral
)

// ExternalClassPaths are the conventional Maven/Gradle dirs added to the
// default classPath so the language server resolves tool-managed jars.
var ExternalClassPaths = []string{
	"./target/dependency", // Maven: mvn dependency:copy-dependencies
	"./build/libs",        // Gradle build output
	"./lib",               // a commonly used manually-managed jar folder
	"./libs",
}

// DefaultSourcePaths are the source roots compiled when sourcePaths is unset:
// the hand-written tree plus the conventional generated-sources root.
var DefaultSourcePaths = []string{DefaultSourcePath, DefaultGeneratedSourcePath}

// DefaultPackageSources are the repositories Maven and Gradle resolve from out
// of the box.
var DefaultPackageSources = []string{MavenCentral, GoogleMaven, GradlePluginPortal}

// Configurations are the dependency configurations, in resolution order. The
// keys of the dependencies section mirror these names.
var Configurations = []string{"api", "implementation", "annotationProcessor", "testImplementation"}

// MavenID is the Maven groupId/artifactId charset.
var MavenID = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// Semver is the canonical semver.org regex: MAJOR.MINOR.PATCH with optional
// -prerelease and +build. "1.0.0" / "1.0.0-SNAPSHOT" pass; "1.0" / "RELEASE" do not.
var Semver = regexp.MustCompile(
	`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`,
)
