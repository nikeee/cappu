// Semver bumping for `cappu version` (npm-style major/minor/patch). Pure; the
// CLI writes the result back to cappu.json and tags.

export type ReleaseType = "major" | "minor" | "patch";

export const RELEASE_TYPES: readonly ReleaseType[] = ["major", "minor", "patch"];

/**
 * The next version after a major/minor/patch release. The core MAJOR.MINOR.PATCH
 * is bumped and any pre-release / build metadata is dropped (a release is a
 * clean version). Unlike `npm version`, a pre-release bumps its core too:
 * 1.2.3-SNAPSHOT -> patch -> 1.2.4 (npm would yield 1.2.3).
 */
export function bumpSemver(version: string, release: ReleaseType): string {
  const match = /^(\d+)\.(\d+)\.(\d+)/.exec(version);
  if (!match) throw new Error(`not a semver version: ${version}`);
  let major = Number(match[1]);
  let minor = Number(match[2]);
  let patch = Number(match[3]);
  if (release === "major") {
    major += 1;
    minor = 0;
    patch = 0;
  } else if (release === "minor") {
    minor += 1;
    patch = 0;
  } else {
    patch += 1;
  }
  return `${major}.${minor}.${patch}`;
}
