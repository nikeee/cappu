import { mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import { loadConfig } from "./config.ts";
import { installDependencies } from "./install.ts";
import { MavenRepositorySource } from "./packages/index.ts";

const POM_GSON = `<project>
  <dependencies>
    <dependency>
      <groupId>org.example</groupId><artifactId>base</artifactId><version>1.0</version>
    </dependency>
  </dependencies>
</project>`;
const POM_BASE = `<project></project>`;

function fakeRepo(): MavenRepositorySource {
  const texts: Record<string, string> = {
    "https://repo.test/m2/com/google/code/gson/gson/2.14.0/gson-2.14.0.pom": POM_GSON,
    "https://repo.test/m2/org/example/base/1.0/base-1.0.pom": POM_BASE,
  };
  const jars: Record<string, string> = {
    "https://repo.test/m2/com/google/code/gson/gson/2.14.0/gson-2.14.0.jar": "gson-bytes",
    "https://repo.test/m2/org/example/base/1.0/base-1.0.jar": "base-bytes",
  };
  return new MavenRepositorySource(
    "https://repo.test/m2",
    url => Promise.resolve(texts[url]),
    url => Promise.resolve(jars[url] ? new TextEncoder().encode(jars[url]) : undefined),
  );
}

test("cappu install resolves transitively and writes jars into lib/classes", async () => {
  const dir = mkdtempSync(join(tmpdir(), "cappu-install-"));
  try {
    writeFileSync(
      join(dir, "cappu.json"),
      '{ "dependencies": { "implementation": { "com.google.code.gson:gson": "2.14.0" } } }',
    );
    const config = loadConfig(undefined, dir);
    const result = await installDependencies(config, [fakeRepo()]);

    expect(result.targetDir).toBe(join(dir, "lib/classes"));
    expect(result.installed).toEqual([
      join(dir, "lib/classes", "gson-2.14.0.jar"), // the root
      join(dir, "lib/classes", "base-1.0.jar"), // its transitive dependency
    ]);
    expect(result.noArtifact).toEqual([]);
    expect(result.resolution.missing).toEqual([]);
    expect(readFileSync(join(dir, "lib/classes", "gson-2.14.0.jar"), "utf8")).toBe("gson-bytes");
    expect(readdirSync(join(dir, "lib/classes")).sort()).toEqual([
      "base-1.0.jar",
      "gson-2.14.0.jar",
    ]);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("an unknown dependency surfaces as missing, nothing is written for it", async () => {
  const dir = mkdtempSync(join(tmpdir(), "cappu-install-"));
  try {
    writeFileSync(
      join(dir, "cappu.json"),
      '{ "dependencies": { "api": { "org.gone:gone": "9" } } }',
    );
    const config = loadConfig(undefined, dir);
    const result = await installDependencies(config, [fakeRepo()]);
    expect(result.installed).toEqual([]);
    expect(result.resolution.missing).toHaveLength(1);
    expect(result.resolution.missing[0]!.coordinates).toEqual({
      groupId: "org.gone",
      artifactId: "gone",
      version: "9",
    });
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
