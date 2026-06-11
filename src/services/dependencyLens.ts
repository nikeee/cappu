// Code lenses over the dependencies section of cappu.json: when a newer
// version of an entry is published, a lens above the line shows it. The
// parsing side is line-based and pure (testable without a server): in
// cappu.json, a dependency is the only kind of KEY containing a colon
// ("group:artifact"), so a `"g:a": "version"` pair identifies an entry -
// compilerOptions keys have no colon and packageSources urls are array
// elements, not keys.

/** One `"group:artifact": "version"` line of the dependencies section. */
export interface DependencyEntry {
  groupId: string;
  artifactId: string;
  version: string;
  /** 0-based line plus the character span of the `"key": "value"` text. */
  line: number;
  startCharacter: number;
  endCharacter: number;
}

const ENTRY = /^(\s*)("([^"\s:]+:[^"\s:]+)"\s*:\s*"([^"]*)")/;

export function findDependencyEntries(text: string): DependencyEntry[] {
  const entries: DependencyEntry[] = [];
  text.split("\n").forEach((lineText, line) => {
    if (lineText.trimStart().startsWith("//")) return; // commented-out entry
    const match = ENTRY.exec(lineText);
    if (!match) return;
    const [groupId = "", artifactId = ""] = match[3]!.split(":");
    entries.push({
      groupId,
      artifactId,
      version: match[4]!,
      line,
      startCharacter: match[1]!.length,
      endCharacter: match[1]!.length + match[2]!.length,
    });
  });
  return entries;
}

/** The newest published version of group:artifact, or undefined if unknown. */
export type LatestVersionLookup = (
  groupId: string,
  artifactId: string,
) => Promise<string | undefined>;

export interface DependencyLens {
  entry: DependencyEntry;
  /** Lens text, e.g. "newer version: 2.14.0". */
  title: string;
}

/** A lens per dependency whose newest published version differs from the entry. */
export async function dependencyLenses(
  text: string,
  lookup: LatestVersionLookup,
): Promise<DependencyLens[]> {
  const lenses: DependencyLens[] = [];
  for (const entry of findDependencyEntries(text)) {
    const latest = await lookup(entry.groupId, entry.artifactId);
    if (latest !== undefined && latest !== entry.version) {
      lenses.push({ entry, title: `newer version: ${latest}` });
    }
  }
  return lenses;
}
