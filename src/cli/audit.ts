// `cappu audit`: scan the resolved dependencies for known vulnerabilities
// (OSV.dev), grouped by severity and coloured like npm. No fixing. Exits
// non-zero when anything is found.

import { styleText } from "node:util";

import {
  type AuditReport,
  type AuditSource,
  OsvSource,
  type Severity,
  SEVERITY_ORDER,
  auditPackages,
} from "../audit/index.ts";
import { type CappuConfig } from "../config.ts";
import {
  configuredRoots,
  configuredSources,
  lockedCoordinates,
  processorRoots,
  testRoots,
} from "../install.ts";
import { type Coordinates, coordinatesToString, resolveTransitive } from "../packages/index.ts";
import { colorEnabled } from "./color.ts";

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
  source: AuditSource = new OsvSource(),
): Promise<never> {
  const color = colorEnabled(process.stdout.isTTY);
  const paint = (format: StyleFormat, text: string): string =>
    color ? styleText(format, text, { stream: process.stdout }) : text;

  // The locked set is the resolved truth; without a lock, resolve on the fly
  // so audit works before the first install.
  let coordinates = lockedCoordinates(config);
  if (coordinates === undefined) {
    process.stderr.write("no cappu-lock.json; resolving dependencies to audit...\n");
    const roots = [...configuredRoots(config), ...processorRoots(config), ...testRoots(config)];
    const resolution = await resolveTransitive(roots, configuredSources(config));
    coordinates = resolution.packages.map(p => p.coordinates) as Coordinates[];
  }

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
      process.stdout.write(`    ${a.url}\n`);
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
