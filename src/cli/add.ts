// `cappu add <configuration> <group:artifact[:version]>`: write the entry into
// the cappu.json dependencies section (preserving comments - the file is
// JSONC) and then download it and its transitive dependencies exactly like
// `cappu install`. The Gradle/Maven `group:artifact:version` form is used as-is,
// so a coordinate copied from a build.gradle just works. An absent or partial
// version ("2", "2.10") picks the newest matching version whose transitive
// resolution is conflict-free against the already-configured dependencies.

import { readFileSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";

import { parse, stringify } from "comment-json";

import { type CappuConfig, DEFAULT_CONFIG_NAME, loadConfig } from "../config.ts";
import { configuredSources, pickAddVersion } from "../install.ts";
import { type PackageKey, type PackageSource } from "../packages/index.ts";
import { runInstall } from "./install.ts";

const CONFIGURATIONS = [
  "api",
  "implementation",
  "annotationProcessor",
  "testImplementation",
] as const;
type Configuration = (typeof CONFIGURATIONS)[number];

export interface AddCoordinate {
  /** "group:artifact" - the dependencies-section key. */
  key: PackageKey;
  /** Explicit version, or undefined to use the newest published one. */
  version?: string;
}

/**
 * Parse the Gradle/Maven "group:artifact[:version]" form (so a coordinate
 * copied from a build file works as-is). Undefined if it is not that shape.
 */
export function parseAddCoordinate(spec: string): AddCoordinate | undefined {
  const segments = spec.split(":");
  if (segments.some(s => s === "")) return undefined;
  if (segments.length === 2) return { key: spec as PackageKey };
  if (segments.length === 3)
    return { key: `${segments[0]}:${segments[1]}` as PackageKey, version: segments[2] };
  return undefined;
}

/** Whether the written spec is already exact enough to skip the picker. */
function looksExact(version: string | undefined): version is string {
  // Heuristic: two dots (or a dash qualifier) is a full maven version; "2" or
  // "2.10" are prefixes to complete against the published list.
  return version !== undefined && (version.split(".").length >= 3 || version.includes("-"));
}

/**
 * Insert (or overwrite) the dependency in the JSONC config text. comment-json
 * round-trips the user's comments, which plain JSON.parse/stringify would eat.
 */
export function addDependencyToJsonc(
  text: string,
  configuration: Configuration,
  key: string,
  version: string,
): string {
  const root = parse(text) as Record<string, Record<string, Record<string, string>>> | null;
  if (root === null || typeof root !== "object") {
    throw new Error("the config file does not contain an object");
  }
  root.dependencies ??= {};
  root.dependencies[configuration] ??= {};
  root.dependencies[configuration][key] = version;
  return `${stringify(root, null, 2)}\n`;
}

export async function runAdd(
  configurationArg: string | undefined,
  specs: readonly string[],
  configPathArg: string | undefined,
  config: CappuConfig,
  // Injectable for tests; defaults to the configured remote repositories.
  sources?: readonly PackageSource[],
): Promise<never> {
  const configuration = CONFIGURATIONS.find(c => c === configurationArg);
  const coordinates = specs.map(parseAddCoordinate);
  const invalid = specs.filter((_, i) => coordinates[i] === undefined);
  if (!configuration || coordinates.length === 0 || invalid.length > 0) {
    for (const spec of invalid) process.stderr.write(`cappu: not a coordinate: '${spec}'\n`);
    process.stderr.write(
      `usage: cappu add <${CONFIGURATIONS.join("|")}> <group:artifact[:version]> [more...]\n` +
        "e.g.:  cappu add implementation com.google.code.gson:gson:2.14.0 org.slf4j:slf4j-api\n",
    );
    process.exit(2);
  }
  if (!config.fromFile) {
    process.stderr.write("cappu: no cappu.json found - run `cappu init` first\n");
    process.exit(1);
  }

  const configPath = configPathArg
    ? resolve(configPathArg)
    : join(config.baseDir, DEFAULT_CONFIG_NAME);
  const resolvedSources = sources ?? configuredSources(config);

  // Pick every version FIRST, against an in-memory config that accumulates the
  // earlier additions, so later picks stay compatible with them - but write
  // nothing to disk until all picks succeed. A failure mid-way (an
  // unresolvable coordinate) then leaves cappu.json untouched rather than
  // partially mutated.
  let working = config;
  const picks: { key: string; version: string }[] = [];
  for (const coordinate of coordinates as AddCoordinate[]) {
    let version = coordinate.version;
    if (!looksExact(version)) {
      const picked = await pickAddVersion(working, coordinate.key, version, resolvedSources);
      if (picked === undefined) {
        const wanted = version === undefined ? "" : ` matching '${version}'`;
        process.stderr.write(
          `cappu: no published version of ${coordinate.key}${wanted} found in any package source\n`,
        );
        process.exit(1);
      }
      if (!picked.compatible) {
        process.stderr.write(
          `warning: every matching version of ${coordinate.key} conflicts with the configured dependencies; using ${picked.version}\n`,
        );
      }
      version = picked.version;
    }
    picks.push({ key: coordinate.key, version });
    // thread the addition into the in-memory config for the next pick
    working = {
      ...working,
      dependencies: {
        ...working.dependencies,
        [configuration]: { ...working.dependencies[configuration], [coordinate.key]: version },
      },
    };
  }

  // All picks resolved: now write them in one go and install.
  let text = readFileSync(configPath, "utf8");
  for (const { key, version } of picks) {
    text = addDependencyToJsonc(text, configuration, key, version);
  }
  writeFileSync(configPath, text);
  for (const { key, version } of picks) {
    process.stderr.write(`added ${configuration} ${key}:${version}\n`);
  }

  // Re-resolve and rewrite the lock - install alone only consumes the lock.
  return runInstall(loadConfig(configPath), { updateLock: true });
}
