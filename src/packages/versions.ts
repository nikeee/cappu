// Version-spec matching for `cappu add`: a spec is an exact version or a
// leading-segment prefix ("2" matches 2.10.1 and 2-rc1, "2.1" matches 2.1.3
// but not 2.10.1). No ordering model beyond publish order: maven-metadata.xml
// lists versions oldest first, and "latest" means last in that list - the same
// assumption latestVersion has always made (Maven versions are not semver).

/** Whether `version` is `spec` itself or refines it segment-wise. */
export function matchesVersionSpec(spec: string, version: string): boolean {
  return version === spec || version.startsWith(`${spec}.`) || version.startsWith(`${spec}-`);
}

/**
 * The matching versions, newest (per publish order) first. No spec: all of
 * them, newest first.
 */
export function matchingVersions(versions: readonly string[], spec?: string): string[] {
  const matching =
    spec === undefined ? versions : versions.filter(v => matchesVersionSpec(spec, v));
  return matching.toReversed();
}
