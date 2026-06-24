// `cappu outdated`: report every declared dependency that has a newer published
// stable version, showing the current version, the newest in-major version
// (`wanted`, what `cappu update` would move to) and the newest overall (`latest`,
// which may be a major bump). Read-only - it never edits cappu.json or the lock.

import { type CappuConfig } from "../config.ts";
import { type OutdatedDependency, planOutdated } from "../install.ts";
import { type PackageSource } from "../packages/index.ts";
import { emitAnnotation } from "./annotations.ts";

/** Render the outdated rows as an aligned table (or "" when nothing is outdated). */
export function formatOutdated(rows: readonly OutdatedDependency[]): string {
  if (rows.length === 0) return "";
  const header = ["dependency", "current", "wanted", "latest", "configuration"];
  const cells = rows.map(r => [
    r.key,
    r.current,
    r.wanted ?? r.current,
    r.latest ?? r.wanted ?? r.current,
    r.configuration,
  ]);
  const widths = header.map((h, i) => Math.max(h.length, ...cells.map(c => c[i]!.length)));
  const line = (cols: string[]): string => cols.map((c, i) => c.padEnd(widths[i]!)).join("  ").trimEnd();
  return [line(header), ...cells.map(line)].join("\n") + "\n";
}

export async function runOutdated(
  config: CappuConfig,
  // Injectable for tests; defaults to the configured remote repositories.
  sources?: readonly PackageSource[],
): Promise<never> {
  if (!config.fromFile) {
    process.stderr.write("cappu: no cappu.json found - run `cappu init` first\n");
    emitAnnotation("error", "no cappu.json found - run `cappu init` first");
    process.exit(1);
  }

  let rows: OutdatedDependency[];
  try {
    rows = await planOutdated(config, sources);
  } catch (e) {
    process.stderr.write(`cappu: outdated failed: ${(e as Error).message}\n`);
    emitAnnotation("error", `outdated failed: ${(e as Error).message}`);
    process.exit(2);
  }

  if (rows.length === 0) {
    process.stdout.write("all dependencies are up to date\n");
    process.exit(0);
  }
  process.stdout.write(formatOutdated(rows));
  process.exit(0);
}
