// Port of google-java-format's StringWrapper (core/.../java/StringWrapper.java),
// the post-pass that reflows string literals which run past the column limit.
//
// gjf runs it on its own OUTPUT: the formatter lays the code out, then this pass
// re-splits any `"..." + "..."` chain whose line is too long and the formatter
// runs again over the rewritten source. We do the same from `formatSource`.
//
// Only the long-string reflow is ported; gjf's text-block re-indentation in the
// same file is already handled by the printer's own text-block path.

import { forEachChild, parseSourceFile } from "../compiler/parser.ts";
import { skipTrivia } from "../compiler/utilities.ts";
import {
  type BinaryExpression,
  type Expression,
  type Node,
  type PropertyAccessExpression,
  type SourceFile,
  SyntaxKind,
} from "../compiler/types.ts";

const COLUMN_LIMIT = 100;
const TEXT_BLOCK = '"""';

/** One replacement: `[start, end)` of the original text, and its new spelling. */
interface Replacement {
  start: number;
  end: number;
  text: string;
}

/**
 * Reflow the string literals in `text` (already-formatted Java) that reach past
 * the column limit. Returns `text` unchanged when there is nothing to do, when
 * the source does not parse, or when the rewrite would change the token stream -
 * this pass must never alter the program, only where its strings are cut.
 */
export function wrapLongStrings(text: string): string {
  if (!needsWrapping(text)) return text;
  const sf = parseSourceFile("wrap.java", text);
  if (sf.parseDiagnostics.length > 0) return text;

  const replacements = collectReplacements(sf, text);
  if (replacements.length === 0) return text;

  // Apply back-to-front so earlier offsets stay valid.
  let out = text;
  for (const r of [...replacements].sort((a, b) => b.start - a.start)) {
    out = out.slice(0, r.start) + r.text + out.slice(r.end);
  }
  return sameStringValues(text, out) ? out : text;
}

/** gjf's fast path: only files with an over-long line can need this pass. */
function needsWrapping(text: string): boolean {
  for (const line of text.split("\n")) {
    if (line.length > COLUMN_LIMIT) return true;
  }
  return false;
}

function collectReplacements(sf: SourceFile, text: string): Replacement[] {
  const out: Replacement[] = [];
  const parents = new Map<Node, Node>();
  const literals: Node[] = [];

  const walk = (node: Node): void => {
    forEachChild(node, child => {
      parents.set(child, node);
      if (isLongStringLiteral(child, parents, text)) literals.push(child);
      walk(child);
      return undefined;
    });
  };
  walk(sf);

  const seen = new Set<number>();
  for (const literal of literals) {
    // The outermost contiguous `+` chain the literal belongs to.
    let enclosing: Node = literal;
    for (;;) {
      const parent = parents.get(enclosing);
      if (parent === undefined || !isConcat(parent)) break;
      enclosing = parent;
    }
    const flat = flattenConcat(enclosing);
    const idx = flat.indexOf(literal);
    if (idx < 0) continue;
    // Only the run of adjacent string literals around this one is reflowed.
    let lo = idx;
    while (lo > 0 && isPlainString(flat[lo - 1], text)) lo--;
    let hi = idx;
    while (hi + 1 < flat.length && isPlainString(flat[hi + 1], text)) hi++;
    const run = flat.slice(lo, hi + 1);
    const start = startOf(run[0], text);
    if (seen.has(start)) continue; // two literals of one chain produce one edit
    seen.add(start);
    const end = run[run.length - 1].end;

    const startColumn = start - lineStart(text, start);
    // Room the tail of the line still needs (`);`, `,`, ...).
    const trailing = lineEnd(text, end) - end;
    const first = lo === 0;
    const components = stringComponents(run, text);
    if (components.length === 0) continue;
    out.push({
      start,
      end,
      text: reflow(components, startColumn, trailing, first),
    });
  }
  return out;
}

/**
 * A string literal whose line runs past the column limit, and that gjf would
 * reflow: not a text block, and not the receiver of a dereference
 * (`"...".length()`), whose line the wrap could not shorten anyway.
 */
function isLongStringLiteral(node: Node, parents: Map<Node, Node>, text: string): boolean {
  if (node.kind !== SyntaxKind.StringLiteral) return false;
  const start = startOf(node, text);
  if (text.startsWith(TEXT_BLOCK, start)) return false;
  const parent = parents.get(node);
  if (
    parent?.kind === SyntaxKind.PropertyAccessExpression &&
    (parent as PropertyAccessExpression).expression === node
  ) {
    return false;
  }
  return lineEnd(text, node.end) - lineStart(text, node.end) > COLUMN_LIMIT;
}

function isConcat(node: Node): boolean {
  return (
    node.kind === SyntaxKind.BinaryExpression &&
    (node as BinaryExpression).operatorToken === SyntaxKind.PlusToken
  );
}

