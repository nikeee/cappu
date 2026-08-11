// How the import block is grouped and ordered. Shared by the formatter
// (src/format/printer.ts), the `source.organizeImports` code action and the
// `organize_imports` MCP tool, so the three can never disagree.
//
// Unset `importOrder` reproduces google-java-format: one lexicographic block in
// google style, and gjf's AOSP order (ImportOrderer.AOSP_IMPORT_COMPARATOR plus
// shouldInsertBlankLineAosp) in aosp style. A configured `importOrder` replaces
// whichever built-in applies.

/** The minimum an import must expose to be ordered: its name and staticness. */
export interface ImportLike {
  /** The dotted name, without `import`/`static`/`.*`/`;` (`java.io.File`). */
  readonly name: string;
  readonly isStatic: boolean;
}

export interface ImportOrderOptions {
  readonly style: "google" | "aosp";
  /** Package prefixes ending in `*`; `""` inserts a blank line. */
  readonly importOrder?: readonly string[];
}

/** gjf's AOSP android group (ImportOrderer.Import#isAndroid). */
const ANDROID_PREFIXES = ["android.", "androidx.", "dalvik.", "libcore.", "com.android."];

/**
 * Group and order `imports` into the blocks to print, in order. Blocks are
 * separated by one blank line; the caller renders each block's lines.
 *
 * Static imports always form the first block (gjf's rule, and every other Java
 * tool's default), so `importOrder` applies to the non-static imports only.
 */
export function orderImports<T extends ImportLike>(
  imports: readonly T[],
  options: ImportOrderOptions,
): T[][] {
  const statics = byName(imports.filter(i => i.isStatic));
  const rest = imports.filter(i => !i.isStatic);
  const blocks =
    options.importOrder !== undefined
      ? configuredBlocks(rest, options.importOrder)
      : options.style === "aosp"
        ? aospBlocks(rest)
        : [byName(rest)];
  return [statics, ...blocks].filter(b => b.length > 0);
}

function byName<T extends ImportLike>(imports: readonly T[]): T[] {
  return [...imports].sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
}

/**
 * The prefix a pattern selects: everything before its trailing `*`. The config
 * schema rejects a pattern that does not end in `*`, so this is total.
 */
export function patternPrefix(pattern: string): string {
  return pattern.endsWith("*") ? pattern.slice(0, -1) : pattern;
}

/**
 * `importOrder` splits into blocks at each `""`. An import joins the block of
 * the pattern with the LONGEST matching prefix - so precedence does not depend
 * on list position, and a specific group may sit after a general one. Equal
 * prefixes fall back to list order. Imports matching nothing form a last block.
 */
function configuredBlocks<T extends ImportLike>(
  imports: readonly T[],
  importOrder: readonly string[],
): T[][] {
  // Patterns in list order, each tagged with the block it belongs to.
  const patterns: { prefix: string; block: number }[] = [];
  let block = 0;
  for (const entry of importOrder) {
    if (entry === "") {
      block++;
      continue;
    }
    patterns.push({ prefix: patternPrefix(entry), block });
  }
  const blockCount = block + 1;
  const blocks: T[][] = Array.from({ length: blockCount + 1 }, () => []);
  const unmatched = blockCount; // the extra block at the end
  for (const imp of imports) {
    let best: { prefix: string; block: number } | undefined;
    for (const p of patterns) {
      if (!imp.name.startsWith(p.prefix)) continue;
      if (best === undefined || p.prefix.length > best.prefix.length) best = p;
    }
    blocks[best?.block ?? unmatched].push(imp);
  }
  return blocks.map(byName);
}

/**
 * gjf's AOSP order: android imports, then third-party, then `java`/`javax`,
 * lexicographic within each, with a blank line wherever the top-level package
 * changes (so `com.foo` and `io.bar` split even though both are third party) or
 * an android import is followed by a non-android one.
 */
function aospBlocks<T extends ImportLike>(imports: readonly T[]): T[][] {
  const sorted = [...imports].sort((a, b) => {
    const ra = aospRank(a.name);
    const rb = aospRank(b.name);
    if (ra !== rb) return ra - rb;
    return a.name < b.name ? -1 : a.name > b.name ? 1 : 0;
  });
  const blocks: T[][] = [];
  let current: T[] = [];
  for (const imp of sorted) {
    const prev = current[current.length - 1];
    if (prev !== undefined && aospSplits(prev.name, imp.name)) {
      blocks.push(current);
      current = [];
    }
    current.push(imp);
  }
  if (current.length > 0) blocks.push(current);
  return blocks;
}

function isAndroid(name: string): boolean {
  return ANDROID_PREFIXES.some(p => name.startsWith(p));
}

function topLevel(name: string): string {
  const dot = name.indexOf(".");
  return dot < 0 ? name : name.slice(0, dot);
}

function aospRank(name: string): number {
  if (isAndroid(name)) return 0;
  const top = topLevel(name);
  return top === "java" || top === "javax" ? 2 : 1;
}

/** gjf's shouldInsertBlankLineAosp, minus the static/non-static case. */
function aospSplits(prev: string, curr: string): boolean {
  if (isAndroid(prev) && !isAndroid(curr)) return true;
  return topLevel(prev) !== topLevel(curr);
}
