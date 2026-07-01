// `cappu show <group:artifact[:version]>`: a single-package detail card -
// coordinates, description, license, homepage/repo, published versions, the
// declared dependencies, how this project depends on it, and any known
// vulnerabilities (OSV). The npm/cargo `info`/`show` shape. --json emits the
// same data machine-readable (implied under an agent, like the other commands).

import type { CappuConfig } from "../config.ts";
import {
  type AuditSource,
  auditPackages,
  cachedFetchJson,
  OsvSource,
  type Severity,
  SEVERITY_ORDER,
} from "../audit/index.ts";
import { configuredSources, type ProjectContext, projectContext } from "../install.ts";
import {
  type Coordinates,
  type DependencyDeclaration,
  normalizeLicense,
  type PackageMetadata,
  type PackageSource,
  toCoordinates,
} from "../packages/index.ts";
import { type StyleFormat, painter } from "./style.ts";

const SEVERITY_STYLE: Record<Severity, StyleFormat> = {
  critical: ["bold", "red"],
  high: "red",
  moderate: "yellow",
  low: "cyan",
  unknown: "dim",
};

const LABEL_WIDTH = 13;

interface ShowAdvisory {
  readonly id: string;
  readonly aliases: readonly string[];
  readonly severity: Severity;
  readonly summary: string;
  readonly fixedVersions: readonly string[];
  readonly url: string;
}

/** Everything the card (and --json) shows, gathered once so the two can't drift. */
export interface ShowData {
  readonly groupId: string;
  readonly artifactId: string;
  readonly version: string;
  /** Whether the user pinned the version (vs. defaulting to latest). */
  readonly explicitVersion: boolean;
  readonly latestVersion: string | undefined;
  readonly versionCount: number;
  /** How many published versions are newer than the shown one. */
  readonly newer: number;
  readonly description: string | undefined;
  readonly homepage: string | undefined;
  readonly scmUrl: string | undefined;
  /** SPDX ids the licenses map to, else the raw POM names. */
  readonly spdx: readonly string[];
  readonly rawLicenses: readonly string[];
  readonly dependencies: readonly DependencyDeclaration[];
  readonly project: ProjectContext;
  readonly vulnerabilities: readonly ShowAdvisory[];
}

interface ShowError {
  readonly error: string;
  readonly code: number;
}

/** Split "group:artifact[:version]" into its parts, or undefined if malformed. */
function parseCoordinate(
  coord: string,
): { groupId: string; artifactId: string; version?: string } | undefined {
  const parts = coord.split(":");
  if (parts.length < 2 || parts.length > 3 || parts.some(p => p === "")) return undefined;
  const [groupId, artifactId, version] = parts;
  return { groupId: groupId!, artifactId: artifactId!, version };
}

/** The first source that lists any version of group:artifact wins (oldest first). */
async function listVersionsAcross(
  groupId: string,
  artifactId: string,
  sources: readonly PackageSource[],
): Promise<string[]> {
  for (const source of sources) {
    const versions = await source.listVersions(groupId, artifactId);
    if (versions.length > 0) return versions;
  }
  return [];
}

/** The first source that has metadata for these coordinates wins. */
async function metadataAcross(
  coordinates: Coordinates,
  sources: readonly PackageSource[],
): Promise<PackageMetadata | undefined> {
  for (const source of sources) {
    const metadata = await source.getMetadata(coordinates);
    if (metadata) return metadata;
  }
  return undefined;
}

