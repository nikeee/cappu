// `cappu cache clean`: remove the global download cache (packages, JDKs,
// resolved metadata). `cappu cache verify`: check the cached artifacts against
// the hashes recorded beside them. Other `cache` subcommands are rejected.

import { cleanCache } from "../cacheDir.ts";
import { verifyCache } from "../install.ts";

export function runCacheCommand(args: readonly string[]): never {
  if (args.length === 1 && args[0] === "clean") {
    let removed: string[];
    try {
      removed = cleanCache();
    } catch (e) {
      // An undeletable cache dir is a real error, not a stack trace (and not
      // the Go build's former silent success).
      process.stderr.write(`cappu: ${(e as Error).message}\n`);
      process.exit(1);
    }
    if (removed.length === 0) {
      process.stderr.write("cache already empty\n");
    } else {
      for (const dir of removed) process.stdout.write(`removed ${dir}\n`);
    }
    process.exit(0);
  }
  if (args.length === 1 && args[0] === "verify") {
    const result = verifyCache();
    for (const file of result.modified)
      process.stderr.write(`error: ${file}: cached bytes do not match the recorded hash\n`);
    for (const file of result.missing)
      process.stderr.write(`error: ${file}: a hash is recorded but the file is gone\n`);
    process.stderr.write(
      `${result.ok.length} ok, ${result.modified.length} modified, ${result.missing.length} missing\n`,
    );
    process.exit(result.modified.length + result.missing.length > 0 ? 1 : 0);
  }
  process.stderr.write("usage: cappu cache <clean|verify>\n");
  process.exit(2);
}
