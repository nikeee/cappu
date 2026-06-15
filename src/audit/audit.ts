// Audit the resolved dependency set against an AuditSource. Print-free: builds
// an AuditReport the CLI renders.

import { type Coordinates, coordinatesToString } from "../packages/index.ts";
import {
  type Advisory,
  type AuditReport,
  type AuditSource,
  type PackageAdvisories,
  type Severity,
  SEVERITY_ORDER,
} from "./types.ts";

const rank = (s: Severity): number => SEVERITY_ORDER.indexOf(s);

function worstSeverity(advisories: readonly Advisory[]): Severity {
  return advisories.reduce<Severity>(
    (worst, a) => (rank(a.severity) < rank(worst) ? a.severity : worst),
    "unknown",
  );
}

/** Scan `coordinates` (deduped) for known vulnerabilities. */
export async function auditPackages(
  coordinates: readonly Coordinates[],
  source: AuditSource,
): Promise<AuditReport> {
  // dedupe by exact coordinate (a package can appear in several lock sets)
  const unique = new Map<string, Coordinates>();
  for (const c of coordinates) unique.set(coordinatesToString(c), c);
  const distinct = [...unique.values()];

  const found = await source.findVulnerabilities(distinct);

  const counts: Record<Severity, number> = {
    critical: 0,
    high: 0,
    moderate: 0,
    low: 0,
    unknown: 0,
  };
  const vulnerable: PackageAdvisories[] = [];
  for (const c of distinct) {
    const advisories = found.get(coordinatesToString(c));
    if (!advisories || advisories.length === 0) continue;
    for (const a of advisories) counts[a.severity]++;
    vulnerable.push({ coordinates: c, advisories });
  }

  // worst severity first, then by coordinate for stable output
  vulnerable.sort((a, b) => {
    const byWorst = rank(worstSeverity(a.advisories)) - rank(worstSeverity(b.advisories));
    return byWorst !== 0
      ? byWorst
      : coordinatesToString(a.coordinates).localeCompare(coordinatesToString(b.coordinates));
  });

  return { scanned: distinct.length, vulnerable, counts };
}
