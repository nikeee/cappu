// MCP project tools: read-only package-management queries (audit, licenses,
// search) over the project's resolved dependency graph. Unlike the semantic
// tools in mcp.ts these are async and config-driven (they resolve dependencies
// from the configured sources, not the Java program), and they return the same
// structured findings as the `cappu audit` / `cappu licenses` / `cappu search`
// CLI commands. Sources are injectable so the transport can pass real network
// sources while tests pass in-memory ones.

import {
  auditPackages,
  type AuditSource,
  cachedFetchJson,
  OsvSource,
  type Severity,
} from "../audit/index.ts";
import type { CappuConfig } from "../config.ts";
import {
  configuredRoots,
  configuredSources,
  planUpdates,
  processorRoots,
  testRoots,
} from "../install.ts";
import {
  type Coordinates,
  coordinatesToString,
  dependencyPath,
  latestVersion,
  packageKey,
  type PackageSource,
  type ResolvedPackage,
  resolveTransitive,
  searchPackages,
  toCoordinates,
} from "../packages/index.ts";

export interface McpAdvisory {
  id: string;
  aliases: string[];
  severity: Severity;
  summary: string;
  fixedVersions: string[];
  url: string;
}

export interface McpVulnerablePackage {
  coordinate: string;
  /** [root, ..., vulnerable package] - why it is in the tree. */
  path: string[];
  advisories: McpAdvisory[];
}

export interface McpAuditReport {
  scanned: number;
  counts: Record<Severity, number>;
  vulnerable: McpVulnerablePackage[];
}

export interface McpLicenseRow {
  coordinate: string;
  /** Raw licenses as declared in the POM. */
  licenses: { name: string; url?: string }[];
  /** Best-effort SPDX ids those map to (unmapped names dropped). */
  spdx: string[];
}

export interface McpOutdatedDependency {
  configuration: string;
  /** "group:artifact". */
  coordinate: string;
  from: string;
  to: string;
}

export interface McpTreeNode {
  coordinate: string;
  /** 0 for a declared root, deeper for transitive dependencies. */
  depth: number;
  /** The coordinate that pulled this one in (absent for roots). */
  requestedBy?: string;
}

export interface ProjectTools {
  audit(): Promise<McpAuditReport>;
  licenses(): Promise<{ licenses: McpLicenseRow[] }>;
  searchPackages(args: { query: string }): Promise<{ matches: string[] }>;
  outdated(): Promise<{ outdated: McpOutdatedDependency[] }>;
  latestVersion(args: { coord: string }): Promise<{ coordinate: string; latest?: string }>;
  /** Whole resolved graph, or - with `coord` - the path that introduces it. */
  dependencyTree(args: { coord?: string }): Promise<{ packages?: McpTreeNode[]; path?: string[] }>;
}

export interface ProjectToolDeps {
  /** Package sources to resolve/search against (default: the configured ones). */
  sources?: readonly PackageSource[];
  /** CVE source (default: OSV over a disk-caching fetcher). */
  auditSource?: AuditSource;
}

export function createProjectTools(config: CappuConfig, deps: ProjectToolDeps = {}): ProjectTools {
  const sources = deps.sources ?? configuredSources(config);
  const auditSource = deps.auditSource ?? new OsvSource(cachedFetchJson());

  // The whole graph (compile + processor + test, transitive), no progress
  // callback - the MCP client does not show a spinner.
  function resolveAll() {
    return resolveTransitive(
      [...configuredRoots(config), ...processorRoots(config), ...testRoots(config)],
      sources,
    );
  }

  async function audit(): Promise<McpAuditReport> {
    const resolution = await resolveAll();
    const byKey = new Map<string, ResolvedPackage>();
    for (const p of resolution.packages) byKey.set(packageKey(p.coordinates), p);
    const report = await auditPackages(
      resolution.packages.map(p => p.coordinates),
      auditSource,
    );
    return {
      scanned: report.scanned,
      counts: report.counts,
      vulnerable: report.vulnerable.map(p => ({
        coordinate: coordinatesToString(p.coordinates),
        path: dependencyPath(byKey, p.coordinates).map(coordinatesToString),
        advisories: p.advisories.map(a => ({
          id: a.id,
          aliases: [...a.aliases],
          severity: a.severity,
          summary: a.summary,
          fixedVersions: [...a.fixedVersions],
          url: a.url,
        })),
      })),
    };
  }

  async function licenses(): Promise<{ licenses: McpLicenseRow[] }> {
    const resolution = await resolveAll();
    const rows = resolution.packages
      .map(p => ({
        coordinate: coordinatesToString(p.coordinates),
        licenses: (p.metadata.licenses ?? []).map(l => ({
          name: l.name,
          ...(l.url ? { url: l.url } : {}),
        })),
        spdx: [...(p.metadata.licenseNormalized ?? [])],
      }))
      .sort((a, b) => a.coordinate.localeCompare(b.coordinate));
    return { licenses: rows };
  }

  async function search(args: { query: string }): Promise<{ matches: string[] }> {
    const hits = await searchPackages(args.query, sources);
    return { matches: hits.map(coordinatesToString) };
  }

  // Read-only preview of `cappu update`: the newest conflict-free stable bump
  // for each declared dependency. Does not write cappu.json.
  async function outdated(): Promise<{ outdated: McpOutdatedDependency[] }> {
    const bumps = await planUpdates(config, sources);
    return {
      outdated: bumps.map(b => ({
        configuration: b.configuration,
        coordinate: b.key,
        from: b.from,
        to: b.to,
      })),
    };
  }

  async function latest(args: { coord: string }): Promise<{ coordinate: string; latest?: string }> {
    const [groupId = "", artifactId = ""] = args.coord.split(":");
    const version = await latestVersion(groupId, artifactId, sources);
    return { coordinate: `${groupId}:${artifactId}`, ...(version ? { latest: version } : {}) };
  }

  async function dependencyTree(args: {
    coord?: string;
  }): Promise<{ packages?: McpTreeNode[]; path?: string[] }> {
    const resolution = await resolveAll();
    if (args.coord) {
      const [groupId = "", artifactId = "", version = ""] = args.coord.split(":");
      const byKey = new Map<string, ResolvedPackage>();
      for (const p of resolution.packages) byKey.set(packageKey(p.coordinates), p);
      const target: Coordinates = toCoordinates(groupId, artifactId, version);
      return { path: dependencyPath(byKey, target).map(coordinatesToString) };
    }
    return {
      packages: resolution.packages.map(p => ({
        coordinate: coordinatesToString(p.coordinates),
        depth: p.depth,
        ...(p.requestedBy ? { requestedBy: coordinatesToString(p.requestedBy) } : {}),
      })),
    };
  }

  return {
    audit,
    licenses,
    searchPackages: search,
    outdated,
    latestVersion: latest,
    dependencyTree,
  };
}
