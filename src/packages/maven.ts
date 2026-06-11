// A PackageSource over a maven2 repository layout (e.g. the cappu.json
// "packageSources" default, Maven Central): maven-metadata.xml lists versions,
// the .pom carries the declared dependencies. The XML subset involved is flat
// and regular, so a small element scanner is enough - no XML dependency.
// fetchText is injectable so everything is testable without a network.

import {
  type Coordinates,
  type DependencyDeclaration,
  type PackageMetadata,
  type PackageSource,
} from "./types.ts";

/** Returns the body for a url, or undefined for a 404-ish miss. */
export type FetchText = (url: string) => Promise<string | undefined>;
/** Returns the bytes for a url, or undefined for a 404-ish miss. */
export type FetchBytes = (url: string) => Promise<Uint8Array | undefined>;

const defaultFetchText: FetchText = async url => {
  const response = await fetch(url);
  return response.ok ? response.text() : undefined;
};

const defaultFetchBytes: FetchBytes = async url => {
  const response = await fetch(url);
  return response.ok ? new Uint8Array(await response.arrayBuffer()) : undefined;
};

function elementText(xml: string, tag: string): string | undefined {
  return xml.match(new RegExp(`<${tag}>([^<]*)</${tag}>`))?.[1]?.trim();
}

function elements(xml: string, tag: string): string[] {
  return [...xml.matchAll(new RegExp(`<${tag}>([\\s\\S]*?)</${tag}>`, "g"))].map(m => m[1]!);
}

/** All versions from a maven-metadata.xml, oldest first (document order). */
export function parseMetadataVersions(xml: string): string[] {
  const versioning = elements(xml, "versioning")[0] ?? xml;
  const versions = elements(versioning, "versions")[0] ?? "";
  return elements(versions, "version").map(v => v.trim());
}

/**
 * The declared dependencies of a .pom. Versions referencing properties or
 * dependencyManagement (`${...}` or absent) are dropped: resolving them needs
 * parent-pom interpolation, which is out of scope here - the resolver reports
 * what it skipped via the `incomplete` flag.
 */
export function parsePom(
  xml: string,
  coordinates: Coordinates,
): PackageMetadata & { incomplete: boolean } {
  // Cut <dependencyManagement> so only the real <dependencies> block remains.
  const withoutManagement = xml.replace(
    /<dependencyManagement>[\s\S]*?<\/dependencyManagement>/g,
    "",
  );
  const block = elements(withoutManagement, "dependencies")[0] ?? "";
  const dependencies: DependencyDeclaration[] = [];
  let incomplete = false;
  for (const dep of elements(block, "dependency")) {
    const groupId = elementText(dep, "groupId");
    const artifactId = elementText(dep, "artifactId");
    const version = elementText(dep, "version");
    if (!groupId || !artifactId) continue;
    if (!version || version.includes("${")) {
      incomplete = true; // needs property/parent interpolation
      continue;
    }
    dependencies.push({
      groupId,
      artifactId,
      version,
      scope: elementText(dep, "scope"),
      optional: elementText(dep, "optional") === "true",
    });
  }
  return {
    coordinates,
    description: elementText(xml, "description"),
    dependencies,
    incomplete,
  };
}

export class MavenRepositorySource implements PackageSource {
  readonly name: string;

  constructor(
    private readonly baseUrl: string,
    private readonly fetchText: FetchText = defaultFetchText,
    private readonly fetchBytes: FetchBytes = defaultFetchBytes,
  ) {
    this.name = baseUrl;
  }

  private artifactDir(groupId: string, artifactId: string): string {
    return `${this.baseUrl.replace(/\/$/, "")}/${groupId.replaceAll(".", "/")}/${artifactId}`;
  }

  /** A maven2 repository has no search endpoint; searching needs an index service. */
  search(): Promise<Coordinates[]> {
    return Promise.resolve([]);
  }

  async listVersions(groupId: string, artifactId: string): Promise<string[]> {
    const xml = await this.fetchText(`${this.artifactDir(groupId, artifactId)}/maven-metadata.xml`);
    return xml ? parseMetadataVersions(xml) : [];
  }

  async getMetadata(coordinates: Coordinates): Promise<PackageMetadata | undefined> {
    const { groupId, artifactId, version } = coordinates;
    const dir = `${this.artifactDir(groupId, artifactId)}/${version}`;
    const pom = await this.fetchText(`${dir}/${artifactId}-${version}.pom`);
    return pom ? parsePom(pom, coordinates) : undefined;
  }

  getArtifact(coordinates: Coordinates): Promise<Uint8Array | undefined> {
    const { groupId, artifactId, version } = coordinates;
    const dir = `${this.artifactDir(groupId, artifactId)}/${version}`;
    return this.fetchBytes(`${dir}/${artifactId}-${version}.jar`);
  }
}
