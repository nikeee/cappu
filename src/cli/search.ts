// `cappu search <query>`: free-text search across the configured package
// sources (deduplicated by group:artifact, source order wins). Only sources
// with an index service answer; plain maven2 repositories are not searchable.

import type { CappuConfig } from "../config.ts";
import { configuredSources } from "../install.ts";
import { type PackageSource, searchPackages } from "../packages/index.ts";

export async function runSearch(
  query: string,
  config: CappuConfig,
  sources: readonly PackageSource[] = configuredSources(config),
): Promise<never> {
  const hits = await searchPackages(query, sources);
  if (hits.length === 0) {
    process.stderr.write(`no packages found for '${query}'\n`);
    process.exit(1);
  }
  for (const hit of hits) {
    process.stdout.write(`${hit.groupId}:${hit.artifactId}@${hit.version}\n`);
  }
  process.exit(0);
}