/** Gather the full detail of one package, or an error to print (and its exit code). */
export async function buildShowData(
  coord: string,
  config: CappuConfig,
  sources: readonly PackageSource[],
  auditSource: AuditSource,
): Promise<ShowData | ShowError> {
  const parsed = parseCoordinate(coord);
  if (!parsed) {
    return {
      error: "show needs group:artifact[:version], e.g. `cappu show com.google.code.gson:gson`",
      code: 2,
    };
  }
  const { groupId, artifactId } = parsed;

  const versions = await listVersionsAcross(groupId, artifactId, sources);
  const latest = versions.at(-1);
  const version = parsed.version ?? latest;
  if (version === undefined) {
    return { error: `package not found: ${groupId}:${artifactId}`, code: 1 };
  }
  const coordinates = toCoordinates(groupId, artifactId, version);

  const metadata = await metadataAcross(coordinates, sources);
  if (metadata === undefined && versions.length === 0) {
    return { error: `package not found: ${groupId}:${artifactId}:${version}`, code: 1 };
  }

  // OSV scan of just this version; a network failure must not sink the card.
  let vulnerabilities: ShowAdvisory[] = [];
  try {
    const report = await auditPackages([coordinates], auditSource);
    vulnerabilities = (report.vulnerable[0]?.advisories ?? []).map(a => ({
      id: a.id,
      aliases: a.aliases,
      severity: a.severity,
      summary: a.summary,
      fixedVersions: a.fixedVersions,
      url: a.url,
    }));
    // OSV returns advisories in database order; show them worst severity first.
    vulnerabilities.sort(
      (a, b) => SEVERITY_ORDER.indexOf(a.severity) - SEVERITY_ORDER.indexOf(b.severity),
    );
  } catch {
    vulnerabilities = [];
  }

  const spdx = [
    ...new Set((metadata?.licenses ?? []).map(l => normalizeLicense(l.name, l.url))),
  ].filter(s => s !== undefined);

  return {
    groupId,
    artifactId,
    version,
    explicitVersion: parsed.version !== undefined,
    latestVersion: latest,
    versionCount: versions.length,
    newer: versions.includes(version) ? versions.length - 1 - versions.indexOf(version) : 0,
    // POM descriptions are often multi-line and indented; collapse to one line.
    description: metadata?.description?.replace(/\s+/g, " ").trim() || undefined,
    homepage: metadata?.homepage,
    scmUrl: metadata?.scmUrl,
    spdx,
    rawLicenses: (metadata?.licenses ?? []).map(l => l.name),
    dependencies: [...(metadata?.dependencies ?? [])].sort((a, b) =>
      `${a.groupId}:${a.artifactId}`.localeCompare(`${b.groupId}:${b.artifactId}`),
    ),
    project: projectContext(config, `${groupId}:${artifactId}`),
    vulnerabilities,
  };
}

/** The machine-readable view (one stable shape for --json). */
export function showToJson(data: ShowData): object {
  return {
    groupId: data.groupId,
    artifactId: data.artifactId,
    version: data.version,
    latestVersion: data.latestVersion ?? null,
    versionCount: data.versionCount,
    description: data.description ?? null,
    homepage: data.homepage ?? null,
    scmUrl: data.scmUrl ?? null,
    license: data.spdx.length > 0 ? data.spdx : data.rawLicenses,
    dependencies: data.dependencies.map(d => ({
      groupId: d.groupId,
      artifactId: d.artifactId,
      version: d.version,
      ...(d.scope ? { scope: d.scope } : {}),
      ...(d.optional ? { optional: true } : {}),
    })),
    project: {
      configurations: data.project.configurations,
      declared: data.project.declared ?? null,
      installed: data.project.installed ?? null,
    },
    vulnerabilities: data.vulnerabilities,
  };
}

type Paint = (format: StyleFormat, text: string) => string;

