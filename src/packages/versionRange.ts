// Maven version-range support. cappu otherwise treats a declared version as an
// exact coordinate; a Maven range (`[1.0,2.0)`) - in cappu.json or a real-world
// transitive POM - must first be resolved to a concrete published version.
// This module provides the ordering (a subset of Maven's ComparableVersion),
// the range parser, membership, and the "highest published match" selector.
//
// Scope: bracket/paren ranges, comma-joined sets, and the RELEASE/LATEST
// tokens. A bare version keeps its exact-pin meaning (parseVersionSpec returns
// undefined for it) and is NOT reinterpreted as a Maven soft requirement.

// ---- ordering (Maven ComparableVersion, subset) -----------------------------

// Known qualifier ranks (lower sorts earlier). "" is the release itself and
// outranks every pre-release qualifier; "sp"/"ga"/"final" are >= release.
// ponytail: Maven's full alias table (a=alpha, b=beta, m=milestone, cr=rc, ...)
// is reproduced only for the common aliases below; an unknown qualifier sorts
// after the release and lexically among its peers - upgrade the table if a real
// dependency's ordering needs it.
const QUALIFIER_RANK = new Map<string, number>([
  ["alpha", -6],
  ["a", -6],
  ["beta", -5],
  ["b", -5],
  ["milestone", -4],
  ["m", -4],
  ["rc", -3],
  ["cr", -3],
  ["snapshot", -2],
  ["", 0], // the release
  ["ga", 0],
  ["final", 0],
  ["release", 0],
  ["sp", 1],
]);

type Segment = { readonly num?: bigint; readonly qual?: string };

// Split into numeric and qualifier segments. Maven separates on `.` and `-`,
// and also at a digit/letter transition ("1alpha" -> "1", "alpha"). A numeric
// segment is compared numerically; a qualifier segment by rank then lexically.
function segmentsOf(version: string): Segment[] {
  const segments: Segment[] = [];
  for (const raw of version.toLowerCase().split(/[.\-_+]/)) {
    if (raw === "") continue;
    // break at each digit<->letter boundary
    for (const piece of raw.match(/\d+|[a-z]+/g) ?? []) {
      if (/^\d+$/.test(piece)) segments.push({ num: BigInt(piece) });
      else segments.push({ qual: piece });
    }
  }
  return segments;
}

// Compare two qualifiers by known rank, then lexically; a known (pre-release or
// release) qualifier always sorts before an unknown one (Maven ranks unknown
// qualifiers after the release).
function compareQualifier(aq: string, bq: string): number {
  const ar = QUALIFIER_RANK.get(aq);
  const br = QUALIFIER_RANK.get(bq);
  if (ar !== undefined && br !== undefined) return Math.sign(ar - br);
  if (ar !== undefined) return -1;
  if (br !== undefined) return 1;
  return aq < bq ? -1 : aq > bq ? 1 : 0;
}

// A present segment against a missing one (the shorter version padded): a
// number compares against 0 (so 1.0 == 1.0.0), a qualifier against the release
// (so 1.0-alpha < 1.0, but 1.0-sp > 1.0).
function compareToMissing(seg: Segment): number {
  if (seg.num !== undefined) return seg.num > 0n ? 1 : seg.num < 0n ? -1 : 0;
  return compareQualifier(seg.qual!, "");
}

function compareSegment(a: Segment | undefined, b: Segment | undefined): number {
  if (a === undefined) return b === undefined ? 0 : -compareToMissing(b);
  if (b === undefined) return compareToMissing(a);
  if (a.num !== undefined && b.num !== undefined) return a.num < b.num ? -1 : a.num > b.num ? 1 : 0;
  // A number always outranks a qualifier (Maven: 1.1 > 1.1-alpha).
  if (a.num !== undefined) return 1;
  if (b.num !== undefined) return -1;
  return compareQualifier(a.qual!, b.qual!);
}

/** Maven-style version ordering: negative if a<b, positive if a>b, 0 if equal. */
export function compareVersions(a: string, b: string): number {
  const as = segmentsOf(a);
  const bs = segmentsOf(b);
  const len = Math.max(as.length, bs.length);
  for (let i = 0; i < len; i++) {
    const c = compareSegment(as[i], bs[i]);
    if (c !== 0) return c;
  }
  return 0;
}

