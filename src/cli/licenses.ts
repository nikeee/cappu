// `cappu licenses`: resolve the full dependency graph (compile + processor +
// test, transitive included) and print each package with the license it ships
// under - the best-effort SPDX id when one maps, otherwise the raw POM name.
// --json emits the same data machine-readable. Also exports the shared warning
// the resolving commands (install, audit) use for licenses with no SPDX id.

import { type CappuConfig } from "../config.ts";
import {
  compareStrings,
  configuredRoots,
  configuredSources,
  processorRoots,
  testRoots,
} from "../install.ts";
import {
  coordinatesToString,
  normalizeLicense,
  type PackageSource,
  type ResolvedPackage,
  resolveTransitive,
} from "../packages/index.ts";
import { colorEnabled } from "./color.ts";
import { painter } from "./style.ts";

export function warnUnmappedLicenses(
  packages: readonly ResolvedPackage[],
  stream: NodeJS.WriteStream = process.stderr,
): void {
  const paint = painter(stream);
  for (const pkg of packages) {
    for (const license of pkg.metadata.licenses ?? []) {
      if (normalizeLicense(license.name, license.url) !== undefined) continue;
      stream.write(
        `${paint("yellow", "warning:")} ${coordinatesToString(pkg.coordinates)}: ` +
          `license ${JSON.stringify(license.name)} has no SPDX mapping\n`,
      );
    }
  }
}

interface LicenseRow {
  coordinate: string;
  /** Raw licenses as declared in the POM, each with its best-effort SPDX id
   * (null when the name/url maps to nothing). */
  licenses: { name: string; url?: string; spdx: string | null }[];
}

export async function runLicenses(
  config: CappuConfig,
  options: { json?: boolean } = {},
  sources: readonly PackageSource[] = configuredSources(config),
): Promise<never> {
  let resolving = 0;
  let resolution;
  try {
    resolution = await resolveTransitive(
      [...configuredRoots(config), ...processorRoots(config), ...testRoots(config)],
      sources,
      () => {
        if (colorEnabled(process.stderr.isTTY)) {
          process.stderr.write(`\r\x1b[2Kresolving dependency graph (${++resolving})...`);
        }
      },
    );
  } catch (e) {
    // A resolution/network failure is a clean error, not a stack trace (Go parity).
    if (resolving > 0) process.stderr.write("\r\x1b[2K");
    process.stderr.write(`cappu: ${(e as Error).message}\n`);
    process.exit(1);
  }
  if (resolving > 0) process.stderr.write("\r\x1b[2K");

  const rows: LicenseRow[] = resolution.packages
    .map(p => ({
      coordinate: coordinatesToString(p.coordinates),
      licenses: (p.metadata.licenses ?? []).map(l => ({
        name: l.name,
        ...(l.url ? { url: l.url } : {}),
        spdx: normalizeLicense(l.name, l.url) ?? null,
      })),
    }))
    .sort((a, b) => compareStrings(a.coordinate, b.coordinate));

  if (options.json) {
    // The project's own license appears in the human output; include it here too
    // (as a leading row) so --json carries the same information.
    const json: LicenseRow[] = config.license
      ? [
          {
            coordinate: "this project",
            licenses: [{ name: config.license, spdx: normalizeLicense(config.license) ?? null }],
          },
          ...rows,
        ]
      : rows;
    process.stdout.write(`${JSON.stringify(json, null, 2)}\n`);
    process.exit(0);
  }

  const out = painter(process.stdout);
  if (config.license) {
    process.stdout.write(
      `${out("dim", "this project:")} ${out(["bold", "cyan"], config.license)}\n`,
    );
  }
  const width = rows.reduce((w, r) => Math.max(w, r.coordinate.length), 0);
  for (const r of rows) {
    const spdx = [...new Set(r.licenses.map(l => l.spdx).filter((s): s is string => s !== null))];
    const label =
      spdx.length > 0
        ? out("cyan", spdx.join(", "))
        : r.licenses.length > 0
          ? out("yellow", `${r.licenses.map(l => l.name).join(", ")} (no SPDX id)`)
          : out("dim", "no license declared");
    process.stdout.write(`${r.coordinate.padEnd(width)}  ${label}\n`);
  }
  process.exit(0);
}
