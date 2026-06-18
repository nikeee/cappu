// `cappu cache clean`: remove the global download cache (packages, JDKs,
// resolved metadata). Other `cache` subcommands are rejected.

import { cleanCache } from "../cacheDir.ts";

export function runCacheCommand(args: readonly string[]): never {
  if (args[0] !== "clean" || args.length > 1) {
    process.stderr.write("usage: cappu cache clean\n");
    process.exit(2);
  }
  const removed = cleanCache();
  if (removed.length === 0) {
    process.stderr.write("cache already empty\n");
  } else {
    for (const dir of removed) process.stdout.write(`removed ${dir}\n`);
  }
  process.exit(0);
}
