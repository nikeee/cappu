import { test } from "node:test";
import { expect } from "expect";

import { type Advisory, type AuditSource } from "../audit/index.ts";
import type { CappuConfig } from "../config.ts";
import {
  type Coordinates,
  coordinatesToString,
  type CoordinateString,
  InMemoryPackageSource,
  type PackageMetadata,
} from "../packages/index.ts";
import { createProjectTools, type ProjectToolDeps } from "./mcpProject.ts";

// A config whose implementation deps are the given "group:artifact:version"
// specs; everything else createProjectTools touches stays empty.
function configWith(...specs: string[]): CappuConfig {
  const implementation: Record<string, string> = {};
  for (const spec of specs) {
    const [group, artifact, version] = spec.split(":");
    implementation[`${group}:${artifact}`] = version!;
  }
  return {
    dependencies: { api: {}, implementation, annotationProcessor: {}, testImplementation: {} },
  } as unknown as CappuConfig;
}

function meta(spec: string, extra: Partial<PackageMetadata> = {}): PackageMetadata {
  const [groupId, artifactId, version] = spec.split(":");
  return {
    coordinates: { groupId, artifactId, version } as unknown as Coordinates,
    dependencies: [],
    ...extra,
  };
}

const advisory = (id: string, severity: Advisory["severity"]): Advisory => ({
  id: id as Advisory["id"],
  aliases: [],
  summary: id,
  severity,
  fixedVersions: [],
  url: `https://osv.dev/vulnerability/${id}`,
});

function auditSource(map: Record<string, Advisory[]>): AuditSource {
  return {
    name: "fake",
    findVulnerabilities: (coordinates: readonly Coordinates[]) => {
      const result = new Map<CoordinateString, Advisory[]>();
      for (const c of coordinates) {
        const a = map[coordinatesToString(c)];
        if (a) result.set(coordinatesToString(c), a);
      }
      return Promise.resolve(result);
    },
  };
}

function tools(specs: string[], packages: PackageMetadata[], deps: ProjectToolDeps = {}) {
  const source = new InMemoryPackageSource("test", packages);
  return createProjectTools(configWith(...specs), { sources: [source], ...deps });
}

test("searchPackages returns matching coordinate strings", async () => {
  const t = tools([], [meta("org.a:alpha:1"), meta("org.b:beta:1")]);
  const { matches } = await t.searchPackages({ query: "alpha" });
  expect(matches).toEqual(["org.a:alpha:1"]);
});

test("licenses lists resolved packages with their SPDX ids, sorted", async () => {
  const t = tools(
    ["org.a:a:1"],
    [
      meta("org.a:a:1", {
        dependencies: [{ groupId: "org.b", artifactId: "b", version: "1" } as unknown as Coordinates],
        licenses: [{ name: "Apache-2.0", url: "https://apache.org/licenses/LICENSE-2.0" }],
        licenseNormalized: ["Apache-2.0" as never],
      }),
      meta("org.b:b:1", { licenses: [{ name: "MIT" }], licenseNormalized: ["MIT" as never] }),
    ],
  );
  const { licenses } = await t.licenses();
  expect(licenses.map(r => r.coordinate)).toEqual(["org.a:a:1", "org.b:b:1"]);
  expect(licenses[0].spdx).toEqual(["Apache-2.0"]);
  expect(licenses[0].licenses[0].url).toBe("https://apache.org/licenses/LICENSE-2.0");
  expect(licenses[1].licenses[0].url).toBeUndefined();
});

test("audit reports vulnerable packages with severity counts and a dependency path", async () => {
  const t = tools(
    ["org.a:a:1"],
    [
      meta("org.a:a:1", {
        dependencies: [{ groupId: "org.b", artifactId: "bad", version: "1" } as unknown as Coordinates],
      }),
      meta("org.b:bad:1"),
    ],
    { auditSource: auditSource({ "org.b:bad:1": [advisory("CVE-1", "high")] }) },
  );
  const report = await t.audit();
  expect(report.scanned).toBe(2);
  expect(report.counts.high).toBe(1);
  expect(report.vulnerable).toHaveLength(1);
  expect(report.vulnerable[0].coordinate).toBe("org.b:bad:1");
  expect(report.vulnerable[0].path).toEqual(["org.a:a:1", "org.b:bad:1"]);
  expect(report.vulnerable[0].advisories[0].id).toBe("CVE-1");
});

test("audit is clean when nothing is vulnerable", async () => {
  const t = tools(["org.a:a:1"], [meta("org.a:a:1")], { auditSource: auditSource({}) });
  const report = await t.audit();
  expect(report.vulnerable).toEqual([]);
});
