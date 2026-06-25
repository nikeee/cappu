// A port of google-java-format's line-breaking engine (the
// `com.google.googlejavaformat.Doc` / `Indent` algorithm), not vanilla
// Wadler/Leijen. The difference matters for byte-compatibility: gjf breaks a
// Level's direct Breaks by FillMode (UNIFIED = all together, INDEPENDENT = fill,
// FORCED = always), propagates a "must break" flag across a broken Level, and
// carries the continuation indent on the Level/Break rather than as a standalone
// wrapper. See google/google-java-format core/.../Doc.java and Indent.java.
//
// The AST -> Doc lowering lives in printer.ts; this file is purely the IR and
// the breaking algorithm and knows nothing about Java.
//
// We drop gjf's Input/Tok/CommentsHelper machinery: comments are recovered and
// emitted as plain text by the printer, so a leaf here is just a `string` Token.

/** How a Level breaks its direct Breaks when it does not fit on one line. */
export type FillMode = "unified" | "independent" | "forced";

const MAX_WIDTH = 1000;

/** Records whether a particular Break was taken, for a conditional Indent. */
export class BreakTag {
  private broken = false;
  recordBroken(b: boolean): void {
    this.broken = b;
  }
  wasBreakTaken(): boolean {
    return this.broken;
  }
}

// --- indent ----------------------------------------------------------------

// An Indent's `n` is in columns at google scale (one indent level = 2, a
// continuation = 4); `evalIndent` multiplies by the style multiplier (1 google,
// 2 aosp) so aosp doubles to 4/8. gjf bakes the multiplier at construction;
// deferring it lets the printer build a Doc once and print it in either style.
export type Indent =
  | { kind: "const"; n: number }
  | { kind: "if"; cond: BreakTag; then: Indent; else: Indent };

export function indentConst(n: number): Indent {
  return { kind: "const", n };
}
/** Conditional indent: `then` if `cond`'s break was taken, else `else`. */
export function indentIf(cond: BreakTag, thenI: Indent, elseI: Indent): Indent {
  return { kind: "if", cond, then: thenI, else: elseI };
}
export const ZERO: Indent = indentConst(0);

function evalIndent(i: Indent, mult: number): number {
  return i.kind === "const"
    ? i.n * mult
    : evalIndent(i.cond.wasBreakTaken() ? i.then : i.else, mult);
}

// --- doc nodes -------------------------------------------------------------
// A `string` is a literal token. Compound nodes are objects with a `kind`.

export type Doc = string | Concat | Level | Break;

class Concat {
  readonly kind = "concat";
  private _w = -1;
  private _f: string | null = null;
  constructor(readonly parts: Doc[]) {}
  width(): number {
    if (this._w < 0) this._w = sumWidth(this.parts);
    return this._w;
  }
  flat(): string {
    if (this._f === null) this._f = this.parts.map(docFlat).join("");
    return this._f;
  }
}

// A Break carries only immutable description. Its per-occurrence decision
// (broken? new indent?) lives in the controlling Level's parallel arrays, so a
// shared singleton (line/softline/hardline) reused many times never clobbers
// itself - gjf gets this for free by minting a fresh Break per occurrence.
export class Break {
  readonly kind = "break";
  constructor(
    readonly fillMode: FillMode,
    readonly flatText: string,
    readonly plusIndent: Indent,
    readonly optTag?: BreakTag,
  ) {}
  width(): number {
    return this.fillMode === "forced" ? MAX_WIDTH : this.flatText.length;
  }
}

class Level {
  readonly kind = "level";
  private _w = -1;
  private _f: string | null = null;
  // Filled in by computeBreaks, read by write. broken[i]/newIndent[i] are the
  // decision for breaks[i] (parallel arrays, one entry per controlled break).
  oneLine = false;
  splits: Doc[][] = [];
  breaks: Break[] = [];
  broken: boolean[] = [];
  newIndent: number[] = [];
  constructor(
    readonly plusIndent: Indent,
    readonly docs: Doc[],
  ) {}
  width(): number {
    if (this._w < 0) this._w = sumWidth(this.docs);
    return this._w;
  }
  flat(): string {
    if (this._f === null) this._f = this.docs.map(docFlat).join("");
    return this._f;
  }
}

function docWidth(doc: Doc): number {
  if (typeof doc === "string") return doc.includes("\n") ? MAX_WIDTH : doc.length;
  return doc.width();
}

