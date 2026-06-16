import { test } from "node:test";

import { expect } from "expect";

import {
  type Coordinates,
  coordinatesToString,
  type CoordinateString,
  toCoordinates,
} from "../packages/index.ts";
import { auditPackages } from "./audit.ts";
import { type Advisory, type AdvisoryId, type AuditSource } from "./types.ts";

const advisory = (id: string, severity: Advisory["severity"]): Advisory => ({
  id: id as AdvisoryId,
  aliases: [],
  summary: id,
  severity,
  fixedVersions: [],
  url: `https://osv.dev/vulnerability/${id}`,
});

// A canned source: maps coordinate strings to advisories.
function source(map: Record<string, Advisory[]>): AuditSource {
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

test("auditPackages tallies counts, dedupes coordinates and sorts worst-first", async () => {
  const coords: Coordinates[] = [
    toCoordinates("org", "low", "1"),
    toCoordinates("org", "crit", "1"),
    toCoordinates("org", "crit", "1"), // duplicate (e.g. main + test)
    toCoordinates("org", "clean", "1"),
  ];
  const report = await auditPackages(
    coords,
    source({
      "org:low:1": [advisory("L1", "low")],
      "org:crit:1": [advisory("C1", "critical"), advisory("M1", "moderate")],
    }),
  );

  expect(report.scanned).toBe(3); // deduped: crit counted once
  expect(report.counts).toEqual({ critical: 1, high: 0, moderate: 1, low: 1, unknown: 0 });
  // crit (worst = critical) before low
  expect(report.vulnerable.map(p => p.coordinates.artifactId)).toEqual(["crit", "low"]);
});

test("auditPackages reports nothing for a clean set", async () => {
  const report = await auditPackages([toCoordinates("org", "clean", "1")], source({}));
  expect(report.vulnerable).toEqual([]);
  expect(report.scanned).toBe(1);
});
