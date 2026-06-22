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
  dependencyPath,
  packageKey,
  type ResolvedPackage,
  resolveTransitive,
} from "../packages/index.ts";
import { colorEnabled } from "./color.ts";
import { emitAnnotation } from "./annotations.ts";
import { warnUnmappedLicenses } from "./licenses.ts";
import { painter } from "./style.ts";

type StyleFormat = Parameters<typeof styleText>[0];

// npm's palette; only applied when stdout is a colour-capable TTY.
const SEVERITY_STYLE: Record<Severity, StyleFormat> = {
  critical: ["bold", "red"],
  high: "red",
  moderate: "yellow",
  low: "cyan",
  unknown: "dim",
};

export async function runAudit(
  config: CappuConfig,
  // --no-cache: ignore the metadata and OSV detail caches for a fresh scan.
  // --json: emit the findings machine-readable instead of the coloured report.
  options: { noCache?: boolean; json?: boolean } = {},
  // The CVE source; defaults to OSV over a fetcher that caches vuln details on
  // disk (skipped under --no-cache so the scan is fully fresh).
  source: AuditSource = new OsvSource(options.noCache ? undefined : cachedFetchJson()),
): Promise<never> {
  const color = colorEnabled(process.stdout.isTTY);
  const paint = (format: StyleFormat, text: string): string =>
    color ? styleText(format, text, { stream: process.stdout }) : text;

  // Resolve the whole graph (not just the locked coordinate list): the
  // requestedBy edges are what let us show why a transitive package is here.
  let resolving = 0;
  const resolution = await resolveTransitive(
    [...configuredRoots(config), ...processorRoots(config), ...testRoots(config)],
    configuredSources(config, { cache: !options.noCache }),
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

  // Nothing resolved means there were no declared dependencies (no cappu.json,
  // or empty dependency configurations) - warn so a clean report here is not
  // mistaken for "scanned and found nothing".
  if (coordinates.length === 0) {
    const warn = painter(process.stderr);
    process.stderr.write(
      `${warn("yellow", "warning:")} no dependencies to scan ` +
        `(no cappu.json or empty dependencies)\n`,
    );
  }

  let report: AuditReport;
  try {
    report = await auditPackages(coordinates, source);
  } catch (e) {
    process.stderr.write(`cappu: audit failed: ${(e as Error).message}\n`);
    emitAnnotation("error", `audit failed: ${(e as Error).message}`);
    process.exit(2);
  }

  if (options.json) {
    const output = {
      scanned: report.scanned,
      counts: report.counts,
      vulnerable: report.vulnerable.map(p => ({
        coordinate: coordinatesToString(p.coordinates),
        path: dependencyPath(byKey, p.coordinates).map(coordinatesToString),
        advisories: p.advisories.map(a => ({
          id: a.id,
          aliases: a.aliases,
          severity: a.severity,
          summary: a.summary,
          fixedVersions: a.fixedVersions,
          url: a.url,
        })),
      })),
    };
    process.stdout.write(`${JSON.stringify(output, null, 2)}\n`);
    process.exit(report.vulnerable.length > 0 ? 1 : 0);
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