/** The `+` chain flattened left to right (gjf's pre-order traversal). */
function flattenConcat(root: Node): Node[] {
  const flat: Node[] = [];
  const todo: Node[] = [root];
  while (todo.length > 0) {
    const node = todo.shift()!;
    if (isConcat(node)) {
      const b = node as BinaryExpression;
      todo.unshift(b.left as Expression, b.right as Expression);
    } else {
      flat.push(node);
    }
  }
  return flat;
}

function isPlainString(node: Node, text: string): boolean {
  return node.kind === SyntaxKind.StringLiteral && !text.startsWith(TEXT_BLOCK, startOf(node, text));
}

/**
 * The "words" of the run: each literal's text without its quotes, split after
 * whitespace and after an escaped tab or newline, so a line can only be cut
 * where gjf would cut it.
 */
function stringComponents(run: Node[], text: string): string[] {
  const result: string[] = [];
  let piece = "";
  for (const node of run) {
    if (!isPlainString(node, text)) return [];
    const body = text.slice(startOf(node, text) + 1, node.end - 1);
    let start = 0;
    for (let idx = 0; idx < body.length; idx++) {
      if (/\s/.test(body[idx]) || body.startsWith("\\t", idx)) {
        // Cut BEFORE the whitespace: it begins the next piece, so a line break
        // lands where gjf puts it (`"... for"` / `+ " full-range ..."`).
      } else {
        let n = escapedNewlineAt(body, idx);
        if (n === 0) continue;
        while (n > 0) {
          idx += n;
          n = escapedNewlineAt(body, idx);
        }
      }
      piece += body.slice(start, idx);
      result.push(piece);
      piece = "";
      start = idx;
    }
    // gjf flushes at the end of each literal BEFORE carrying its tail over, so a
    // literal with no cut point of its own still becomes its own component - the
    // source's existing `"..." + "..."` cuts are kept when nothing else fits.
    if (piece.length > 0) {
      result.push(piece);
      piece = "";
    }
    if (start < body.length) piece += body.slice(start);
  }
  if (piece.length > 0) result.push(piece);
  return result;
}

function escapedNewlineAt(body: string, i: number): number {
  let n = 0;
  if (body.startsWith("\\r", i)) n += 2;
  if (body.startsWith("\\n", i + n)) n += 2;
  return n;
}

/**
 * gjf's greedy fill: words go on a line while they fit in `width`, which is the
 * room left by the start column and the two quotes; the last line also has to
 * leave room for `trailing`, and every line after the first pays for the +4
 * continuation indent and the `+ `.
 */
function reflow(
  components: string[],
  startColumn: number,
  trailing: number,
  first0: boolean,
): string {
  let width = COLUMN_LIMIT - startColumn - 2;
  const input = [...components];
  const lines: string[] = [];
  let first = first0;
  while (input.length > 0) {
    let length = 0;
    const line: string[] = [];
    if (totalLengthAtMost(input, width)) width -= trailing;
    while (input.length > 0 && (length <= 4 || length + input[0].length <= width)) {
      const word = input.shift()!;
      line.push(word);
      length += word.length;
      if (word.endsWith("\\n") || word.endsWith("\\r")) break;
    }
    if (line.length === 0) line.push(input.shift()!);
    lines.push(line.join(""));
    if (first) {
      width -= 6;
      first = false;
    }
  }
  const indent = " ".repeat(Math.max(0, startColumn + (first0 ? 4 : -2)));
  return `"${lines.join(`"\n${indent}+ "`)}"`;
}

function totalLengthAtMost(input: readonly string[], length: number): boolean {
  let total = 0;
  for (const s of input) {
    total += s.length;
    if (total > length) return false;
  }
  return true;
}

/**
 * The rewrite only re-cuts string literals, so the concatenated value of every
 * literal run must survive it. Cheap guard against a bad split silently
 * changing the program (gjf compares the re-parsed ASTs for the same reason).
 */
function sameStringValues(before: string, after: string): boolean {
  return stringPayload(before) === stringPayload(after);
}

function stringPayload(text: string): string {
  // Every literal's body, in order, with the `" + "` seams removed: that is what
  // must not change. Comments cannot contain a quote-delimited run here because
  // this pass only ever rewrites inside literals.
  const out: string[] = [];
  const re = /"(?:\\.|[^"\\\n])*"/g;
  for (const m of text.matchAll(re)) out.push(m[0].slice(1, -1));
  // No normalization: re-cutting a literal never moves a character, so the
  // concatenated bodies must come out byte-for-byte identical.
  return out.join("");
}

function lineStart(text: string, pos: number): number {
  return text.lastIndexOf("\n", pos - 1) + 1;
}

function lineEnd(text: string, pos: number): number {
  const idx = text.indexOf("\n", pos);
  return idx === -1 ? text.length : idx;
}

function startOf(node: Node, text: string): number {
  return skipTrivia(text, node.pos);
}
