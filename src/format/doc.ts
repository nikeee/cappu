// A small Wadler/Leijen-style document IR and printer - the same model
// google-java-format, prettier, ruff and biome use: build a tree of layout
// intentions, then print it at a target column width, breaking a `group` onto
// multiple lines only when it does not fit flat.
//
// The AST -> Doc lowering lives in printer.ts; this file is purely the IR and
// the printing algorithm and knows nothing about Java.

export type Doc =
  | string
  | { kind: "concat"; parts: Doc[] }
  | { kind: "line"; soft: boolean; hard: boolean }
  | { kind: "group"; doc: Doc }
  | { kind: "indent"; doc: Doc };

/** A break that is a space when flat and a newline when its group breaks. */
export const line: Doc = { kind: "line", soft: false, hard: false };
/** A break that is nothing when flat and a newline when its group breaks. */
export const softline: Doc = { kind: "line", soft: true, hard: false };
/** A break that is always a newline; forces every enclosing group to break. */
export const hardline: Doc = { kind: "line", soft: false, hard: true };

export function concat(parts: Doc[]): Doc {
  return { kind: "concat", parts };
}

/** Join `parts` with `sep` between each. */
export function join(sep: Doc, parts: Doc[]): Doc {
  const out: Doc[] = [];
  parts.forEach((p, i) => {
    if (i > 0) out.push(sep);
    out.push(p);
  });
  return concat(out);
}

/** Lay the doc out flat if it fits the remaining width, else break its lines. */
export function group(doc: Doc): Doc {
  return { kind: "group", doc };
}

/** Increase the indent of any newline produced inside `doc` by one unit. */
export function indent(doc: Doc): Doc {
  return { kind: "indent", doc };
}

// --- printing --------------------------------------------------------------

const enum Mode {
  Flat,
  Break,
}

type Cmd = { indent: number; mode: Mode; doc: Doc };

interface PrintOptions {
  /** Hard wrap column (google-java-format: 100). */
  width: number;
  /** Spaces per indent level (google: 2, aosp: 4). */
  indentUnit: number;
}

// Does the command (and everything queued after it on the current line) fit in
// `remaining` columns laid out flat? A hardline never fits, which is what makes
// a group containing one break onto multiple lines.
function fits(remaining: number, next: Cmd, rest: Cmd[], indentUnit: number): boolean {
  if (remaining < 0) return false;
  const cmds: Cmd[] = [next];
  let restIdx = rest.length - 1;
  while (remaining >= 0) {
    if (cmds.length === 0) {
      if (restIdx < 0) return true;
      cmds.push(rest[restIdx]);
      restIdx--;
      continue;
    }
    // biome/prettier order: process the most recently pushed command (a stack).
    const cmd = cmds.pop()!;
    const doc = cmd.doc;
    if (typeof doc === "string") {
      remaining -= doc.length;
    } else if (doc.kind === "concat") {
      for (let i = doc.parts.length - 1; i >= 0; i--) {
        cmds.push({ indent: cmd.indent, mode: cmd.mode, doc: doc.parts[i] });
      }
    } else if (doc.kind === "indent") {
      cmds.push({ indent: cmd.indent + indentUnit, mode: cmd.mode, doc: doc.doc });
    } else if (doc.kind === "group") {
      // Inside fits-check a group is measured in its parent's mode.
      cmds.push({ indent: cmd.indent, mode: cmd.mode, doc: doc.doc });
    } else if (doc.kind === "line") {
      // A line break that will actually break (the surrounding content is in
      // Break mode, or a later hardline in the trailing rest) ends the current
      // line, so everything measured so far fits. Only a hardline *inside* the
      // group being measured flat means the group cannot stay flat.
      if (cmd.mode === Mode.Break) return true;
      if (doc.hard) return false;
      if (!doc.soft) remaining -= 1; // a flat soft line is nothing, a flat line is a space
    }
  }
  return false;
}

export function printDoc(doc: Doc, options: PrintOptions): string {
  const { width, indentUnit } = options;
  const out: string[] = [];
  let pos = 0; // current column
  const cmds: Cmd[] = [{ indent: 0, mode: Mode.Break, doc }];

  while (cmds.length > 0) {
    const cmd = cmds.pop()!;
    const d = cmd.doc;
    if (typeof d === "string") {
      out.push(d);
      pos += d.length;
    } else if (d.kind === "concat") {
      for (let i = d.parts.length - 1; i >= 0; i--) {
        cmds.push({ indent: cmd.indent, mode: cmd.mode, doc: d.parts[i] });
      }
    } else if (d.kind === "indent") {
      cmds.push({ indent: cmd.indent + indentUnit, mode: cmd.mode, doc: d.doc });
    } else if (d.kind === "group") {
      const flat: Cmd = { indent: cmd.indent, mode: Mode.Flat, doc: d.doc };
      if (fits(width - pos, flat, cmds, indentUnit)) {
        cmds.push(flat);
      } else {
        cmds.push({ indent: cmd.indent, mode: Mode.Break, doc: d.doc });
      }
    } else {
      // line
      if (cmd.mode === Mode.Flat && !d.hard) {
        if (!d.soft) {
          out.push(" ");
          pos += 1;
        }
      } else {
        // Trim trailing spaces on the line we are ending (g-j-f never leaves them).
        trimTrailingSpace(out);
        out.push("\n" + " ".repeat(cmd.indent));
        pos = cmd.indent;
      }
    }
  }
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