function docFlat(doc: Doc): string {
  return typeof doc === "string" ? doc : doc.kind === "break" ? doc.flatText : doc.flat();
}

function sumWidth(docs: Doc[]): number {
  let w = 0;
  for (const d of docs) {
    w += docWidth(d);
    if (w >= MAX_WIDTH) return MAX_WIDTH;
  }
  return w;
}

// --- constructors ----------------------------------------------------------

/** A literal token (identity: a string is already a Doc). */
export function text(s: string): Doc {
  return s;
}

export function concat(parts: Doc[]): Doc {
  return new Concat(parts);
}

/** Join `parts` with `sep` between each. */
export function join(sep: Doc, parts: Doc[]): Doc {
  const out: Doc[] = [];
  parts.forEach((p, i) => {
    if (i > 0) out.push(sep);
    out.push(p);
  });
  return new Concat(out);
}

/** A breakable group whose breaks take an extra `plusIndent` (base units). */
export function level(plusIndent: Indent, docs: Doc[]): Doc {
  return new Level(plusIndent, docs);
}

/** A break: `flat` text when not broken, a newline + `plusIndent` when broken. */
export function brk(
  fillMode: FillMode,
  flatText: string,
  plusIndent: Indent = ZERO,
  optTag?: BreakTag,
): Doc {
  return new Break(fillMode, flatText, plusIndent, optTag);
}

// --- compatibility layer (the original Wadler-ish API, mapped onto gjf) -----
// These keep printer.ts close to its pre-rewrite shape. group = a zero-indent
// Level; indent = a Level carrying one base unit of continuation indent; the
// three line kinds are UNIFIED/FORCED breaks. The per-construct phases replace
// these with explicit level()/brk() where gjf needs richer behavior.

/** Lay flat if it fits, else break this group's UNIFIED lines. */
export function group(doc: Doc): Doc {
  return new Level(ZERO, [doc]);
}

/** Add one indent level (2 columns at google scale) to any break inside `doc`. */
export function indent(doc: Doc): Doc {
  return new Level(indentConst(2), [doc]);
}

/** A break that is a space when flat and a newline when its group breaks. */
export const line: Doc = new Break("unified", " ", ZERO);
/** A break that is nothing when flat and a newline when its group breaks. */
export const softline: Doc = new Break("unified", "", ZERO);
/** A break that is always a newline; forces every enclosing group to break. */
export const hardline: Doc = new Break("forced", "", ZERO);

// --- breaking algorithm ----------------------------------------------------

interface State {
  lastIndent: number;
  indent: number;
  column: number;
  mustBreak: boolean;
}

interface PrintOptions {
  /** Hard wrap column (google-java-format: 100). */
  width: number;
  /** Indent multiplier: 1 for google (2-space), 2 for aosp (4-space). */
  indentMultiplier: number;
}

// Split a Level's docs into Break-separated groups. Concats are transparent and
// flattened in place (so breaks they contain are controlled by this Level);
// Levels are opaque (their own breaks are controlled by them).
function splitByBreaks(docs: Doc[]): { splits: Doc[][]; breaks: Break[] } {
  const splits: Doc[][] = [[]];
  const breaks: Break[] = [];
  const walk = (ds: Doc[]): void => {
    for (const d of ds) {
      if (typeof d === "string") {
        splits[splits.length - 1].push(d);
      } else if (d.kind === "break") {
        breaks.push(d);
        splits.push([]);
      } else if (d.kind === "concat") {
        walk(d.parts);
      } else {
        splits[splits.length - 1].push(d);
      }
    }
  };
  walk(docs);
  return { splits, breaks };
}

function computeBreaks(doc: Doc, maxWidth: number, mult: number, state: State): State {
  if (typeof doc === "string") {
    return { ...state, column: state.column + docWidth(doc) };
  }
  switch (doc.kind) {
    case "concat": {
      let s = state;
      for (const d of doc.parts) s = computeBreaks(d, maxWidth, mult, s);
      return s;
    }
    case "level":
      return computeLevel(doc, maxWidth, mult, state);
    case "break":
      // Breaks are handled by their enclosing Level's computeBreakAndSplit; a
      // break reaching here is a bug (it was not a direct child of a Level).
      throw new Error("unexpected Break outside a Level");
  }
}

