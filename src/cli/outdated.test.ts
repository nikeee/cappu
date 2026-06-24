import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import TempDir from "../TempDir.ts";
import { loadConfig } from "../config.ts";
import { type OutdatedDependency, planOutdated } from "../install.ts";
import {
  InMemoryPackageSource,
  type PackageKey,
  type PackageMetadata,
  toCoordinates,
} from "../packages/index.ts";
import { formatOutdated } from "./outdated.ts";

function source(): InMemoryPackageSource {
  const pkg = (g: string, a: string, v: string, deps: PackageMetadata["dependencies"] = []) => ({
    coordinates: toCoordinates(g, a, v),
    dependencies: deps,
  });
  return new InMemoryPackageSource("versions", [
    pkg("org.x", "lib", "1.0"),
    pkg("org.x", "lib", "1.2"), // a newer in-major release (the `wanted` target)
    pkg("org.x", "lib", "2.0"), // a major bump (the `latest`)
    pkg("org.x", "uptodate", "3.0"),
  ]);
}

function configWith(json: object): ReturnType<typeof loadConfig> {
  const dir = TempDir.create("cappu-outdated-");
  writeFileSync(join(dir.path, "cappu.json"), JSON.stringify(json));
  return loadConfig(undefined, dir.path);
}

test("planOutdated reports the in-major (wanted) and overall (latest) newer versions", async () => {
  const config = configWith({
    dependencies: {
      implementation: { "org.x:lib": "1.0", "org.x:uptodate": "3.0" },
    },
  });
  const rows = await planOutdated(config, [source()]);
  const expected: OutdatedDependency[] = [
    {
      configuration: "implementation",
      key: "org.x:lib" as PackageKey,
      current: "1.0",
      wanted: "1.2",
      latest: "2.0",
    },
  ];
  expect(rows).toEqual(expected);
});

test("planOutdated omits a dependency that is already newest", async () => {
  const config = configWith({ dependencies: { api: { "org.x:lib": "2.0" } } });
  expect(await planOutdated(config, [source()])).toEqual([]);
});

test("formatOutdated renders an aligned table, or empty when nothing is outdated", () => {
  expect(formatOutdated([])).toBe("");
  const out = formatOutdated([
    {
      configuration: "implementation",
      key: "org.x:lib" as PackageKey,
      current: "1.0",
      wanted: "1.2",
      latest: "2.0",
    },
  ]);
  expect(out).toContain("dependency");
  expect(out).toContain("org.x:lib");
  expect(out).toContain("1.0");
  expect(out).toContain("2.0");
});
