import { mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import { loadConfig } from "./config.ts";
import { installDependencies, LOCKFILE_NAME, pickAddVersion } from "./install.ts";
import {
  InMemoryPackageSource,
  MavenRepositorySource,
  type PackageMetadata,
} from "./packages/index.ts";

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

test("install writes a lockfile and reuses it while the dependencies match", async () => {
  const dir = mkdtempSync(join(tmpdir(), "cappu-install-"));
  try {
    writeFileSync(
      join(dir, "cappu.json"),
      '{ "dependencies": { "implementation": { "com.google.code.gson:gson": "2.14.0" } } }',
    );
    const config = loadConfig(undefined, dir);
    const first = await installDependencies(config, [fakeRepo()]);
    expect(first.fromLock).toBe(false);
    const lock = JSON.parse(readFileSync(join(dir, LOCKFILE_NAME), "utf8")) as {
      roots: unknown;
      packages: unknown[];
    };
    expect(lock.packages).toHaveLength(2); // gson + its transitive base

    // Unchanged section: the locked set installs without any POM fetch.
    const fetchedPoms: string[] = [];
    const countingRepo = new MavenRepositorySource(
      "https://repo.test/m2",
      url => {
        fetchedPoms.push(url);
        return Promise.resolve(undefined);
      },
      url =>
        Promise.resolve(url.endsWith(".jar") ? new TextEncoder().encode("jar-bytes") : undefined),
    );
    const second = await installDependencies(loadConfig(undefined, dir), [countingRepo]);
    expect(second.fromLock).toBe(true);
    expect(second.installed).toHaveLength(2);
    expect(fetchedPoms).toEqual([]);

    // Changed section: install ONLY respects the lock - the locked set is
    // installed anyway, flagged stale; updateLock (what `cappu add` passes)
    // re-resolves and rewrites it.
    writeFileSync(
      join(dir, "cappu.json"),
      '{ "dependencies": { "implementation": { "com.google.code.gson:gson": "2.14.0", "org.example:base": "1.0" } } }',
    );
    const third = await installDependencies(loadConfig(undefined, dir), [fakeRepo()]);
    expect(third.fromLock).toBe(true);
    expect(third.lockStale).toBe(true);
    const fourth = await installDependencies(loadConfig(undefined, dir), [fakeRepo()], {
      updateLock: true,
    });
    expect(fourth.fromLock).toBe(false);
    expect(fourth.lockStale).toBe(false);
    const rewritten = JSON.parse(readFileSync(join(dir, LOCKFILE_NAME), "utf8")) as {
      roots: { implementation: Record<string, string> };
    };
    expect(Object.keys(rewritten.roots.implementation).sort()).toEqual([
      "com.google.code.gson:gson",
      "org.example:base",
    ]);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

function versionedSource(): InMemoryPackageSource {
  const pkg = (g: string, a: string, v: string, deps: PackageMetadata["dependencies"] = []) => ({
    coordinates: { groupId: g, artifactId: a, version: v },
    dependencies: deps,
  });
  return new InMemoryPackageSource("versions", [
    pkg("org.x", "base", "1.0"),
    pkg("org.x", "base", "2.0"),
    // lib@1 sits on base 1, lib@2.0/2.1 sit on base 2
    pkg("org.x", "lib", "1.5", [{ groupId: "org.x", artifactId: "base", version: "1.0" }]),
    pkg("org.x", "lib", "2.0", [{ groupId: "org.x", artifactId: "base", version: "2.0" }]),
    pkg("org.x", "lib", "2.1", [{ groupId: "org.x", artifactId: "base", version: "2.0" }]),
  ]);
}

function configWith(dir: string, json: string): ReturnType<typeof loadConfig> {
  writeFileSync(join(dir, "cappu.json"), json);
  return loadConfig(undefined, dir);
}

test("pickAddVersion takes the newest matching version that resolves conflict-free", async () => {
  const dir = mkdtempSync(join(tmpdir(), "cappu-pick-"));
  try {
    // Nothing configured: the newest published version wins outright.
    const empty = configWith(dir, "{}");
    expect(await pickAddVersion(empty, "org.x:lib", undefined, [versionedSource()])).toEqual({
      version: "2.1",
      compatible: true,
    });
    // A partial spec restricts the candidates.
    expect(await pickAddVersion(empty, "org.x:lib", "1", [versionedSource()])).toEqual({
      version: "1.5",
      compatible: true,
    });
    // base 1.0 already configured: lib 2.x would conflict over base, so the
    // newest COMPATIBLE version is the 1.x line.
    const pinned = configWith(
      dir,
      '{ "dependencies": { "implementation": { "org.x:base": "1.0" } } }',
    );
    expect(await pickAddVersion(pinned, "org.x:lib", undefined, [versionedSource()])).toEqual({
      version: "1.5",
      compatible: true,
    });
    // A spec that ONLY matches conflicting versions falls back, flagged.
    expect(await pickAddVersion(pinned, "org.x:lib", "2", [versionedSource()])).toEqual({
      version: "2.1",
      compatible: false,
    });
    // No matching published version at all.
    expect(await pickAddVersion(empty, "org.x:lib", "9", [versionedSource()])).toBeUndefined();
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
