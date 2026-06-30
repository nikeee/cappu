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
	deps  map[string]string
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
		{cfg.Dependencies.TestImplementation, "test"},
	}
	var deps []string
	for _, sc := range scoped {
		for _, coordinate := range sortedKeys(sc.deps) {
			version := sc.deps[coordinate]
			groupID, artifactID, _ := strings.Cut(coordinate, ":")
			lines := []string{
				"    <dependency>",
				"      <groupId>" + escapeXML(groupID) + "</groupId>",
				"      <artifactId>" + escapeXML(artifactID) + "</artifactId>",
				"      <version>" + escapeXML(version) + "</version>",
			}
			if sc.scope != "" {
				lines = append(lines, "      <scope>"+sc.scope+"</scope>")
			}
			lines = append(lines, "    </dependency>")
			deps = append(deps, strings.Join(lines, "\n"))
		}
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
func sortedKeys(m map[string]string) []string {
	return slices.Sorted(maps.Keys(m))
}
