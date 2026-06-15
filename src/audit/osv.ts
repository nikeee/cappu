// OSV.dev as an AuditSource (https://api.osv.dev). Free, no auth, Maven-aware,
// and it does version-range matching server-side: querybatch returns the vuln
// ids affecting each {package, version}, then each id is hydrated once for its
// details. Injectable fetchJson keeps it testable without a network.

import { type Coordinates, coordinatesToString, type CoordinateString } from "../packages/index.ts";
import { type Advisory, type AuditSource, type Severity } from "./types.ts";

const API = "https://api.osv.dev";
// querybatch accepts many queries per request; chunk well under the limit.
const BATCH = 1000;

// POST when a body is given, else GET; returns the parsed JSON (or undefined).
export type FetchJson = (url: string, body?: unknown) => Promise<unknown>;

const defaultFetchJson: FetchJson = async (url, body) => {
  const response = await fetch(url, {
    method: body === undefined ? "GET" : "POST",
    ...(body === undefined
      ? {}
      : { headers: { "content-type": "application/json" }, body: JSON.stringify(body) }),
  });
  if (!response.ok) throw new Error(`OSV ${response.status} for ${url}`);
  return response.json();
};

interface OsvVuln {
  id: string;
  summary?: string;
  details?: string;
  aliases?: string[];
  severity?: { type: string; score: string }[];
  database_specific?: { severity?: string };
  affected?: {
    package?: { name?: string };
    ranges?: { events?: { introduced?: string; fixed?: string }[] }[];
  }[];
}

/** GHSA severity -> our bucket; falls back to the CVSS base score, else unknown. */
export function osvSeverity(vuln: OsvVuln): Severity {
  const ghsa = vuln.database_specific?.severity?.toLowerCase();
  if (ghsa === "critical" || ghsa === "high" || ghsa === "moderate" || ghsa === "low") {
    return ghsa;
  }
  // No GHSA label: bucket the CVSS base score the way GHSA/npm do.
  const score = cvssBaseScore(vuln.severity?.find(s => s.type.startsWith("CVSS"))?.score);
  if (score === undefined) return "unknown";
  if (score >= 9) return "critical";
  if (score >= 7) return "high";
  if (score >= 4) return "moderate";
  return "low";
}

// The base score from a CVSS vector is not carried by OSV, only the vector, so
// this returns undefined unless a bare numeric score was provided; severity
// then falls back to "unknown". (GHSA-reviewed entries always carry the label.)
function cvssBaseScore(vector: string | undefined): number | undefined {
  if (!vector) return undefined;
  const numeric = Number(vector);
  return Number.isFinite(numeric) ? numeric : undefined;
}

/** The CVE ids among a vuln's aliases. */
export function cveAliases(vuln: OsvVuln): string[] {
  return (vuln.aliases ?? []).filter(a => a.startsWith("CVE-"));
}

/** The "fixed" versions OSV records for `coordinates`' package. */
export function fixedVersionsOf(vuln: OsvVuln, coordinates: Coordinates): string[] {
  const name = `${coordinates.groupId}:${coordinates.artifactId}`;
  const fixed = new Set<string>();
  for (const affected of vuln.affected ?? []) {
    if (affected.package?.name && affected.package.name !== name) continue;
    for (const range of affected.ranges ?? []) {
      for (const event of range.events ?? []) {
        if (event.fixed) fixed.add(event.fixed);
      }
    }
  }
  return [...fixed];
}

function toAdvisory(vuln: OsvVuln, coordinates: Coordinates): Advisory {
  return {
    id: vuln.id,
    aliases: cveAliases(vuln),
    summary: vuln.summary ?? vuln.details?.split("\n")[0] ?? "(no summary)",
    severity: osvSeverity(vuln),
    fixedVersions: fixedVersionsOf(vuln, coordinates),
    url: `https://osv.dev/vulnerability/${vuln.id}`,
  };
}

export class OsvSource implements AuditSource {
  readonly name = API;

  constructor(private readonly fetchJson: FetchJson = defaultFetchJson) {}

  async findVulnerabilities(
    coordinates: readonly Coordinates[],
  ): Promise<Map<CoordinateString, Advisory[]>> {
    const result = new Map<CoordinateString, Advisory[]>();
    if (coordinates.length === 0) return result;

    // 1. batched id lookup: results[i] lines up with coordinates[i]
    const idsByCoordinate = new Map<CoordinateString, string[]>();
    const wantedIds = new Set<string>();
    for (let start = 0; start < coordinates.length; start += BATCH) {
      const chunk = coordinates.slice(start, start + BATCH);
      const body = {
        queries: chunk.map(c => ({
          version: c.version,
          package: { name: `${c.groupId}:${c.artifactId}`, ecosystem: "Maven" },
        })),
      };
      const response = (await this.fetchJson(`${API}/v1/querybatch`, body)) as {
        results?: { vulns?: { id: string }[] }[];
      };
      (response.results ?? []).forEach((entry, i) => {
        const ids = (entry.vulns ?? []).map(v => v.id);
        if (ids.length === 0) return;
        idsByCoordinate.set(coordinatesToString(chunk[i]!), ids);
        for (const id of ids) wantedIds.add(id);
      });
    }

    // 2. hydrate each distinct vuln once
    const vulns = new Map<string, OsvVuln>();
    for (const id of wantedIds) {
      vulns.set(id, (await this.fetchJson(`${API}/v1/vulns/${id}`)) as OsvVuln);
    }

    // 3. attach advisories to their coordinates
    for (const c of coordinates) {
      const key = coordinatesToString(c);
      const ids = idsByCoordinate.get(key);
      if (!ids) continue;
      result.set(
        key,
        ids.map(id => toAdvisory(vulns.get(id)!, c)),
      );
    }
    return result;
  }
}