/** Render the colored detail card (paint is a no-op when color is disabled). */
export function renderShowCard(data: ShowData, paint: Paint): string {
  const lines: string[] = [];
  const row = (label: string, value: string): void => {
    lines.push(`  ${paint("dim", label.padEnd(LABEL_WIDTH))}${value}`);
  };

  // Header: coordinates + version, with a freshness hint from the version list.
  const hint =
    !data.explicitVersion || data.version === data.latestVersion
      ? paint("green", "latest")
      : data.newer > 0
        ? paint("yellow", `${data.newer} newer available`)
        : "";
  lines.push(
    `${paint(["bold", "cyan"], `${data.groupId}:${data.artifactId}`)} ${paint("bold", data.version)}` +
      (hint ? `  ${hint}` : ""),
  );
  if (data.description) lines.push(paint("dim", `  ${data.description}`));
  lines.push("");

  const licenseLabel =
    data.spdx.length > 0
      ? paint("cyan", data.spdx.join(", "))
      : data.rawLicenses.length > 0
        ? paint("yellow", `${data.rawLicenses.join(", ")} (no SPDX id)`)
        : paint("dim", "none declared");
  row("License", licenseLabel);
  if (data.homepage) row("Homepage", paint("blue", data.homepage));
  if (data.scmUrl) row("Repository", paint("blue", data.scmUrl));
  if (data.latestVersion !== undefined) {
    row(
      "Versions",
      `${data.latestVersion} ${paint("dim", "(latest)")}${paint("dim", `, ${data.versionCount} published`)}`,
    );
  }
  row("In project", formatProject(data.project, paint));

  lines.push("", `  ${paint("bold", `Dependencies (${data.dependencies.length})`)}`);
  if (data.dependencies.length === 0) {
    lines.push(`    ${paint("dim", "none")}`);
  } else {
    for (const d of data.dependencies) lines.push(`    ${formatDependency(d, paint)}`);
  }

  lines.push("", `  ${paint("bold", "Vulnerabilities")}`);
  if (data.vulnerabilities.length === 0) {
    lines.push(`    ${paint("green", "no known vulnerabilities")}`);
  } else {
    for (const a of data.vulnerabilities) {
      const cve = a.aliases.length > 0 ? ` (${a.aliases.join(", ")})` : "";
      const fixed =
        a.fixedVersions.length > 0
          ? paint("dim", `  [fixed in: ${a.fixedVersions.join(", ")}]`)
          : "";
      lines.push(
        `    ${paint(SEVERITY_STYLE[a.severity], a.severity.toUpperCase())}  ` +
          `${a.id}${cve} - ${a.summary}${fixed}`,
      );
      lines.push(`      ${paint("dim", a.url)}`);
    }
    lines.push(`    ${paint("dim", "run `cappu audit` to scan the whole dependency tree")}`);
  }

  return `${lines.join("\n")}\n`;
}

function formatProject(context: ProjectContext, paint: Paint): string {
  if (context.configurations.length === 0) return paint("dim", "not a direct dependency");
  const where = context.configurations.join(", ");
  const parts: string[] = [];
  if (context.declared !== undefined) parts.push(`declared ${context.declared}`);
  if (context.installed !== undefined) parts.push(`installed ${context.installed}`);
  return `${where}${parts.length > 0 ? paint("dim", ` (${parts.join(", ")})`) : ""}`;
}

function formatDependency(d: DependencyDeclaration, paint: Paint): string {
  const coord = `${d.groupId}:${d.artifactId}:${d.version}`;
  const tags: string[] = [];
  if (d.scope && d.scope !== "compile") tags.push(d.scope);
  if (d.optional) tags.push("optional");
  return `${coord}${tags.length > 0 ? paint("dim", `  ${tags.join(", ")}`) : ""}`;
}

export async function runShow(
  coord: string,
  config: CappuConfig,
  options: { json?: boolean } = {},
  sources: readonly PackageSource[] = configuredSources(config),
  auditSource: AuditSource = new OsvSource(cachedFetchJson()),
): Promise<never> {
  const data = await buildShowData(coord, config, sources, auditSource);
  if ("error" in data) {
    process.stderr.write(`cappu: ${data.error}\n`);
    process.exit(data.code);
  }

  if (options.json) {
    process.stdout.write(`${JSON.stringify(showToJson(data), null, 2)}\n`);
  } else {
    process.stdout.write(renderShowCard(data, painter(process.stdout)));
  }
  process.exit(data.vulnerabilities.length > 0 ? 1 : 0);
}
