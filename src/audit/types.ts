// Vulnerability-audit domain model (nikeee/cappu): query a CVE source for the
// resolved Maven dependencies. Self-contained; reuses the package coordinates.

import { type Coordinates, type CoordinateString } from "../packages/index.ts";

/** Severity buckets, npm-aligned (GHSA's MODERATE maps to "moderate"). */
export type Severity = "critical" | "high" | "moderate" | "low" | "unknown";

/** Worst-first, for grouping and "highest severity" comparisons. */
export const SEVERITY_ORDER: readonly Severity[] = [
  "critical",
  "high",
  "moderate",
  "low",
  "unknown",
];

/** One known vulnerability affecting a package version. */
export interface Advisory {
  /** Primary id (usually a GHSA id). */
  readonly id: string;
  /** CVE aliases, e.g. ["CVE-2021-44228"]. */
  readonly aliases: readonly string[];
  readonly summary: string;
  readonly severity: Severity;
  /** Versions the advisory records as fixed (informational; audit never fixes). */
  readonly fixedVersions: readonly string[];
  readonly url: string;
}

export interface PackageAdvisories {
  readonly coordinates: Coordinates;
  readonly advisories: readonly Advisory[];
}

export interface AuditReport {
  /** How many distinct package versions were scanned. */
  readonly scanned: number;
  /** Only the vulnerable packages, worst severity first. */
  readonly vulnerable: readonly PackageAdvisories[];
  /** Vulnerability counts per severity (across all vulnerable packages). */
  readonly counts: Record<Severity, number>;
}

/** A source of vulnerability data (OSV by default; pluggable). */
export interface AuditSource {
  readonly name: string;
  /** The advisories for each coordinate, keyed by "group:artifact:version". */
  findVulnerabilities(
    coordinates: readonly Coordinates[],
  ): Promise<Map<CoordinateString, Advisory[]>>;
}

export type { Coordinates, CoordinateString };
