// `cappu search <query>`: free-text search across the configured package
// sources (deduplicated by group:artifact, source order wins). Only sources
// with an index service answer; plain maven2 repositories are not searchable.

import { styleText } from "node:util";

import type { CappuConfig } from "../config.ts";
import { configuredSources } from "../install.ts";
import { type PackageSource, type SearchHit, searchPackages } from "../packages/index.ts";
import { colorEnabled } from "./color.ts";

type StyleFormat = Parameters<typeof styleText>[0];

/** The extra facts a hit may carry, as already-formatted display columns. */
function extraColumns(hit: SearchHit): string[] {
  const columns: string[] = [];
  if (hit.packaging) columns.push(hit.packaging);
  if (hit.versionCount !== undefined) {
    columns.push(`${hit.versionCount} version${hit.versionCount === 1 ? "" : "s"}`);
  }
  if (hit.lastUpdated !== undefined) {
    // epoch ms -> "YYYY-MM"; the day is noise for a "last published" hint
    columns.push(`updated ${new Date(hit.lastUpdated).toISOString().slice(0, 7)}`);
  }
  return columns;
}

export async function runSearch(
  query: string,
  config: CappuConfig,
  // --json: emit the hits machine-readable instead of one line per match.
  options: { json?: boolean } = {},
  sources: readonly PackageSource[] = configuredSources(config),
): Promise<never> {
  const hits = await searchPackages(query, sources);
  if (options.json) {
    const output = hits.map(h => ({
      groupId: h.groupId,
      artifactId: h.artifactId,
      version: h.version,
      packaging: h.packaging,
      versionCount: h.versionCount,
      lastUpdated: h.lastUpdated,
    }));
    process.stdout.write(`${JSON.stringify(output, null, 2)}\n`);
    process.exit(hits.length === 0 ? 1 : 0);
  }
  if (hits.length === 0) {
    process.stderr.write(`no packages found for '${query}'\n`);
    process.exit(1);
  }

  const color = colorEnabled(process.stdout.isTTY);
  const paint = (format: StyleFormat, text: string): string =>
    color ? styleText(format, text, { stream: process.stdout }) : text;

  // The summary line goes to stderr so stdout stays a clean, pipeable list.
  process.stderr.write(
    `found ${paint(["bold", "cyan"], String(hits.length))} package(s) for '${query}'\n`,
  );

  // Pad the coordinate and version columns to their widest entry so the
  // optional extra columns line up across rows.
  const coordinate = (h: SearchHit): string => `${h.groupId}:${h.artifactId}`;
  const coordinateWidth = Math.max(...hits.map(h => coordinate(h).length));
  const versionWidth = Math.max(...hits.map(h => h.version.length));

  for (const hit of hits) {
    const cells = [
      `  ${paint("bold", coordinate(hit).padEnd(coordinateWidth))}`,
      paint("cyan", hit.version.padEnd(versionWidth)),
    ];
    const extras = extraColumns(hit);
    if (extras.length > 0) cells.push(paint("dim", extras.join("  ")));
    process.stdout.write(`${cells.join("  ")}\n`);
  }
  process.exit(0);
}
