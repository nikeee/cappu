// `cappu verify`: check the jars currently in the lib directories against the
// SHA-256 sums in cappu-lock.json. Read-only; exits non-zero on any mismatch
// or missing jar.

import type { CappuConfig } from "../config.ts";
import { verifyInstalled } from "../install.ts";

export function runVerify(config: CappuConfig): never {
  const result = verifyInstalled(config);
  if (!result.fromLock) {
    process.stderr.write(
      "cappu: no cappu-lock.json to verify against; run `cappu install` first\n",
    );
    process.exit(1);
  }
  for (const id of result.modified) {
    process.stderr.write(`error: ${id}: installed jar does not match cappu-lock.json\n`);
  }
  for (const id of result.missing) {
    process.stderr.write(`error: ${id}: locked but not installed (run \`cappu install\`)\n`);
  }
  process.stderr.write(
    `${result.ok.length} ok, ${result.modified.length} modified, ${result.missing.length} missing\n`,
  );
  process.exit(result.modified.length + result.missing.length > 0 ? 1 : 0);
}
