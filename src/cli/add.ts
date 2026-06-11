// `cappu add <configuration> <group:artifact[@version]>`: write the entry into
// the cappu.json dependencies section (preserving comments - the file is
// JSONC) and then download it and its transitive dependencies exactly like
// `cappu install`. Without @version, the newest published version is used.

import { readFileSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";

import { parse, stringify } from "comment-json";

import { type CappuConfig, DEFAULT_CONFIG_NAME, loadConfig } from "../config.ts";
import { latestVersion, MavenRepositorySource } from "../packages/index.ts";
import { runInstall } from "./install.ts";

const CONFIGURATIONS = ["api", "implementation"] as const;
type Configuration = (typeof CONFIGURATIONS)[number];

export interface AddCoordinate {
  /** "group:artifact" - the dependencies-section key. */
  key: string;
  /** Explicit version, or undefined to use the newest published one. */
  version?: string;
}

/** Parse "group:artifact[@version]", or undefined if it is not that shape. */
export function parseAddCoordinate(spec: string): AddCoordinate | undefined {
  const at = spec.indexOf("@");
  const key = at < 0 ? spec : spec.slice(0, at);
  const version = at < 0 ? undefined : spec.slice(at + 1);
  if (at >= 0 && !version) return undefined;
  const segments = key.split(":");
  if (segments.length !== 2 || segments.some(s => s === "")) return undefined;
  return { key, version };
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
  spec: string | undefined,
  configPathArg: string | undefined,
  config: CappuConfig,
): Promise<never> {
  const configuration = CONFIGURATIONS.find(c => c === configurationArg);
  const coordinate = spec === undefined ? undefined : parseAddCoordinate(spec);
  if (!configuration || !coordinate) {
    process.stderr.write(
      "usage: cappu add <api|implementation> <group:artifact[@version]>\n" +
        "e.g.:  cappu add implementation com.google.code.gson:gson@2.14.0\n",
    );
    process.exit(2);
  }
  if (!config.fromFile) {
    process.stderr.write("cappu: no cappu.json found - run `cappu init` first\n");
    process.exit(1);
  }

  let version = coordinate.version;
  if (version === undefined) {
    const [groupId = "", artifactId = ""] = coordinate.key.split(":");
    const sources = config.packageSources.map(url => new MavenRepositorySource(url));
    version = await latestVersion(groupId, artifactId, sources);
    if (version === undefined) {
      process.stderr.write(
        `cappu: no published version of ${coordinate.key} found in any package source\n`,
      );
      process.exit(1);
    }
  }

  const configPath = configPathArg
    ? resolve(configPathArg)
    : join(config.baseDir, DEFAULT_CONFIG_NAME);
  writeFileSync(
    configPath,
    addDependencyToJsonc(readFileSync(configPath, "utf8"), configuration, coordinate.key, version),
  );
  process.stderr.write(`added ${configuration} ${coordinate.key}@${version}\n`);

  // Re-read so install sees exactly what was written, then install everything
  // (the new entry plus whatever was already configured) like `cappu install`.
  return runInstall(loadConfig(configPath));
}
