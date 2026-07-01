import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import TempDir from "./TempDir.ts";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import { loadConfig } from "./config.ts";
import {
  checkLocked,
  installDependencies,
  LOCKFILE_NAME,
  pickAddVersion,
  lockedCoordinates,
  storePathFor,
  verifyCache,
  verifyInstalled,
  withMetadataCache,
} from "./install.ts";
import {
  InMemoryPackageSource,
  MavenRepositorySource,
  type PackageMetadata,
  type SourceName,
  toCoordinates,
} from "./packages/index.ts";

// Every test runs against an isolated package store - never the user's real
// XDG cache (the store test below swaps in its own).
process.env.CAPPU_PACKAGE_STORE = TempDir.create("cappu-store-shared-").path;

const POM_GSON = `<project>
  <licenses>
    <license>
      <name>The Apache Software License, Version 2.0</name>
      <url>https://www.apache.org/licenses/LICENSE-2.0.txt</url>
    </license>
  </licenses>
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
  using dir = TempDir.create("cappu-install-");
  writeFileSync(
    join(dir.path, "cappu.json"),
    '{ "dependencies": { "implementation": { "com.google.code.gson:gson": "2.14.0" } } }',
  );
  const config = loadConfig(undefined, dir.path);
  const result = await installDependencies(config, [fakeRepo()]);

  expect(result.targetDir).toBe(join(dir.path, ".cappu", "lib", "classes"));
  expect(result.installed).toEqual([
    join(dir.path, ".cappu", "lib", "classes", "gson-2.14.0.jar"), // the root
    join(dir.path, ".cappu", "lib", "classes", "base-1.0.jar"), // its transitive dependency
  ]);
  expect(result.noArtifact).toEqual([]);
  expect(result.resolution.missing).toEqual([]);
  expect(readFileSync(join(dir.path, ".cappu", "lib", "classes", "gson-2.14.0.jar"), "utf8")).toBe(
    "gson-bytes",
  );
  expect(readdirSync(join(dir.path, ".cappu", "lib", "classes")).sort()).toEqual([
    "base-1.0.jar",
    "gson-2.14.0.jar",
  ]);
});

test("the lockfile records each package's raw (not normalized) licenses", async () => {
  using dir = TempDir.create("cappu-install-");
  writeFileSync(
    join(dir.path, "cappu.json"),
    '{ "dependencies": { "implementation": { "com.google.code.gson:gson": "2.14.0" } } }',
  );
  const config = loadConfig(undefined, dir.path);
  await installDependencies(config, [fakeRepo()]);
  const lock = JSON.parse(readFileSync(join(dir.path, LOCKFILE_NAME), "utf8")) as {
    packages: {
      coordinates: { artifactId: string };
      licenses?: { name: string; url?: string }[];
    }[];
  };
  const gson = lock.packages.find(p => p.coordinates.artifactId === "gson")!;
  // raw POM name, NOT the best-effort SPDX id ("Apache-2.0")
  expect(gson.licenses).toEqual([
    {
      name: "The Apache Software License, Version 2.0",
      url: "https://www.apache.org/licenses/LICENSE-2.0.txt",
    },
  ]);
  // base declares no license: no licenses key at all
  const base = lock.packages.find(p => p.coordinates.artifactId === "base")!;
  expect(base.licenses).toBeUndefined();
});

test("an unknown dependency surfaces as missing, nothing is written for it", async () => {
  using dir = TempDir.create("cappu-install-");
  writeFileSync(
    join(dir.path, "cappu.json"),
    '{ "dependencies": { "api": { "org.gone:gone": "9" } } }',
  );
  const config = loadConfig(undefined, dir.path);
  const result = await installDependencies(config, [fakeRepo()]);
  expect(result.installed).toEqual([]);
  expect(result.resolution.missing).toHaveLength(1);
  expect(result.resolution.missing[0]!.coordinates).toEqual({
    groupId: "org.gone",
    artifactId: "gone",
    version: "9",
  });
});

test("checkLocked: ok when the lock matches, stale on drift, missing when absent", async () => {
  using dir = TempDir.create("cappu-locked-");
  const configPath = join(dir.path, "cappu.json");
  writeFileSync(
    configPath,
    '{ "dependencies": { "implementation": { "com.google.code.gson:gson": "2.14.0" } } }',
  );
  // No lock yet, but a dependency is declared -> missing.
  expect(checkLocked(loadConfig(undefined, dir.path))).toEqual({
    ok: false,
    reason: expect.stringMatching(/no cappu-lock\.json/),
  });

  // Resolve to write the lock; now it matches -> ok.
  await installDependencies(loadConfig(undefined, dir.path), [fakeRepo()]);
  expect(checkLocked(loadConfig(undefined, dir.path))).toEqual({ ok: true });

  // Change the declared dependencies: the lock is now stale.
  writeFileSync(
    configPath,
    '{ "dependencies": { "implementation": { "com.google.code.gson:gson": "2.13.0" } } }',
  );
  expect(checkLocked(loadConfig(undefined, dir.path))).toEqual({
    ok: false,
    reason: expect.stringMatching(/disagree/),
  });

  // A project with no declared dependencies and no lock is fine.
  using bare = TempDir.create("cappu-locked-bare-");
  writeFileSync(join(bare.path, "cappu.json"), "{}");
  expect(checkLocked(loadConfig(undefined, bare.path))).toEqual({ ok: true });
});

test("install writes a lockfile and reuses it while the dependencies match", async () => {
  using dir = TempDir.create("cappu-install-");
  writeFileSync(
    join(dir.path, "cappu.json"),
    '{ "dependencies": { "implementation": { "com.google.code.gson:gson": "2.14.0" } } }',
  );
  const config = loadConfig(undefined, dir.path);
  const first = await installDependencies(config, [fakeRepo()]);
  expect(first.fromLock).toBe(false);
  const lock = JSON.parse(readFileSync(join(dir.path, LOCKFILE_NAME), "utf8")) as {
    version: number;
    roots: unknown;
    packages: { sha256: string; coordinates: { groupId: string; artifactId: string } }[];
  };
  expect(lock.version).toBe(2);
  expect(lock.packages).toHaveLength(2); // gson + its transitive base
  // every artifact is pinned by the hash of its downloaded bytes (#2)
  for (const pkg of lock.packages) expect(pkg.sha256).toMatch(/^[0-9a-f]{64}$/);
  // The set is sorted by coordinate so the lock is deterministic across runs.
  expect(lock.packages.map(p => `${p.coordinates.groupId}:${p.coordinates.artifactId}`)).toEqual([
    "com.google.code.gson:gson",
    "org.example:base",
  ]);

  // Unchanged section: the locked set installs without any POM fetch.
  const fetchedPoms: string[] = [];
  const countingRepo = new MavenRepositorySource(
    "https://repo.test/m2",
    url => {
      fetchedPoms.push(url);
      return Promise.resolve(undefined);
    },
    url => {
      // the same bytes the lock was written from - a locked install verifies
      const file = url.split("/").at(-1)!;
      const body = file.startsWith("gson-") ? "gson-bytes" : "base-bytes";
      return Promise.resolve(url.endsWith(".jar") ? new TextEncoder().encode(body) : undefined);
    },
  );
  const second = await installDependencies(loadConfig(undefined, dir.path), [countingRepo]);
  expect(second.fromLock).toBe(true);
  expect(second.installed).toHaveLength(2);
  expect(second.integrityFailures).toEqual([]);
  expect(fetchedPoms).toEqual([]);

  // A tampered artifact (bytes differing from the locked SHA-256) is refused.
  // The store would mask the tampered repository (a cache hit IS the honest
  // bytes), so it is emptied first to force the download path.
  rmSync(process.env.CAPPU_PACKAGE_STORE!, { recursive: true, force: true });
  const tamperedRepo = new MavenRepositorySource(
    "https://repo.test/m2",
    () => Promise.resolve(undefined),
    url =>
      Promise.resolve(url.endsWith(".jar") ? new TextEncoder().encode("evil-bytes") : undefined),
  );
  const tampered = await installDependencies(loadConfig(undefined, dir.path), [tamperedRepo]);
  expect(tampered.integrityFailures).toEqual([
    "com.google.code.gson:gson:2.14.0",
    "org.example:base:1.0",
  ]);
  expect(tampered.installed).toEqual([]);

  // Changed section: install ONLY respects the lock - the locked set is
  // installed anyway, flagged stale; updateLock (what `cappu add` passes)
  // re-resolves and rewrites it.
  writeFileSync(
    join(dir.path, "cappu.json"),
    '{ "dependencies": { "implementation": { "com.google.code.gson:gson": "2.14.0", "org.example:base": "1.0" } } }',
  );
  const third = await installDependencies(loadConfig(undefined, dir.path), [fakeRepo()]);
  expect(third.fromLock).toBe(true);
  expect(third.lockStale).toBe(true);
  const fourth = await installDependencies(loadConfig(undefined, dir.path), [fakeRepo()], {
    updateLock: true,
  });
  expect(fourth.fromLock).toBe(false);
  expect(fourth.lockStale).toBe(false);
  const rewritten = JSON.parse(readFileSync(join(dir.path, LOCKFILE_NAME), "utf8")) as {
    roots: { implementation: Record<string, string> };
  };
  expect(Object.keys(rewritten.roots.implementation).sort()).toEqual([
    "com.google.code.gson:gson",
    "org.example:base",
  ]);
});

function versionedSource(): InMemoryPackageSource {
  const pkg = (g: string, a: string, v: string, deps: PackageMetadata["dependencies"] = []) => ({
    coordinates: toCoordinates(g, a, v),
    dependencies: deps,
  });
  return new InMemoryPackageSource("versions", [
    pkg("org.x", "base", "1.0"),
    pkg("org.x", "base", "2.0"),
    // lib@1 sits on base 1, lib@2.0/2.1 sit on base 2
    pkg("org.x", "lib", "1.5", [toCoordinates("org.x", "base", "1.0")]),
    pkg("org.x", "lib", "2.0", [toCoordinates("org.x", "base", "2.0")]),
    pkg("org.x", "lib", "2.1", [toCoordinates("org.x", "base", "2.0")]),
  ]);
}

function configWith(dir: string, json: string): ReturnType<typeof loadConfig> {
  writeFileSync(join(dir, "cappu.json"), json);
  return loadConfig(undefined, dir);
}

test("annotationProcessor deps resolve independently into lib/processors", async () => {
  using dir = TempDir.create("cappu-install-");
  writeFileSync(
    join(dir.path, "cappu.json"),
    JSON.stringify({
      dependencies: {
        implementation: { "com.google.code.gson:gson": "2.14.0" },
        annotationProcessor: { "org.example:base": "1.0" },
      },
    }),
  );
  const config = loadConfig(undefined, dir.path);
  const result = await installDependencies(config, [fakeRepo()]);
  // compile deps in lib/classes, the processor in lib/processors
  expect(result.installed).toContain(join(dir.path, ".cappu", "lib", "classes", "gson-2.14.0.jar"));
  expect(result.installed).toContain(join(dir.path, ".cappu", "lib", "processors", "base-1.0.jar"));
  // the processor dir holds ONLY the processor closure (gson stays out)
  expect(readdirSync(join(dir.path, ".cappu", "lib", "processors"))).toEqual(["base-1.0.jar"]);

  const lock = JSON.parse(readFileSync(join(dir.path, LOCKFILE_NAME), "utf8")) as {
    packages: unknown[];
    processorPackages: unknown[];
  };
  expect(lock.packages).toHaveLength(2); // gson + transitive base
  expect(lock.processorPackages).toHaveLength(1);

  // the locked sets install again without resolution
  const again = await installDependencies(config, [fakeRepo()]);
  expect(again.fromLock).toBe(true);
  expect(again.lockStale).toBe(false);
  expect(again.installed).toContain(join(dir.path, ".cappu", "lib", "processors", "base-1.0.jar"));
});

test("testImplementation deps resolve independently into lib/test-classes", async () => {
  using dir = TempDir.create("cappu-install-");
  writeFileSync(
    join(dir.path, "cappu.json"),
    JSON.stringify({ dependencies: { testImplementation: { "org.example:base": "1.0" } } }),
  );
  const config = loadConfig(undefined, dir.path);
  const result = await installDependencies(config, [fakeRepo()]);
  expect(result.installed).toEqual([
    join(dir.path, ".cappu", "lib", "test-classes", "base-1.0.jar"),
  ]);
  const lock = JSON.parse(readFileSync(join(dir.path, LOCKFILE_NAME), "utf8")) as {
    packages: unknown[];
    testPackages: unknown[];
  };
  expect(lock.packages).toHaveLength(0);
  expect(lock.testPackages).toHaveLength(1);
  const again = await installDependencies(config, [fakeRepo()]);
  expect(again.fromLock).toBe(true);
  expect(again.installed).toEqual([
    join(dir.path, ".cappu", "lib", "test-classes", "base-1.0.jar"),
  ]);
});

test("locks written before the annotationProcessor configuration stay fresh", async () => {
  using dir = TempDir.create("cappu-install-");
  writeFileSync(
    join(dir.path, "cappu.json"),
    '{ "dependencies": { "implementation": { "com.google.code.gson:gson": "2.14.0" } } }',
  );
  const config = loadConfig(undefined, dir.path);
  await installDependencies(config, [fakeRepo()]);
  // simulate a lock from BEFORE the schema knew annotationProcessor: its
  // roots lack the (empty) configuration entirely
  const lock = JSON.parse(readFileSync(join(dir.path, LOCKFILE_NAME), "utf8")) as {
    roots: Record<string, unknown>;
  };
  delete lock.roots.annotationProcessor;
  delete (lock.roots as { api?: unknown }).api; // empty sections may be absent too
  writeFileSync(join(dir.path, LOCKFILE_NAME), JSON.stringify(lock));

  const again = await installDependencies(config, [fakeRepo()]);
  expect(again.fromLock).toBe(true);
  expect(again.lockStale).toBe(false);
});

test("pickAddVersion takes the newest matching version that resolves conflict-free", async () => {
  using dir = TempDir.create("cappu-pick-");
  const empty = configWith(dir.path, "{}");
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
    dir.path,
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
});

test("listVersions answers are cached in the package store with a TTL", async () => {
  using store = TempDir.create("cappu-store-");
  const previousStore = process.env.CAPPU_PACKAGE_STORE;
  process.env.CAPPU_PACKAGE_STORE = store.path;
  try {
    let fetches = 0;
    const source = withMetadataCache({
      name: "https://repo.test/m2" as SourceName,
      search: () => Promise.resolve([]),
      listVersions: () => {
        fetches++;
        return Promise.resolve(["1.0", "1.1"]);
      },
      getMetadata: () => Promise.resolve(undefined),
    });

    expect(await source.listVersions("org.example", "thing")).toEqual(["1.0", "1.1"]);
    expect(await source.listVersions("org.example", "thing")).toEqual(["1.0", "1.1"]);
    expect(fetches).toBe(1); // second answer came from the store

    // an expired entry is refetched and rewritten
    const cacheFile = join(
      store.path,
      "_metadata",
      "https_repo.test_m2",
      "org",
      "example",
      "thing",
      "versions.json",
    );
    writeFileSync(cacheFile, JSON.stringify({ fetchedAt: 0, versions: ["stale"] }));
    expect(await source.listVersions("org.example", "thing")).toEqual(["1.0", "1.1"]);
    expect(fetches).toBe(2);

    // unsafe segments bypass the cache entirely
    expect(await source.listVersions("org/../evil", "thing")).toEqual(["1.0", "1.1"]);
    expect(await source.listVersions("org/../evil", "thing")).toEqual(["1.0", "1.1"]);
    expect(fetches).toBe(4);
  } finally {
    if (previousStore === undefined) delete process.env.CAPPU_PACKAGE_STORE;
    else process.env.CAPPU_PACKAGE_STORE = previousStore;
    rmSync(store.path, { recursive: true, force: true });
  }
});

test("lockedCoordinates flattens every locked set, undefined without a lock", async () => {
  using store = TempDir.create("cappu-store-");
  using dir = TempDir.create("cappu-install-");
  const previousStore = process.env.CAPPU_PACKAGE_STORE;
  process.env.CAPPU_PACKAGE_STORE = store.path;
  try {
    expect(lockedCoordinates(loadConfig(undefined, dir.path))).toBeUndefined();
    writeFileSync(
      join(dir.path, "cappu.json"),
      JSON.stringify({
        dependencies: {
          implementation: { "com.google.code.gson:gson": "2.14.0" },
          testImplementation: { "org.example:base": "1.0" },
        },
      }),
    );
    const config = loadConfig(undefined, dir.path);
    await installDependencies(config, [fakeRepo()]);
    const coords = lockedCoordinates(config)!.map(c => `${c.groupId}:${c.artifactId}:${c.version}`);
    // gson + its transitive base (compile set) and the test-set base
    expect(coords).toContain("com.google.code.gson:gson:2.14.0");
    expect(coords.filter(c => c === "org.example:base:1.0").length).toBeGreaterThanOrEqual(1);
  } finally {
    if (previousStore === undefined) delete process.env.CAPPU_PACKAGE_STORE;
    else process.env.CAPPU_PACKAGE_STORE = previousStore;
    rmSync(store.path, { recursive: true, force: true });
    rmSync(dir.path, { recursive: true, force: true });
  }
});

test("verifyInstalled checks lib jars against the lockfile sums", async () => {
  using store = TempDir.create("cappu-store-");
  using dir = TempDir.create("cappu-install-");
  const previousStore = process.env.CAPPU_PACKAGE_STORE;
  process.env.CAPPU_PACKAGE_STORE = store.path;
  try {
    const config = loadConfig(undefined, dir.path);
    // no lockfile yet
    expect(verifyInstalled(config).fromLock).toBe(false);

    writeFileSync(
      join(dir.path, "cappu.json"),
      '{ "dependencies": { "implementation": { "com.google.code.gson:gson": "2.14.0" } } }',
    );
    const installed = loadConfig(undefined, dir.path);
    await installDependencies(installed, [fakeRepo()]);

    // fresh install: every locked jar matches
    const clean = verifyInstalled(installed);
    expect(clean.modified).toEqual([]);
    expect(clean.missing).toEqual([]);
    expect(clean.ok).toContain("com.google.code.gson:gson:2.14.0");

    // tamper one jar on disk -> modified (materialized jars are read-only, so
    // replace rather than overwrite in place)
    const tampered = join(dir.path, ".cappu", "lib", "classes", "gson-2.14.0.jar");
    rmSync(tampered, { force: true });
    writeFileSync(tampered, "corrupted");
    expect(verifyInstalled(installed).modified).toContain("com.google.code.gson:gson:2.14.0");

    // delete another -> missing
    rmSync(join(dir.path, ".cappu", "lib", "classes", "base-1.0.jar"));
    expect(verifyInstalled(installed).missing).toContain("org.example:base:1.0");
  } finally {
    if (previousStore === undefined) delete process.env.CAPPU_PACKAGE_STORE;
    else process.env.CAPPU_PACKAGE_STORE = previousStore;
    rmSync(store.path, { recursive: true, force: true });
    rmSync(dir.path, { recursive: true, force: true });
  }
});

test("a SHA-256 mismatch evicts the bad jar from the store (#2)", async () => {
  using store = TempDir.create("cappu-store-");
  using dir = TempDir.create("cappu-install-");
  const previousStore = process.env.CAPPU_PACKAGE_STORE;
  process.env.CAPPU_PACKAGE_STORE = store.path;
  try {
    writeFileSync(
      join(dir.path, "cappu.json"),
      '{ "dependencies": { "implementation": { "com.google.code.gson:gson": "2.14.0" } } }',
    );
    const config = loadConfig(undefined, dir.path);
    // first install populates the store + a correct lock
    await installDependencies(config, [fakeRepo()]);
    const gson = toCoordinates("com.google.code.gson", "gson", "2.14.0");
    expect(existsSync(storePathFor(gson)!)).toBe(true);
    expect(existsSync(`${storePathFor(gson)!}.sha256`)).toBe(true);

    // tamper the lock's hash so the (correct) stored jar fails verification
    const lockPath = join(dir.path, LOCKFILE_NAME);
    const lock = JSON.parse(readFileSync(lockPath, "utf8")) as {
      packages: { coordinates: { artifactId: string }; sha256: string }[];
    };
    for (const p of lock.packages)
      if (p.coordinates.artifactId === "gson") p.sha256 = "0".repeat(64);
    writeFileSync(lockPath, JSON.stringify(lock));

    const result = await installDependencies(config, [fakeRepo()]);
    expect(result.integrityFailures).toContain("com.google.code.gson:gson:2.14.0");
    // the poisoned store entry (and its hash sidecar) is gone, so a later good
    // install can re-download
    expect(existsSync(storePathFor(gson)!)).toBe(false);
    expect(existsSync(`${storePathFor(gson)!}.sha256`)).toBe(false);
  } finally {
    if (previousStore === undefined) delete process.env.CAPPU_PACKAGE_STORE;
    else process.env.CAPPU_PACKAGE_STORE = previousStore;
    rmSync(store.path, { recursive: true, force: true });
    rmSync(dir.path, { recursive: true, force: true });
  }
});

test("a downloaded jar gets a SHA-256 sidecar in the store", async () => {
  using store = TempDir.create("cappu-store-");
  using dir = TempDir.create("cappu-install-");
  const previousStore = process.env.CAPPU_PACKAGE_STORE;
  process.env.CAPPU_PACKAGE_STORE = store.path;
  try {
    writeFileSync(
      join(dir.path, "cappu.json"),
      '{ "dependencies": { "implementation": { "com.google.code.gson:gson": "2.14.0" } } }',
    );
    await installDependencies(loadConfig(undefined, dir.path), [fakeRepo()]);
    const gson = toCoordinates("com.google.code.gson", "gson", "2.14.0");
    const sidecar = `${storePathFor(gson)!}.sha256`;
    expect(existsSync(sidecar)).toBe(true);
    // the sidecar holds the hex SHA-256 of the cached jar bytes
    expect(readFileSync(sidecar, "utf8")).toBe(
      createHash("sha256").update(new TextEncoder().encode("gson-bytes")).digest("hex"),
    );
  } finally {
    if (previousStore === undefined) delete process.env.CAPPU_PACKAGE_STORE;
    else process.env.CAPPU_PACKAGE_STORE = previousStore;
    rmSync(store.path, { recursive: true, force: true });
    rmSync(dir.path, { recursive: true, force: true });
  }
});

test("verifyCache checks jars and poms against the hashes recorded beside them", () => {
  using store = TempDir.create("cappu-store-");
  const previousStore = process.env.CAPPU_PACKAGE_STORE;
  process.env.CAPPU_PACKAGE_STORE = store.path;
  try {
    const sha = (s: string): string =>
      createHash("sha256").update(new TextEncoder().encode(s)).digest("hex");

    // a good jar (bytes match the sidecar)
    const okJar = join(store.path, "org", "a", "a", "1");
    mkdirSync(okJar, { recursive: true });
    writeFileSync(join(okJar, "a-1.jar"), "good");
    writeFileSync(join(okJar, "a-1.jar.sha256"), sha("good"));

    // a tampered jar (sidecar records different bytes) and an orphan sidecar
    const badJar = join(store.path, "org", "b", "b", "1");
    mkdirSync(badJar, { recursive: true });
    writeFileSync(join(badJar, "b-1.jar"), "tampered");
    writeFileSync(join(badJar, "b-1.jar.sha256"), sha("original"));
    writeFileSync(join(badJar, "ghost-1.jar.sha256"), sha("x")); // jar is gone

    // a good cached POM
    const meta = join(store.path, "_metadata", "src", "org", "c", "c", "1");
    mkdirSync(meta, { recursive: true });
    writeFileSync(join(meta, "c-1.pom"), "<pom/>");
    writeFileSync(
      join(meta, "metadata.json"),
      JSON.stringify({
        v: 2,
        metadata: { coordinates: { groupId: "org.c", artifactId: "c", version: "1" } },
        pomSha256: sha("<pom/>"),
      }),
    );

    const result = verifyCache();
    expect(result.ok.sort()).toEqual(
      [
        join("_metadata", "src", "org", "c", "c", "1", "c-1.pom"),
        join("org", "a", "a", "1", "a-1.jar"),
      ].sort(),
    );
    expect(result.modified).toEqual([join("org", "b", "b", "1", "b-1.jar")]);
    expect(result.missing).toEqual([join("org", "b", "b", "1", "ghost-1.jar")]);
  } finally {
    if (previousStore === undefined) delete process.env.CAPPU_PACKAGE_STORE;
    else process.env.CAPPU_PACKAGE_STORE = previousStore;
    rmSync(store.path, { recursive: true, force: true });
  }
});

test("the cached metadata persists the raw POM and records its SHA-256", async () => {
  using store = TempDir.create("cappu-store-");
  const previousStore = process.env.CAPPU_PACKAGE_STORE;
  process.env.CAPPU_PACKAGE_STORE = store.path;
  try {
    const pomBytes = new TextEncoder().encode("<project>raw pom</project>");
    const source = withMetadataCache({
      name: "https://repo.test/m2" as SourceName,
      search: () => Promise.resolve([]),
      listVersions: () => Promise.resolve([]),
      getMetadata: () =>
        Promise.resolve({ coordinates: toCoordinates("org.x", "y", "1.0"), dependencies: [] }),
      getPom: () => Promise.resolve(pomBytes),
    });
    await source.getMetadata(toCoordinates("org.x", "y", "1.0"));

    const verDir = join(store.path, "_metadata", "https_repo.test_m2", "org", "x", "y", "1.0");
    // the raw POM is persisted next to its metadata
    expect(readFileSync(join(verDir, "y-1.0.pom"))).toEqual(Buffer.from(pomBytes));
    // and metadata.json records its hash
    const entry = JSON.parse(readFileSync(join(verDir, "metadata.json"), "utf8")) as {
      pomSha256?: string;
    };
    expect(entry.pomSha256).toBe(createHash("sha256").update(pomBytes).digest("hex"));
  } finally {
    if (previousStore === undefined) delete process.env.CAPPU_PACKAGE_STORE;
    else process.env.CAPPU_PACKAGE_STORE = previousStore;
    rmSync(store.path, { recursive: true, force: true });
  }
});

test("getMetadata answers (resolved POMs) are cached forever in the store", async () => {
  using store = TempDir.create("cappu-store-");
  const previousStore = process.env.CAPPU_PACKAGE_STORE;
  process.env.CAPPU_PACKAGE_STORE = store.path;
  try {
    let fetches = 0;
    const metadata = {
      coordinates: toCoordinates("org.example", "thing", "1.0"),
      dependencies: [toCoordinates("org.dep", "dep", "2.0")],
    };
    const source = withMetadataCache({
      name: "https://repo.test/m2" as SourceName,
      search: () => Promise.resolve([]),
      listVersions: () => Promise.resolve([]),
      getMetadata: () => {
        fetches++;
        return Promise.resolve(metadata);
      },
    });
    const coords = toCoordinates("org.example", "thing", "1.0");

    expect(await source.getMetadata(coords)).toEqual(metadata);
    expect(await source.getMetadata(coords)).toEqual(metadata); // from the store, no TTL
    expect(fetches).toBe(1);

    // a different version is a different entry (immutable per version)
    await source.getMetadata(toCoordinates(coords.groupId, coords.artifactId, "1.1"));
    expect(fetches).toBe(2);

    // a not-found answer is not cached (re-fetched each time)
    const empty = withMetadataCache({
      name: "https://repo.test/m2" as SourceName,
      search: () => Promise.resolve([]),
      listVersions: () => Promise.resolve([]),
      getMetadata: () => {
        fetches++;
        return Promise.resolve(undefined);
      },
    });
    await empty.getMetadata(toCoordinates("org.missing", "x", "1"));
    await empty.getMetadata(toCoordinates("org.missing", "x", "1"));
    expect(fetches).toBe(4);
  } finally {
    if (previousStore === undefined) delete process.env.CAPPU_PACKAGE_STORE;
    else process.env.CAPPU_PACKAGE_STORE = previousStore;
    rmSync(store.path, { recursive: true, force: true });
  }
});

test("a metadata entry from an older cappu (no schema version) is ignored and re-fetched", async () => {
  using store = TempDir.create("cappu-store-");
  const previousStore = process.env.CAPPU_PACKAGE_STORE;
  process.env.CAPPU_PACKAGE_STORE = store.path;
  try {
    let fetches = 0;
    const metadata = { coordinates: toCoordinates("org.x", "y", "1.0"), dependencies: [] };
    const source = withMetadataCache({
      name: "https://repo.test/m2" as SourceName,
      search: () => Promise.resolve([]),
      listVersions: () => Promise.resolve([]),
      getMetadata: () => {
        fetches++;
        return Promise.resolve(metadata);
      },
    });
    const coords = toCoordinates("org.x", "y", "1.0");
    await source.getMetadata(coords); // writes a current-schema entry
    expect(fetches).toBe(1);

    // overwrite it with the bare PackageMetadata an older cappu would have stored
    const rel = (readdirSync(store.path, { recursive: true }) as string[]).find(f =>
      f.endsWith("metadata.json"),
    );
    writeFileSync(join(store.path, rel!), JSON.stringify(metadata));

    await source.getMetadata(coords); // stale schema -> re-fetched, not served
    expect(fetches).toBe(2);
  } finally {
    if (previousStore === undefined) delete process.env.CAPPU_PACKAGE_STORE;
    else process.env.CAPPU_PACKAGE_STORE = previousStore;
    rmSync(store.path, { recursive: true, force: true });
  }
});

test("the package store serves repeat installs and rejects unsafe segments", async () => {
  using store = TempDir.create("cappu-store-");
  using dirA = TempDir.create("cappu-install-");
  using dirB = TempDir.create("cappu-install-");
  const previousStore = process.env.CAPPU_PACKAGE_STORE;
  process.env.CAPPU_PACKAGE_STORE = store.path;
  try {
    const json =
      '{ "dependencies": { "implementation": { "com.google.code.gson:gson": "2.14.0" } } }';
    writeFileSync(join(dirA.path, "cappu.json"), json);
    const first = await installDependencies(loadConfig(undefined, dirA.path), [fakeRepo()]);
    expect(first.fromStore).toEqual([]);
    // maven2 layout under the store keeps a.b:c and a.b.c:d apart
    expect(
      readFileSync(
        join(store.path, "com", "google", "code", "gson", "gson", "2.14.0", "gson-2.14.0.jar"),
        "utf8",
      ),
    ).toBe("gson-bytes");

    // A second project with a repo that can only answer metadata: the jars
    // come from the store.
    writeFileSync(join(dirB.path, "cappu.json"), json);
    const metadataOnly = new MavenRepositorySource(
      "https://repo.test/m2",
      url =>
        Promise.resolve(
          url.endsWith("gson-2.14.0.pom")
            ? POM_GSON
            : url.endsWith("base-1.0.pom")
              ? POM_BASE
              : undefined,
        ),
      () => Promise.resolve(undefined),
    );
    const second = await installDependencies(loadConfig(undefined, dirB.path), [metadataOnly]);
    expect(second.installed).toHaveLength(2);
    expect(second.fromStore).toEqual(["com.google.code.gson:gson:2.14.0", "org.example:base:1.0"]);

    // A poisoned store entry fails the locked install like any tampered jar.
    // Materialized jars are read-only (hardlinks share the store inode), so
    // tampering replaces the entry rather than writing it in place.
    const poison = join(
      store.path,
      "com",
      "google",
      "code",
      "gson",
      "gson",
      "2.14.0",
      "gson-2.14.0.jar",
    );
    rmSync(poison, { force: true });
    writeFileSync(poison, "evil-bytes");
    const poisoned = await installDependencies(loadConfig(undefined, dirB.path), [metadataOnly]);
    expect(poisoned.integrityFailures).toEqual(["com.google.code.gson:gson:2.14.0"]);

    // Unsafe coordinate segments never map into the store.
    expect(storePathFor(toCoordinates("../../etc", "x", "1"))).toBeUndefined();
    expect(storePathFor(toCoordinates("a..b", "x", "1"))).toBeUndefined();
    expect(storePathFor(toCoordinates("a.b", "x/y", "1"))).toBeUndefined();
  } finally {
    if (previousStore === undefined) delete process.env.CAPPU_PACKAGE_STORE;
    else process.env.CAPPU_PACKAGE_STORE = previousStore;
    for (const d of [store.path, dirA.path, dirB.path]) rmSync(d, { recursive: true, force: true });
  }
});

// nikeee/cappu#31: the compile/processor/test sets download concurrently, so a
// per-set limiter would let the real ceiling reach 3x DOWNLOAD_CONCURRENCY and
// trip the registry's rate limiting. The limiter is shared, so the live socket
// count never exceeds the single cap regardless of how the deps are split.
test("downloads stay under one global concurrency cap across all sets (#31)", async () => {
  using store = TempDir.create("cappu-store-");
  using dir = TempDir.create("cappu-install-");
  const previousStore = process.env.CAPPU_PACKAGE_STORE;
  process.env.CAPPU_PACKAGE_STORE = store.path;
  try {
    // 20 leaf deps per set (> the cap of 6): a per-set limiter would admit up
    // to 6 from each of the three sets at once (18 in flight).
    const cfg = (n: number) =>
      Object.fromEntries(Array.from({ length: n }, (_, i) => [`org.t:p${i}`, "1"]));
    writeFileSync(
      join(dir.path, "cappu.json"),
      JSON.stringify({
        dependencies: {
          implementation: cfg(20),
          annotationProcessor: cfg(20),
          testImplementation: cfg(20),
        },
      }),
    );

    let active = 0;
    let maxActive = 0;
    const sleep = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));
    const source = new MavenRepositorySource(
      "https://repo.test/m2",
      // every coordinate exists as a dependency-free POM
      url => Promise.resolve(url.endsWith(".pom") ? "<project></project>" : undefined),
      async url => {
        if (!url.endsWith(".jar")) return undefined;
        active++;
        maxActive = Math.max(maxActive, active);
        await sleep(10); // hold the socket so concurrent admissions pile up
        active--;
        return new TextEncoder().encode(url);
      },
    );

    const result = await installDependencies(loadConfig(undefined, dir.path), [source]);
    expect(result.installed).toHaveLength(60);
    expect(maxActive).toBeGreaterThan(0);
    expect(maxActive).toBeLessThanOrEqual(6); // DOWNLOAD_CONCURRENCY, shared
  } finally {
    if (previousStore === undefined) delete process.env.CAPPU_PACKAGE_STORE;
    else process.env.CAPPU_PACKAGE_STORE = previousStore;
    rmSync(store.path, { recursive: true, force: true });
  }
});

test("storePathFor appends the classifier to the jar filename", () => {
  const plain = toCoordinates("org.jacoco", "org.jacoco.agent", "0.8.12");
  const runtime = toCoordinates("org.jacoco", "org.jacoco.agent", "0.8.12", "runtime");
  expect(storePathFor(plain)!.endsWith("org.jacoco.agent-0.8.12.jar")).toBe(true);
  expect(storePathFor(runtime)!.endsWith("org.jacoco.agent-0.8.12-runtime.jar")).toBe(true);
  // a classified artifact must not collide with the plain one of the same GAV
  expect(storePathFor(runtime)).not.toBe(storePathFor(plain));
});