// ---- ranges -----------------------------------------------------------------

interface Restriction {
  readonly lower?: string;
  readonly lowerInclusive: boolean;
  readonly upper?: string;
  readonly upperInclusive: boolean;
}

export interface VersionSpec {
  /** Any-newest tokens (RELEASE/LATEST): pick the highest published version. */
  readonly newest?: boolean;
  /** OR-joined restrictions; a version satisfies the spec if it satisfies any. */
  readonly restrictions: readonly Restriction[];
}

function parseRestriction(text: string): Restriction | undefined {
  const lowerInclusive = text.startsWith("[");
  const upperInclusive = text.endsWith("]");
  const open = text.startsWith("[") || text.startsWith("(");
  const close = text.endsWith("]") || text.endsWith(")");
  if (!open || !close) return undefined;
  const inner = text.slice(1, -1);
  if (!inner.includes(",")) {
    // [1.5] - a single hard version (both bounds inclusive on that version)
    if (!lowerInclusive || !upperInclusive || inner === "") return undefined;
    return { lower: inner, lowerInclusive: true, upper: inner, upperInclusive: true };
  }
  const comma = inner.indexOf(",");
  const lower = inner.slice(0, comma).trim();
  const upper = inner.slice(comma + 1).trim();
  return {
    ...(lower !== "" ? { lower } : {}),
    lowerInclusive,
    ...(upper !== "" ? { upper } : {}),
    upperInclusive,
  };
}

/**
 * Parse a Maven version spec, or undefined when `spec` is a plain exact version
 * (the caller then treats it as an exact coordinate, as before). RELEASE/LATEST
 * become a newest-wins spec.
 */
export function parseVersionSpec(spec: string): VersionSpec | undefined {
  const trimmed = spec.trim();
  if (trimmed === "RELEASE" || trimmed === "LATEST") return { newest: true, restrictions: [] };
  if (!trimmed.startsWith("[") && !trimmed.startsWith("(")) return undefined;
  // Split comma-joined sets at the commas *between* restrictions (those that
  // follow a `]` or `)`), leaving the intra-restriction commas intact.
  const restrictions: Restriction[] = [];
  let depth = 0;
  let start = 0;
  for (let i = 0; i <= trimmed.length; i++) {
    const ch = trimmed[i];
    if (ch === "[" || ch === "(") depth++;
    else if (ch === "]" || ch === ")") depth--;
    if (i === trimmed.length || (ch === "," && depth === 0)) {
      const part = trimmed.slice(start, i).trim();
      if (part !== "") {
        const restriction = parseRestriction(part);
        if (!restriction) return undefined; // malformed: fall back to exact
        restrictions.push(restriction);
      }
      start = i + 1;
    }
  }
  return restrictions.length > 0 ? { restrictions } : undefined;
}

function withinRestriction(r: Restriction, version: string): boolean {
  if (r.lower !== undefined) {
    const c = compareVersions(version, r.lower);
    if (c < 0 || (c === 0 && !r.lowerInclusive)) return false;
  }
  if (r.upper !== undefined) {
    const c = compareVersions(version, r.upper);
    if (c > 0 || (c === 0 && !r.upperInclusive)) return false;
  }
  return true;
}

/** Whether `version` satisfies `spec` (RELEASE/LATEST are satisfied by any). */
export function satisfies(spec: VersionSpec, version: string): boolean {
  if (spec.newest) return true;
  return spec.restrictions.some(r => withinRestriction(r, version));
}

/**
 * The highest published version satisfying `spec` (Maven picks the highest in
 * range; RELEASE/LATEST pick the newest overall), or undefined when none match.
 */
export function selectVersion(spec: VersionSpec, published: readonly string[]): string | undefined {
  let best: string | undefined;
  for (const version of published) {
    if (!satisfies(spec, version)) continue;
    if (best === undefined || compareVersions(version, best) > 0) best = version;
  }
  return best;
}
