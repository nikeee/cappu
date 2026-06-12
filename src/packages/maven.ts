// A PackageSource over a maven2 repository layout (e.g. the cappu.json
// "packageSources" default, Maven Central): maven-metadata.xml lists versions,
// the .pom carries the declared dependencies. POMs are parsed with
// fast-xml-parser and resolved EFFECTIVELY: Central serves the pom.xml as
// written, and multi-module projects version their dependencies through
// ${properties} and <dependencyManagement> defined up the <parent> chain - so
// getMetadata walks the parents (their coordinates are always literal),
// merges properties child-over-parent, interpolates, and fills missing
// dependency versions from the managed entries. BOM imports (scope=import)
// stay out of scope; whatever still lacks a version is dropped and flagged
// via `incomplete`. fetchText is injectable so everything is testable without
// a network.

import { XMLParser } from "fast-xml-parser";

import {
  type Coordinates,
  coordinatesToString,
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

const xml = new XMLParser({
  ignoreAttributes: true,
  // a single list entry must come back as the same shape as many; plain
  // <version> elements (dependency/parent/project) stay scalar
  isArray: (_name, jpath) =>
    String(jpath).endsWith("dependencies.dependency") || String(jpath).endsWith("versions.version"),
  parseTagValue: false, // versions like "1.0" stay strings
});

function asText(value: unknown): string | undefined {
  return typeof value === "string" && value !== "" ? value : undefined;
}

/** All versions from a maven-metadata.xml, oldest first (document order). */
export function parseMetadataVersions(text: string): string[] {
  const doc = xml.parse(text) as {
    metadata?: { versioning?: { versions?: { version?: unknown[] } } };
  };
  const versions = doc.metadata?.versioning?.versions?.version ?? [];
  return versions.map(v => String(v));
}

interface RawDependency {
  groupId?: string;
  artifactId?: string;
  version?: string;
  scope?: string;
  optional?: string | boolean;
}

/** One pom.xml as written: nothing inherited, nothing interpolated yet. */
export interface RawPom {
  parent?: Coordinates;
  properties: Record<string, string>;
  dependencies: RawDependency[];
  /** group:artifact -> raw version from <dependencyManagement>. */
  managed: Map<string, string>;
  description?: string;
}

export function parseRawPom(text: string): RawPom {
  const doc = xml.parse(text) as { project?: Record<string, unknown> };
  const project = doc.project ?? {};
  const parentNode = project.parent as Record<string, unknown> | undefined;
  const parent =
    parentNode &&
    asText(parentNode.groupId) &&
    asText(parentNode.artifactId) &&
    asText(parentNode.version)
      ? {
          groupId: asText(parentNode.groupId)!,
          artifactId: asText(parentNode.artifactId)!,
          version: asText(parentNode.version)!,
        }
      : undefined;

  const properties: Record<string, string> = {};
  const propertiesNode = project.properties as Record<string, unknown> | undefined;
  for (const [key, value] of Object.entries(propertiesNode ?? {})) {
    if (typeof value === "string" || typeof value === "number") properties[key] = String(value);
  }

  const dependencyList = (node: unknown): RawDependency[] =>
    ((node as { dependencies?: { dependency?: RawDependency[] } } | undefined)?.dependencies
      ?.dependency ?? []) as RawDependency[];

  const managed = new Map<string, string>();
  for (const dep of dependencyList(project.dependencyManagement)) {
    if (dep.groupId && dep.artifactId && asText(dep.version) && dep.scope !== "import") {
      managed.set(`${dep.groupId}:${dep.artifactId}`, dep.version!);
    }
  }

  return {
    parent,
    properties,
    dependencies: dependencyList(project),
    managed,
    description: asText(project.description),
  };
}

// ${name} interpolation over the merged property map plus the project
// builtins; nested property values resolve up to a small depth.
function interpolate(
  value: string,
  properties: Record<string, string>,
  project: Coordinates,
): string {
  let current = value;
  for (let depth = 0; depth < 5 && current.includes("${"); depth++) {
    current = current.replace(/\$\{([^}]+)\}/g, (whole, rawName: string) => {
      const name = rawName.trim();
      if (name === "project.version" || name === "version" || name === "pom.version") {
        return project.version;
      }
      if (name === "project.groupId" || name === "groupId" || name === "pom.groupId") {
        return project.groupId;
      }
      if (name === "project.artifactId") return project.artifactId;
      return properties[name] ?? whole;
    });
  }
  return current;
}

/**
 * The effective dependency list of a POM chain (child first, then its
 * parents): properties merged child-over-parent, versions interpolated and
 * filled from <dependencyManagement>.
 */
