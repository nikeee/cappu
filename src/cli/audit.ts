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
import pkg from "../../package.json" with { type: "json" };

type StyleFormat = Parameters<typeof styleText>[0];

/** Audit output formats. `text` is the human report; `sarif` is machine output
 * for code-scanning upload. New machine formats (e.g. `osv`) slot in here. */
export type AuditFormat = "text" | "sarif";

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
  // format: "text" (default human report) or "sarif" (machine output).
  options: { noCache?: boolean; format?: AuditFormat } = {},
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

  if ((options.format ?? "text") === "sarif") {
    const sarif = buildAuditSarif(report, byKey, pkg.version);
    process.stdout.write(`${JSON.stringify(sarif, null, 2)}\n`);
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

// SARIF level + GitHub "security-severity" score per bucket. We only have npm
// severity buckets, not CVSS scores, so the scores are representative midpoints
// (omitted for "unknown") - enough for the code-scanning Security tab to sort.
const SARIF_SEVERITY: Record<Severity, { level: string; score?: string }> = {
  critical: { level: "error", score: "9.0" },
  high: { level: "error", score: "7.0" },
  moderate: { level: "warning", score: "4.0" },
  low: { level: "note", score: "1.0" },
  unknown: { level: "note" },
};

/**
 * Build a SARIF 2.1.0 log for the audit report (GitHub code-scanning ingestible):
 * one rule per distinct advisory, one result per (package, advisory). Results
 * point at cappu.json - a vulnerable transitive package has no source line of
 * its own, but cappu.json is the file that (directly or transitively) declares
 * it. The dependency path is kept in result.properties for traceability.
 */
export function buildAuditSarif(
  report: AuditReport,
  byKey: Map<string, ResolvedPackage>,
  version: string,
): object {
  const rules = new Map<string, object>();
  const results: object[] = [];
  for (const p of report.vulnerable) {
    const coordinate = coordinatesToString(p.coordinates);
    const path = dependencyPath(byKey, p.coordinates).map(coordinatesToString);
    for (const a of p.advisories) {
      const { level, score } = SARIF_SEVERITY[a.severity];
      if (!rules.has(a.id)) {
        rules.set(a.id, {
          id: a.id,
          name: a.id,
          shortDescription: { text: a.summary || a.id },
          helpUri: a.url,
          properties: { tags: ["security"], ...(score ? { "security-severity": score } : {}) },
        });
      }
      const cve = a.aliases.length > 0 ? ` (${a.aliases.join(", ")})` : "";
      const fixed = a.fixedVersions.length > 0 ? ` Fixed in: ${a.fixedVersions.join(", ")}.` : "";
      results.push({
        ruleId: a.id,
        level,
        message: { text: `${coordinate} is affected by ${a.id}${cve}: ${a.summary}.${fixed}` },
        locations: [{ physicalLocation: { artifactLocation: { uri: "cappu.json" } } }],
        properties: { coordinate, severity: a.severity, path },
      });
    }
  }
  return {
    $schema: "https://json.schemastore.org/sarif-2.1.0.json",
    version: "2.1.0",
    runs: [
      {
        tool: {
          driver: {
            name: "cappu",
            informationUri: "https://github.com/nikeee/cappu",
            version,
            rules: [...rules.values()],
          },
        },
        results,
      },
    ],
  };
}
