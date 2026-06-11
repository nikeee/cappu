// `cappu install`: render the print-free installDependencies result - jars
// written, version conflicts (warnings), unresolvable packages (errors).

import type { CappuConfig } from "../config.ts";
import { installDependencies } from "../install.ts";

export async function runInstall(
  config: CappuConfig,
  options: { updateLock?: boolean } = {},
): Promise<never> {
  const result = await installDependencies(config, undefined, options);
  if (result.fromLock) {
    process.stderr.write("using cappu-lock.json\n");
  }
  if (result.fromStore.length > 0) {
    process.stderr.write(`${result.fromStore.length} package(s) from the local store\n`);
  }
  if (result.lockStale) {
    process.stderr.write(
      "warning: cappu.json's dependencies changed since cappu-lock.json was written;\n" +
        "         the locked set was installed anyway. Use `cappu add` (or delete the\n" +
        "         lock file) to re-resolve.\n",
    );
  }
  for (const file of result.installed) process.stdout.write(`${file}\n`);
  for (const c of result.resolution.conflicts) {
    process.stderr.write(
      `warning: ${c.key}: version ${c.rejected} (via ${c.rejectedBy.artifactId}) loses to ${c.selected}\n`,
    );
  }
  let failed = false;
  for (const m of result.resolution.missing) {
    const via = m.requestedBy ? ` (required by ${m.requestedBy.artifactId})` : "";
    process.stderr.write(
      `error: ${m.coordinates.groupId}:${m.coordinates.artifactId}:${m.coordinates.version}: not found in any package source${via}\n`,
    );
    failed = true;
  }
  for (const c of result.noArtifact) {
    process.stderr.write(`error: ${c}: source provided no jar\n`);
    failed = true;
  }
  for (const c of result.integrityFailures) {
    process.stderr.write(
      `error: ${c}: downloaded jar does not match the SHA-256 in cappu-lock.json\n`,
    );
    failed = true;
  }
  process.exit(failed ? 1 : 0);
}