export function effectiveMetadata(
  chain: readonly RawPom[],
  coordinates: Coordinates,
): PackageMetadata & { incomplete: boolean } {
  // child first in `chain`: child values must win, so assign parent-last-first
  const properties: Record<string, string> = {};
  for (const pom of [...chain].reverse()) Object.assign(properties, pom.properties);

  const managed = new Map<string, string>();
  for (const pom of [...chain].reverse()) {
    for (const [key, version] of pom.managed) managed.set(key, version);
  }

  const child = chain[0];
  const dependencies: DependencyDeclaration[] = [];
  let incomplete = false;
  for (const dep of child?.dependencies ?? []) {
    const groupId = dep.groupId && interpolate(dep.groupId, properties, coordinates);
    const artifactId = dep.artifactId && interpolate(dep.artifactId, properties, coordinates);
    if (!groupId || !artifactId) continue;
    const raw = asText(dep.version) ?? managed.get(`${groupId}:${artifactId}`);
    const version = raw === undefined ? undefined : interpolate(raw, properties, coordinates);
    if (version === undefined || version.includes("${")) {
      incomplete = true; // unmanaged or beyond our property model (BOM import, ...)
      continue;
    }
    dependencies.push({
      groupId,
      artifactId,
      version,
      scope: asText(dep.scope),
      optional: dep.optional === "true" || dep.optional === true,
    });
  }
  return { coordinates, description: child?.description, dependencies, incomplete };
}

/**
 * Single-pom convenience used by tests and tooling: the effective view of one
 * POM without its parents (parent-managed versions stay unresolved).
 */
export function parsePom(
  text: string,
  coordinates: Coordinates,
): PackageMetadata & { incomplete: boolean } {
  return effectiveMetadata([parseRawPom(text)], coordinates);
}

const PARENT_CHAIN_LIMIT = 16; // generous; real chains are 2-4 deep

export class MavenRepositorySource implements PackageSource {
  readonly name: string;
  /** Fetched+parsed POMs (null: known miss), keyed by coordinates. */
  private readonly pomCache = new Map<string, RawPom | null>();

  constructor(
    private readonly baseUrl: string,
    private readonly fetchText: FetchText = defaultFetchText,
    private readonly fetchBytes: FetchBytes = defaultFetchBytes,
    /** A solr index service (search.maven.org style); repositories have none. */
    private readonly searchUrl?: string,
  ) {
    this.name = baseUrl;
  }

  /** The repository url for a path under the maven2 layout root. */
  private repositoryUrl(...segments: string[]): string {
    // the trailing slash keeps URL resolution relative to the layout root
    const base = this.baseUrl.endsWith("/") ? this.baseUrl : `${this.baseUrl}/`;
    return new URL(segments.join("/"), base).href;
  }

  private artifactPath(groupId: string, artifactId: string): string {
    return `${groupId.replaceAll(".", "/")}/${artifactId}`;
  }

  /** Free-text search via the index service; empty without one (or on errors). */
  async search(query: string): Promise<Coordinates[]> {
    if (this.searchUrl === undefined) return [];
    const url = new URL(this.searchUrl);
    url.search = new URLSearchParams({ q: query, rows: "20", wt: "json" }).toString();
    const text = await this.fetchText(url.href);
    if (text === undefined) return [];
    try {
      const doc = JSON.parse(text) as {
        response?: { docs?: { g?: string; a?: string; latestVersion?: string }[] };
      };
      return (doc.response?.docs ?? [])
        .filter(d => d.g && d.a && d.latestVersion)
        .map(d => ({ groupId: d.g!, artifactId: d.a!, version: d.latestVersion! }));
    } catch {
      return []; // a broken index answer must not fail the command
    }
  }

  async listVersions(groupId: string, artifactId: string): Promise<string[]> {
    const text = await this.fetchText(
      this.repositoryUrl(this.artifactPath(groupId, artifactId), "maven-metadata.xml"),
    );
    return text ? parseMetadataVersions(text) : [];
  }

  private async rawPom(coordinates: Coordinates): Promise<RawPom | undefined> {
    const key = coordinatesToString(coordinates);
    const cached = this.pomCache.get(key);
    if (cached !== undefined) return cached ?? undefined;
    const { groupId, artifactId, version } = coordinates;
    const text = await this.fetchText(
      this.repositoryUrl(
        this.artifactPath(groupId, artifactId),
        version,
        `${artifactId}-${version}.pom`,
      ),
    );
    const parsed = text === undefined ? null : parseRawPom(text);
    this.pomCache.set(key, parsed);
    return parsed ?? undefined;
  }

  async getMetadata(
    coordinates: Coordinates,
  ): Promise<(PackageMetadata & { incomplete: boolean }) | undefined> {
    const child = await this.rawPom(coordinates);
    if (!child) return undefined;
    // Walk the parent chain (coordinates in <parent> are always literal). A
    // missing parent just stops the walk: the effective view degrades to
    // whatever the fetched poms provide, and `incomplete` reports the rest.
    const chain: RawPom[] = [child];
    const seen = new Set<string>([coordinatesToString(coordinates)]);
    let parent = child.parent;
    while (parent && chain.length < PARENT_CHAIN_LIMIT) {
      const key = coordinatesToString(parent);
      if (seen.has(key)) break; // a cyclic chain must not loop
      seen.add(key);
      const pom = await this.rawPom(parent);
      if (!pom) break;
      chain.push(pom);
      parent = pom.parent;
    }
    return effectiveMetadata(chain, coordinates);
  }

  getArtifact(coordinates: Coordinates): Promise<Uint8Array | undefined> {
    const { groupId, artifactId, version } = coordinates;
    return this.fetchBytes(
      this.repositoryUrl(
        this.artifactPath(groupId, artifactId),
        version,
        `${artifactId}-${version}.jar`,
      ),
    );
  }
}
