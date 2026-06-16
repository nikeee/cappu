// `cappu audit`: scan the resolved dependencies (transitive included) for
// known vulnerabilities (OSV.dev), grouped by severity and coloured like npm.
// For every finding it prints the dependency tree path that pulls the
// vulnerable package in. No fixing. Exits non-zero when anything is found.

import { styleText } from "node:util";

import {
  type AuditReport,
  type AuditSource,
  OsvSource,
  type Severity,
  SEVERITY_ORDER,
  auditPackages,
  cachedFetchJson,
} from "../audit/index.ts";
import { type CappuConfig } from "../config.ts";
import { configuredRoots, configuredSources, processorRoots, testRoots } from "../install.ts";
import {
  type Coordinates,
  coordinatesToString,
  packageKey,
  type ResolvedPackage,
  resolveTransitive,
} from "../packages/index.ts";
import { colorEnabled } from "./color.ts";
import { warnUnmappedLicenses } from "./licenses.ts";

type StyleFormat = Parameters<typeof styleText>[0];

// npm's palette; only applied when stdout is a colour-capable TTY.
const SEVERITY_STYLE: Record<Severity, StyleFormat> = {
  critical: ["bold", "red"],
  high: "red",
  moderate: "yellow",
  low: "cyan",
  unknown: "dim",
};

/**
 * The chain of coordinates from a declared root down to `target`, following
 * each resolved package's `requestedBy` edge (nearest-wins records one parent
 * per package). Returns [root, ..., target]; just [target] for a direct
 * dependency, and is cycle-guarded.
 */
function dependencyPath(
  byKey: ReadonlyMap<string, ResolvedPackage>,
  target: Coordinates,
): Coordinates[] {
  const path: Coordinates[] = [];
  const seen = new Set<string>();
  let current: Coordinates | undefined = target;
  while (current) {
    const key = packageKey(current);
    if (seen.has(key)) break;
    seen.add(key);
    path.unshift(current);
    current = byKey.get(key)?.requestedBy;
  }
  return path;
}

export async function runAudit(
  config: CappuConfig,
  // The OSV source over a fetcher that caches immutable vuln details on disk.
  source: AuditSource = new OsvSource(cachedFetchJson()),
): Promise<never> {
  const color = colorEnabled(process.stdout.isTTY);
  const paint = (format: StyleFormat, text: string): string =>
    color ? styleText(format, text, { stream: process.stdout }) : text;

  // Resolve the whole graph (not just the locked coordinate list): the
  // requestedBy edges are what let us show why a transitive package is here.
  let resolving = 0;
  const resolution = await resolveTransitive(
    [...configuredRoots(config), ...processorRoots(config), ...testRoots(config)],
    configuredSources(config),
    () => {
      if (colorEnabled(process.stderr.isTTY)) {
        process.stderr.write(`\r\x1b[2Kresolving dependency graph (${++resolving})...`);
      }
    },
  );
  if (resolving > 0) process.stderr.write("\r\x1b[2K");
  const byKey = new Map<string, ResolvedPackage>();
  for (const p of resolution.packages) byKey.set(packageKey(p.coordinates), p);
  const coordinates = resolution.packages.map(p => p.coordinates);
  warnUnmappedLicenses(resolution.packages);

  let report: AuditReport;
  try {
    report = await auditPackages(coordinates, source);
  } catch (e) {
    process.stderr.write(`cappu: audit failed: ${(e as Error).message}\n`);
    process.exit(2);
  }

  if (report.vulnerable.length === 0) {
    process.stdout.write(
      `${paint("green", "found no known vulnerabilities")} in ${report.scanned} packages\n`,
    );
    process.exit(0);
  }

  // The dependency path that introduces a vulnerable package, as an indented
  // tree branch (root at the left, the vulnerable package deepest).
  const printTree = (target: Coordinates): void => {
    const path = dependencyPath(byKey, target);
    path.forEach((c, i) => {
      const label = coordinatesToString(c);
      const line = i === path.length - 1 ? paint(["bold", "red"], label) : paint("dim", label);
      process.stdout.write(`    ${"  ".repeat(i)}${line}\n`);
    });
  };

  // Grouped worst-first: a severity header, then its findings.
  for (const severity of SEVERITY_ORDER) {
    const inBucket = report.vulnerable.flatMap(p =>
      p.advisories
        .filter(a => a.severity === severity)
        .map(a => ({ coordinates: p.coordinates, a })),
    );
    if (inBucket.length === 0) continue;
    process.stdout.write(`\n${paint(SEVERITY_STYLE[severity], severity.toUpperCase())}\n`);
    for (const { coordinates: c, a } of inBucket) {
      const cve = a.aliases.length > 0 ? ` (${a.aliases.join(", ")})` : "";
      const fixed = a.fixedVersions.length > 0 ? `  [fixed in: ${a.fixedVersions.join(", ")}]` : "";
      process.stdout.write(`  ${coordinatesToString(c)}  ${a.id}${cve} - ${a.summary}${fixed}\n`);
      process.stdout.write(`    ${paint("dim", a.url)}\n`);
      printTree(c);
    }
  }

  const total = SEVERITY_ORDER.reduce((n, s) => n + report.counts[s], 0);
  const breakdown = SEVERITY_ORDER.filter(s => report.counts[s] > 0)
    .map(s => paint(SEVERITY_STYLE[s], `${report.counts[s]} ${s}`))
    .join(", ");
  process.stdout.write(
    `\n${total} ${total === 1 ? "vulnerability" : "vulnerabilities"} (${breakdown}) ` +
      `across ${report.vulnerable.length} of ${report.scanned} packages\n`,
  );
  process.exit(1);
}
