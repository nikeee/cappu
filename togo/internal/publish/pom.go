// Package publish generates a Maven POM for the project and uploads the built
// artifacts to a Maven registry. Port of src/publish/.
package publish

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/nikeee/cappu/internal/config"
)

// scopedConfig maps a declared dependency configuration to the Maven scope its
// POM entry gets (Gradle-published-POM style); api -> compile (scope omitted).
type scopedConfig struct {
	deps  map[string]config.Dependency
	scope string
}

func escapeXML(value string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(value)
}

// MissingCoordinates returns the coordinate fields a POM needs that are absent.
func MissingCoordinates(cfg *config.Config) []string {
	var missing []string
	if cfg.GroupID == "" {
		missing = append(missing, "groupId")
	}
	if cfg.ArtifactID == "" {
		missing = append(missing, "artifactId")
	}
	if cfg.Version == "" {
		missing = append(missing, "version")
	}
	return missing
}

// GeneratePom renders the project's pom.xml. It errors when the coordinates are
// missing (the CLI validates first; this is a clear-message safety net).
// annotationProcessor is a build-time tool, so it is left out of the POM.
func GeneratePom(cfg *config.Config) (string, error) {
	if missing := MissingCoordinates(cfg); len(missing) > 0 {
		return "", fmt.Errorf("cannot generate a POM: cappu.json is missing %s", strings.Join(missing, ", "))
	}

	scoped := []scopedConfig{
		{cfg.Dependencies.API, ""}, // compile is Maven's default - no <scope>
		{cfg.Dependencies.Implementation, "runtime"},
		{cfg.Dependencies.CompileOnly, "provided"},
		{cfg.Dependencies.RuntimeOnly, "runtime"},
		{cfg.Dependencies.TestImplementation, "test"},
	}
	var deps []string
	for _, sc := range scoped {
		for _, coordinate := range sortedKeys(sc.deps) {
			dep := sc.deps[coordinate]
			groupID, artifactID, _ := strings.Cut(coordinate, ":")
			lines := []string{
				"    <dependency>",
				"      <groupId>" + escapeXML(groupID) + "</groupId>",
				"      <artifactId>" + escapeXML(artifactID) + "</artifactId>",
			}
			// A managed entry has no version of its own: the imported platform
			// below supplies it to consumers, exactly as it did to this build.
			if dep.Version != "" {
				lines = append(lines, "      <version>"+escapeXML(dep.Version)+"</version>")
			}
			if dep.Classifier != "" {
				lines = append(lines, "      <classifier>"+escapeXML(dep.Classifier)+"</classifier>")
			}
			if sc.scope != "" {
				lines = append(lines, "      <scope>"+sc.scope+"</scope>")
			}
			if len(dep.Exclude) > 0 {
				lines = append(lines, "      <exclusions>")
				for _, pattern := range dep.Exclude {
					exGroup, exArtifact, _ := strings.Cut(pattern, ":")
					lines = append(lines,
						"        <exclusion>",
						"          <groupId>"+escapeXML(exGroup)+"</groupId>",
						"          <artifactId>"+escapeXML(exArtifact)+"</artifactId>",
						"        </exclusion>")
				}
				lines = append(lines, "      </exclusions>")
			}
			lines = append(lines, "    </dependency>")
			deps = append(deps, strings.Join(lines, "\n"))
		}
	}
	// Platforms become scope=import entries, so a consumer resolves the managed
	// versions the way this build did.
	var platforms []string
	for _, coordinate := range cfg.Dependencies.PlatformKeys() { // declaration order: precedence
		groupID, artifactID, _ := strings.Cut(coordinate, ":")
		platforms = append(platforms, strings.Join([]string{
			"      <dependency>",
			"        <groupId>" + escapeXML(groupID) + "</groupId>",
			"        <artifactId>" + escapeXML(artifactID) + "</artifactId>",
			"        <version>" + escapeXML(cfg.Dependencies.Platform[coordinate]) + "</version>",
			"        <type>pom</type>",
			"        <scope>import</scope>",
			"      </dependency>",
		}, "\n"))
	}

	var lines []string
	add := func(ls ...string) { lines = append(lines, ls...) }
	add(
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<project xmlns="http://maven.apache.org/POM/4.0.0"`,
		`         xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"`,
		`         xsi:schemaLocation="http://maven.apache.org/POM/4.0.0 https://maven.apache.org/xsd/maven-4.0.0.xsd">`,
		"  <modelVersion>4.0.0</modelVersion>",
		"  <groupId>"+escapeXML(cfg.GroupID)+"</groupId>",
		"  <artifactId>"+escapeXML(cfg.ArtifactID)+"</artifactId>",
		"  <version>"+escapeXML(cfg.Version)+"</version>",
		"  <packaging>jar</packaging>",
	)
	if cfg.License != "" {
		add("  <licenses>", "    <license>", "      <name>"+escapeXML(cfg.License)+"</name>", "    </license>", "  </licenses>")
	}
	if len(platforms) > 0 {
		add("  <dependencyManagement>", "    <dependencies>")
		add(platforms...)
		add("    </dependencies>", "  </dependencyManagement>")
	}
	if len(deps) > 0 {
		add("  <dependencies>")
		add(deps...)
		add("  </dependencies>")
	}
	add("</project>", "")
	return strings.Join(lines, "\n"), nil
}

// sortedKeys gives deterministic dependency order (Go maps are unordered; the
// Node build relies on cappu.json's object-key order).
func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
