import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import TempDir from "../TempDir.ts";
import { type Advisory, type AuditSource } from "../audit/index.ts";
import { loadConfig } from "../config.ts";
import {
  type Coordinates,
  type CoordinateString,
  coordinatesToString,
  InMemoryPackageSource,
  type PackageMetadata,
  toCoordinates,
} from "../packages/index.ts";
import { buildShowData, renderShowCard, type ShowData, showToJson } from "./show.ts";

// A no-op paint so the card text asserts on content, not ANSI codes.
const plain = (_format: unknown, text: string): string => text;

function source(): InMemoryPackageSource {
  const pkg = (
    g: string,
    a: string,
    v: string,
    extra: Partial<PackageMetadata> = {},
  ): PackageMetadata => ({ coordinates: toCoordinates(g, a, v), dependencies: [], ...extra });
  return new InMemoryPackageSource("test", [
    pkg("com.google.code.gson", "gson", "2.11.0"),
    pkg("com.google.code.gson", "gson", "2.13.0"),
    pkg("com.google.code.gson", "gson", "2.13.1", {
      description: "A library to convert Java Objects into JSON and back",
      homepage: "https://github.com/google/gson",
      scmUrl: "https://github.com/google/gson.git",
      licenses: [{ name: "Apache-2.0", url: "https://www.apache.org/licenses/LICENSE-2.0.txt" }],
      dependencies: [toCoordinates("com.google.errorprone", "error_prone_annotations", "2.27.0")],
    }),
  ]);
}

/** An AuditSource that returns canned advisories for specific coordinates. */
function auditStub(byCoord: Record<string, Advisory[]> = {}): AuditSource {
  return {
    name: "stub",
    findVulnerabilities(coordinates: readonly Coordinates[]) {
      const out = new Map<CoordinateString, Advisory[]>();
      for (const c of coordinates) {
        const key = coordinatesToString(c);
        if (byCoord[key]) out.set(key, byCoord[key]);
      }
      return Promise.resolve(out);
    },
  };
}

function configWith(json: object): ReturnType<typeof loadConfig> {
  const dir = TempDir.create("cappu-show-");
  writeFileSync(join(dir.path, "cappu.json"), JSON.stringify(json));
  return loadConfig(undefined, dir.path);
}

async function ok(
  coord: string,
  config: ReturnType<typeof loadConfig>,
  audit: AuditSource = auditStub(),
): Promise<ShowData> {
  const data = await buildShowData(coord, config, [source()], audit);
  if ("error" in data) throw new Error(`unexpected error: ${data.error}`);
  return data;
}

test("show defaults to the latest version and reports it as latest", async () => {
  const config = configWith({});
  const data = await ok("com.google.code.gson:gson", config);

  expect(data.version).toBe("2.13.1");
  expect(data.latestVersion).toBe("2.13.1");
  expect(data.versionCount).toBe(3);
  expect(data.explicitVersion).toBe(false);
  expect(data.spdx).toEqual(["Apache-2.0"]);
  expect(data.homepage).toBe("https://github.com/google/gson");
  expect(data.project.configurations).toEqual([]);

  const card = renderShowCard(data, plain);
  expect(card).toContain("com.google.code.gson:gson 2.13.1  latest");
  expect(card).toContain("convert Java Objects into JSON");
  expect(card).toContain("License      Apache-2.0");
  expect(card).toContain("Homepage     https://github.com/google/gson");
  expect(card).toContain("Repository   https://github.com/google/gson.git");
  expect(card).toContain("not a direct dependency");
  expect(card).toContain("Dependencies (1)");
  expect(card).toContain("com.google.errorprone:error_prone_annotations:2.27.0");
  expect(card).toContain("no known vulnerabilities");
});

test("show on an older pinned version flags newer releases", async () => {
  const config = configWith({});
  const data = await ok("com.google.code.gson:gson:2.11.0", config);

  expect(data.version).toBe("2.11.0");
  expect(data.explicitVersion).toBe(true);
  expect(data.newer).toBe(2);
  expect(renderShowCard(data, plain)).toContain("2.11.0  2 newer available");
});

test("show surfaces how this project depends on the package", async () => {
  const config = configWith({
    dependencies: { implementation: { "com.google.code.gson:gson": "2.13.1" } },
  });
  const data = await ok("com.google.code.gson:gson", config);

  expect(data.project.configurations).toEqual(["implementation"]);
  expect(data.project.declared).toBe("2.13.1");
  expect(renderShowCard(data, plain)).toContain("In project   implementation (declared 2.13.1)");
});

test("show reports known vulnerabilities and exits non-zero", async () => {
  const config = configWith({});
  const advisory: Advisory = {
    id: "GHSA-xxxx-yyyy-zzzz" as Advisory["id"],
    aliases: ["CVE-2022-0001"],
    summary: "Example deserialization issue",
    severity: "high",
    fixedVersions: ["2.13.1"],
    url: "https://osv.dev/vulnerability/GHSA-xxxx-yyyy-zzzz",
  };
  const audit = auditStub({ "com.google.code.gson:gson:2.13.0": [advisory] });
  const data = await ok("com.google.code.gson:gson:2.13.0", config, audit);

  expect(data.vulnerabilities).toHaveLength(1);
  const card = renderShowCard(data, plain);
  expect(card).toContain(
    "HIGH  GHSA-xxxx-yyyy-zzzz (CVE-2022-0001) - Example deserialization issue",
  );
  expect(card).toContain("[fixed in: 2.13.1]");
});

test("show --json carries the same data", async () => {
  const config = configWith({
    dependencies: { implementation: { "com.google.code.gson:gson": "2.13.1" } },
  });
  const data = await ok("com.google.code.gson:gson", config);
  const json = showToJson(data) as Record<string, unknown>;

  expect(json.version).toBe("2.13.1");
  expect(json.latestVersion).toBe("2.13.1");
  expect(json.license).toEqual(["Apache-2.0"]);
  expect(json.homepage).toBe("https://github.com/google/gson");
  expect(json.dependencies).toEqual([
    {
      groupId: "com.google.errorprone",
      artifactId: "error_prone_annotations",
      version: "2.27.0",
    },
  ]);
  expect(json.project).toEqual({
    configurations: ["implementation"],
    declared: "2.13.1",
    installed: null,
  });
  expect(json.vulnerabilities).toEqual([]);
});

test("show errors on a malformed coordinate and an unknown package", async () => {
  const config = configWith({});
  expect(await buildShowData("not-a-coord", config, [source()], auditStub())).toEqual({
    error:
      "show needs group:artifact[:version], e.g. `cappu show com.google.code.gson:gson`; " +
      "search for a package with `cappu search <query>`",
    code: 2,
  });
  expect(await buildShowData("org.x:nope", config, [source()], auditStub())).toEqual({
    error: "package not found: org.x:nope; search for a package with `cappu search <query>`",
    code: 1,
  });
});
