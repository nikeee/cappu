// `cappu verify`: check the jars currently in the lib directories against the
// SHA-256 sums in cappu-lock.json. Read-only; exits non-zero on any mismatch
// or missing jar.

import type { CappuConfig } from "../config.ts";
import { verifyInstalled } from "../install.ts";
import { emitAnnotation } from "./annotations.ts";

export function runVerify(config: CappuConfig): never {
  const result = verifyInstalled(config);
  if (!result.fromLock) {
    // A dep-free project with no lock is vacuously verified (matching
    // `install --locked`), not an error.
    const declared = Object.values(config.dependencies).some(m => Object.keys(m).length > 0);
    if (!declared) {
      process.stderr.write("0 ok, 0 modified, 0 missing\n");
      process.exit(0);
    }
    process.stderr.write(
      "cappu: no cappu-lock.json to verify against; run `cappu install` first\n",
    );
    emitAnnotation("error", "no cappu-lock.json to verify against; run `cappu install` first");
    process.exit(1);
  }
  for (const id of result.modified) {
    process.stderr.write(`error: ${id}: installed jar does not match cappu-lock.json\n`);
    emitAnnotation("error", `${id}: installed jar does not match cappu-lock.json`);
  }
  for (const id of result.missing) {
    process.stderr.write(`error: ${id}: locked but not installed (run \`cappu install\`)\n`);
    emitAnnotation("error", `${id}: locked but not installed (run \`cappu install\`)`);
  }
  process.stderr.write(
    `${result.ok.length} ok, ${result.modified.length} modified, ${result.missing.length} missing\n`,
  );
  process.exit(result.modified.length + result.missing.length > 0 ? 1 : 0);
}
