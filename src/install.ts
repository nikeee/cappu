// `cappu install`: resolve the cappu.json dependencies section (api +
// implementation, transitively) against the configured packageSources and
// download every jar into the classPath's default lib/classes directory, where
// loadClassPath already picks them up. Print-free; the cli renders the result.

import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

import { type CappuConfig, resolveConfigPath } from "./config.ts";
import {
  type Coordinates,
  coordinatesToString,
  MavenRepositorySource,
  type PackageSource,
  type Resolution,
  resolveTransitive,
} from "./packages/index.ts";

export interface InstallResult {
  /** Jar paths written, in resolution order. */
  installed: string[];
  /** Resolved packages whose source could not provide a jar. */
  noArtifact: string[];
  resolution: Resolution;
  /** The directory the jars were written to. */
  targetDir: string;
}

/** "group:artifact" -> version entries of one configuration, as Coordinates. */
function rootsOf(entries: Record<string, string>): Coordinates[] {
  return Object.entries(entries).map(([key, version]) => {
    const [groupId = "", artifactId = ""] = key.split(":");
    return { groupId, artifactId, version };
  });
}

export async function installDependencies(
  config: CappuConfig,
  // Injectable for tests; defaults to the configured remote repositories.
  sources: readonly PackageSource[] = config.packageSources.map(
    url => new MavenRepositorySource(url),
  ),
): Promise<InstallResult> {
  // Only the api and implementation configurations exist so far; both are
  // needed at compile time, so install treats them alike.
  const roots = [
    ...rootsOf(config.dependencies.api),
    ...rootsOf(config.dependencies.implementation),
  ];
  const resolution = await resolveTransitive(roots, sources);

  const targetDir = resolveConfigPath(config, "./lib/classes");
  const installed: string[] = [];
  const noArtifact: string[] = [];
  if (resolution.packages.length > 0) mkdirSync(targetDir, { recursive: true });
  for (const pkg of resolution.packages) {
    const source = sources.find(s => s.name === pkg.source);
    const bytes = await source?.getArtifact?.(pkg.coordinates);
    if (!bytes) {
      noArtifact.push(coordinatesToString(pkg.coordinates));
      continue;
    }
    const file = join(targetDir, `${pkg.coordinates.artifactId}-${pkg.coordinates.version}.jar`);
    writeFileSync(file, bytes);
    installed.push(file);
  }
  return { installed, noArtifact, resolution, targetDir };
}
