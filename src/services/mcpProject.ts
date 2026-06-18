// MCP project tools: read-only package-management queries (audit, licenses,
// search) over the project's resolved dependency graph. Unlike the semantic
// tools in mcp.ts these are async and config-driven (they resolve dependencies
// from the configured sources, not the Java program), and they mirror the
// structured output of `cappu audit --json` / `cappu licenses --json` /
// `cappu search`. Sources are injectable so the transport can pass real network
// sources while tests pass in-memory ones.

import {
  auditPackages,
  type AuditSource,
  cachedFetchJson,
  OsvSource,
  type Severity,
} from "../audit/index.ts";
import type { CappuConfig } from "../config.ts";
import { configuredRoots, configuredSources, processorRoots, testRoots } from "../install.ts";
import {
  coordinatesToString,
  dependencyPath,
  packageKey,
  type PackageSource,
  type ResolvedPackage,
  resolveTransitive,
  searchPackages,
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

export interface ProjectTools {
  audit(): Promise<McpAuditReport>;
  licenses(): Promise<{ licenses: McpLicenseRow[] }>;
  searchPackages(args: { query: string }): Promise<{ matches: string[] }>;
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

  return { audit, licenses, searchPackages: search };
}
