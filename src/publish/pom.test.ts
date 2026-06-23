import { writeFileSync } from "node:fs";
import TempDir from "../TempDir.ts";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import { type CappuConfig, loadConfig } from "../config.ts";
import { generatePom, missingCoordinates } from "./pom.ts";

function configFrom(obj: Record<string, unknown>): CappuConfig {
  using dir = TempDir.create("cappu-pom-");
  writeFileSync(join(dir.path, "cappu.json"), JSON.stringify(obj));
  return loadConfig(undefined, dir.path);
}

test("generatePom emits coordinates, packaging, license and scoped dependencies", () => {
  const pom = generatePom(
    configFrom({
      groupId: "com.example",
      artifactId: "my-lib",
      version: "1.2.0",
      license: "MIT",
      dependencies: {
        api: { "com.google.code.gson:gson": "2.13.1" },
        implementation: { "com.google.guava:guava": "33.2.1-jre" },
        testImplementation: { "org.junit.jupiter:junit-jupiter": "5.12.2" },
        annotationProcessor: { "org.mapstruct:mapstruct-processor": "1.6.3" },
      },
    }),
  );

  expect(pom).toContain("<groupId>com.example</groupId>");
  expect(pom).toContain("<artifactId>my-lib</artifactId>");
  expect(pom).toContain("<version>1.2.0</version>");
  expect(pom).toContain("<packaging>jar</packaging>");
  expect(pom).toContain("<name>MIT</name>");

  // api -> compile (default scope, so no <scope> element)
  expect(pom).toMatch(
    /<artifactId>gson<\/artifactId>\s*<version>2\.13\.1<\/version>\s*<\/dependency>/,
  );
  // implementation -> runtime, testImplementation -> test
  expect(pom).toMatch(/<artifactId>guava<\/artifactId>[\s\S]*?<scope>runtime<\/scope>/);
  expect(pom).toMatch(/<artifactId>junit-jupiter<\/artifactId>[\s\S]*?<scope>test<\/scope>/);
  // annotationProcessor is a build-time tool: never in the POM
  expect(pom).not.toContain("mapstruct-processor");
});

test("a license-less project omits the <licenses> block", () => {
  const pom = generatePom(configFrom({ groupId: "g", artifactId: "a", version: "1.0.0" }));
  expect(pom).not.toContain("<licenses>");
  expect(pom).not.toContain("<dependencies>"); // none declared
});

test("missing coordinates are reported and make generatePom throw", () => {
  const config = configFrom({ groupId: "g", artifactId: "a" }); // no version
  expect(missingCoordinates(config)).toEqual(["version"]);
  expect(() => generatePom(config)).toThrow(/version/);
});