function computeLevel(lvl: Level, maxWidth: number, mult: number, state: State): State {
  const w = lvl.width();
  if (state.column + w <= maxWidth) {
    lvl.oneLine = true;
    return { ...state, column: state.column + w };
  }
  lvl.oneLine = false;
  const inner = evalIndent(lvl.plusIndent, mult);
  const startIndent = state.indent + inner;
  let s: State = {
    lastIndent: startIndent,
    indent: startIndent,
    column: state.column,
    mustBreak: false,
  };
  const { splits, breaks } = splitByBreaks(lvl.docs);
  lvl.splits = splits;
  lvl.breaks = breaks;
  lvl.broken = new Array(breaks.length);
  lvl.newIndent = new Array(breaks.length);

  // First split has no preceding break.
  s = breakAndSplit(lvl, -1, maxWidth, mult, s, undefined, splits[0]);
  for (let i = 0; i < breaks.length; i++) {
    s = breakAndSplit(lvl, i, maxWidth, mult, s, breaks[i], splits[i + 1]);
  }
  return { ...state, column: s.column };
}

// Lay out one Break-separated group. When optBreak is present its decision is
// recorded into lvl.broken[i]/lvl.newIndent[i].
function breakAndSplit(
  lvl: Level,
  i: number,
  maxWidth: number,
  mult: number,
  state: State,
  optBreak: Break | undefined,
  split: Doc[],
): State {
  const breakWidth = optBreak ? optBreak.width() : 0;
  const splitWidth = sumWidth(split);
  const shouldBreak =
    (optBreak !== undefined && optBreak.fillMode === "unified") ||
    state.mustBreak ||
    state.column + breakWidth + splitWidth > maxWidth;

  let s = state;
  if (optBreak) {
    if (optBreak.optTag) optBreak.optTag.recordBroken(shouldBreak);
    if (shouldBreak) {
      const newIndent = Math.max(s.lastIndent + evalIndent(optBreak.plusIndent, mult), 0);
      lvl.broken[i] = true;
      lvl.newIndent[i] = newIndent;
      s = { ...s, column: newIndent };
    } else {
      lvl.broken[i] = false;
      lvl.newIndent[i] = -1;
      s = { ...s, column: s.column + optBreak.flatText.length };
    }
  }
  const enoughRoom = s.column + splitWidth <= maxWidth;
  s = computeSplit(maxWidth, mult, split, { ...s, mustBreak: false });
  if (!enoughRoom) s = { ...s, mustBreak: true };
  return s;
}

function computeSplit(maxWidth: number, mult: number, docs: Doc[], state: State): State {
  let s = state;
  for (const d of docs) s = computeBreaks(d, maxWidth, mult, s);
  return s;
}

// --- output ----------------------------------------------------------------

function writeDoc(doc: Doc, out: string[]): void {
  if (typeof doc === "string") {
    out.push(doc);
    return;
  }
  switch (doc.kind) {
    case "concat":
      for (const d of doc.parts) writeDoc(d, out);
      break;
    case "level":
      if (doc.oneLine) {
        out.push(doc.flat());
      } else {
        for (const d of doc.splits[0]) writeDoc(d, out);
        for (let i = 0; i < doc.breaks.length; i++) {
          if (doc.broken[i]) {
            trimTrailingSpace(out);
            out.push("\n" + " ".repeat(doc.newIndent[i]));
          } else {
            out.push(doc.breaks[i].flatText);
          }
          for (const d of doc.splits[i + 1]) writeDoc(d, out);
        }
      }
      break;
    case "break":
      // Breaks are written by their controlling Level; one here is a bug.
      throw new Error("unexpected Break in writeDoc");
  }
}

export function printDoc(doc: Doc, options: PrintOptions): string {
  // Wrap in a root Level so top-level breaks have a controlling Level.
  const root = typeof doc !== "string" && doc.kind === "level" ? doc : new Level(ZERO, [doc]);
  computeLevel(root, options.width, options.indentMultiplier, {
    lastIndent: 0,
    indent: 0,
    column: 0,
    mustBreak: false,
  });
  const out: string[] = [];
  writeDoc(root, out);
  trimTrailingSpace(out);
  return out.join("");
}

function trimTrailingSpace(out: string[]): void {
  // Walk back over pushed chunks that are all spaces; trim the first non-space.
  for (let i = out.length - 1; i >= 0; i--) {
    const s = out[i];
    if (s.length === 0) continue;
    if (/^ +$/.test(s)) {
      out[i] = "";
      continue;
    }
    const trimmed = s.replace(/ +$/, "");
    if (trimmed !== s) out[i] = trimmed;
    break;
  }
}
