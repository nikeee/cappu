// Generate a Maven POM (pom.xml) for the project from cappu.json - the
// descriptor a Maven registry needs beside the jar. Hand-written XML (the repo
// has no XML builder, only a parser); values are escaped. Phase 1: coordinates,
// packaging, license and the declared dependencies. The declared configurations
// map to Maven scopes the way Gradle's published POM does:
//   api -> compile (default, scope omitted)   implementation -> runtime
//   testImplementation -> test
// annotationProcessor is a build-time tool, not a consumer dependency, so it is
// left out of the POM entirely.

import { type CappuConfig } from "../config.ts";
import { compareStrings } from "../install.ts";

const SCOPED_CONFIGS = [
  { key: "api", scope: undefined }, // compile is Maven's default - no <scope>
  { key: "implementation", scope: "runtime" },
  { key: "testImplementation", scope: "test" },
] as const;

function escapeXml(value: string): string {
  return value.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;");
}

/** The coordinate fields a POM needs; thrown when any is missing. */
export function missingCoordinates(config: CappuConfig): ("groupId" | "artifactId" | "version")[] {
  return (["groupId", "artifactId", "version"] as const).filter(key => !config[key]);
}

/**
 * The project's pom.xml as a string. Throws when the coordinates are missing
 * (the CLI validates first, so this is a safety net with a clear message).
 */
export function generatePom(config: CappuConfig): string {
  const missing = missingCoordinates(config);
  if (missing.length > 0) {
    throw new Error(`cannot generate a POM: cappu.json is missing ${missing.join(", ")}`);
  }

  const dependencies: string[] = [];
  for (const { key, scope } of SCOPED_CONFIGS) {
    for (const [coordinate, version] of Object.entries(config.dependencies[key]).sort(([a], [b]) =>
      compareStrings(a, b),
    )) {
      const [groupId = "", artifactId = ""] = coordinate.split(":");
      dependencies.push(
        [
          "    <dependency>",
          `      <groupId>${escapeXml(groupId)}</groupId>`,
          `      <artifactId>${escapeXml(artifactId)}</artifactId>`,
          `      <version>${escapeXml(version)}</version>`,
          ...(scope ? [`      <scope>${scope}</scope>`] : []),
          "    </dependency>",
        ].join("\n"),
      );
    }
  }

  const licenses = config.license
    ? [
        "  <licenses>",
        "    <license>",
        `      <name>${escapeXml(config.license)}</name>`,
        "    </license>",
        "  </licenses>",
      ]
    : [];

  return [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<project xmlns="http://maven.apache.org/POM/4.0.0"',
    '         xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"',
    '         xsi:schemaLocation="http://maven.apache.org/POM/4.0.0 https://maven.apache.org/xsd/maven-4.0.0.xsd">',
    "  <modelVersion>4.0.0</modelVersion>",
    `  <groupId>${escapeXml(config.groupId!)}</groupId>`,
    `  <artifactId>${escapeXml(config.artifactId!)}</artifactId>`,
    `  <version>${escapeXml(config.version!)}</version>`,
    "  <packaging>jar</packaging>",
    ...licenses,
    ...(dependencies.length > 0 ? ["  <dependencies>", ...dependencies, "  </dependencies>"] : []),
    "</project>",
    "",
  ].join("\n");
}
