// `cappu version major|minor|patch`: bump the version in cappu.json (semver,
// comments preserved). When cappu.json sits at the root of a git repository,
// also commit the bump and create a `v<version>` tag - like `npm version`.

import { execFileSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";

import { parse, stringify } from "comment-json";

import { type CappuConfig, DEFAULT_CONFIG_NAME } from "../config.ts";
import { bumpSemver, RELEASE_TYPES, type ReleaseType } from "../version.ts";
import { painter } from "./style.ts";

/** Set "version" in the JSONC config text, comments intact. */
function setVersionInJsonc(text: string, version: string): string {
  const root = parse(text) as Record<string, unknown> | null;
  if (root === null || typeof root !== "object") {
    throw new Error("the config file does not contain an object");
  }
  root.version = version;
  return `${stringify(root, null, 2)}\n`;
}

/** The git repository root containing `cwd`, or undefined when not in a repo. */
function gitToplevel(cwd: string): string | undefined {
  try {
    return execFileSync("git", ["rev-parse", "--show-toplevel"], {
      cwd,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "ignore"],
    }).trim();
  } catch {
    return undefined;
  }
}

export async function runVersion(
  release: string | undefined,
  configPathArg: string | undefined,
  config: CappuConfig,
): Promise<never> {
  const out = painter(process.stdout);
  const err = painter(process.stderr);

  if (!RELEASE_TYPES.includes(release as ReleaseType)) {
    process.stderr.write(`cappu: version needs one of: ${RELEASE_TYPES.join(", ")}\n`);
    process.exit(2);
  }
  if (!config.fromFile) {
    process.stderr.write(
      `${err("red", "error:")} no cappu.json found - run \`cappu init\` first\n`,
    );
    process.exit(1);
  }
  if (!config.version) {
    process.stderr.write(`${err("red", "error:")} cappu.json has no "version" to bump\n`);
    process.exit(1);
  }

  const next = bumpSemver(config.version, release as ReleaseType);
  const tag = `v${next}`;
  const configPath = configPathArg
    ? resolve(configPathArg)
    : join(config.baseDir, DEFAULT_CONFIG_NAME);
  writeFileSync(configPath, setVersionInJsonc(readFileSync(configPath, "utf8"), next));
  process.stdout.write(`${out("green", tag)}\n`);

  // Commit + tag only when cappu.json is at the git repository root (npm-style).
  const toplevel = gitToplevel(config.baseDir);
  if (toplevel === undefined) return process.exit(0); // not a git repo: bump only
  if (resolve(toplevel) !== resolve(config.baseDir)) {
    process.stderr.write(err("dim", "not the git repository root - bumped cappu.json only\n"));
    process.exit(0);
  }
  try {
    // Path-limited commit: only the cappu.json change, never other working edits.
    execFileSync("git", ["commit", "-m", tag, "--", configPath], {
      cwd: config.baseDir,
      stdio: ["ignore", "pipe", "pipe"],
    });
    execFileSync("git", ["tag", tag], { cwd: config.baseDir, stdio: ["ignore", "pipe", "pipe"] });
    process.stderr.write(err("dim", `committed and tagged ${tag}\n`));
  } catch (e) {
    process.stderr.write(
      `${err("yellow", "warning:")} could not commit/tag ${tag}: ${(e as Error).message}\n`,
    );
  }
  process.exit(0);
}
