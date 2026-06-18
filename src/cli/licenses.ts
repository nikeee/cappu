// `cappu licenses`: resolve the full dependency graph (compile + processor +
// test, transitive included) and print each package with the license it ships
// under - the best-effort SPDX id when one maps, otherwise the raw POM name.
// --json emits the same data machine-readable. Also exports the shared warning
// the resolving commands (install, audit) use for licenses with no SPDX id.

import { type CappuConfig } from "../config.ts";
import { configuredRoots, configuredSources, processorRoots, testRoots } from "../install.ts";
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
  /** Raw licenses as declared in the POM. */
  licenses: { name: string; url?: string }[];
  /** The best-effort SPDX ids those map to (unmapped names dropped). */
  spdx: string[];
}

export async function runLicenses(
  config: CappuConfig,
  options: { json?: boolean } = {},
  sources: readonly PackageSource[] = configuredSources(config),
): Promise<never> {
  let resolving = 0;
  const resolution = await resolveTransitive(
    [...configuredRoots(config), ...processorRoots(config), ...testRoots(config)],
    sources,
    () => {
      if (colorEnabled(process.stderr.isTTY)) {
        process.stderr.write(`\r\x1b[2Kresolving dependency graph (${++resolving})...`);
      }
    },
  );
  if (resolving > 0) process.stderr.write("\r\x1b[2K");

  const rows: LicenseRow[] = resolution.packages
    .map(p => ({
      coordinate: coordinatesToString(p.coordinates),
      licenses: (p.metadata.licenses ?? []).map(l => ({
        name: l.name,
        ...(l.url ? { url: l.url } : {}),
      })),
      spdx: [...(p.metadata.licenseNormalized ?? [])],
    }))
    .sort((a, b) => a.coordinate.localeCompare(b.coordinate));

  if (options.json) {
    process.stdout.write(`${JSON.stringify(rows, null, 2)}\n`);
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
    const label =
      r.spdx.length > 0
        ? out("cyan", r.spdx.join(", "))
        : r.licenses.length > 0
          ? out("yellow", `${r.licenses.map(l => l.name).join(", ")} (no SPDX id)`)
          : out("dim", "no license declared");
    process.stdout.write(`${r.coordinate.padEnd(width)}  ${label}\n`);
  }
  process.exit(0);
}
