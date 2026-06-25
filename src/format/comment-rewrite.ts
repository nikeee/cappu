// Port of google-java-format core/.../java/JavaCommentsHelper.java (the
// `rewrite` path). The outer comment-normalization layer that runs at write time
// on every comment, given the column it starts at. For a javadoc comment it first
// runs the full reflow (formatter.ts); then it re-indents/wraps the lines.

import { formatJavadoc } from "./javadoc/formatter.ts";

const MAX_LINE_LENGTH = 100;

/**
 * Rewrite a comment for output at `column0`. `isLine` is true for `//` comments.
 * Mirrors `JavaCommentsHelper.rewrite`.
 */
export function rewriteComment(text: string, column0: number, isLine: boolean): string {
  if (text.startsWith("/**")) {
    text = formatJavadoc(text, column0);
  }
  const lines = text.split("\n").map(l => (isLine ? l.trim() : trimTrailing(l)));
  if (isLine) return indentLineComments(lines, column0);
  const pc = reformatParamComment(text);
  if (pc !== null) return pc;
  return javadocShaped(lines) ? indentJavadoc(lines, column0) : preserveIndentation(lines, column0);
}

// gjf's PARAMETER_COMMENT: `/*name=*/` -> `/* name= */` (identifier, optional
// varargs `...`). Returns null when the text is not a parameter comment.
const PARAM_COMMENT = /^\/\*\s*([A-Za-z_$][\w$]*(?:\.\.\.)?)\s*=\s*\*\/$/;

export function reformatParamComment(text: string): string | null {
  const m = text.match(PARAM_COMMENT);
  return m ? `/* ${m[1]}= */` : null;
}

// For a non-javadoc-shaped block comment, shift the block to `column0` but keep
// relative indentation.
function preserveIndentation(lines: string[], column0: number): string {
  let startCol = -1;
  for (let i = 1; i < lines.length; i++) {
    const idx = lines[i].search(/\S/);
    if (idx >= 0 && (startCol === -1 || idx < startCol)) startCol = idx;
  }
  let out = lines[0];
  const pad = " ".repeat(column0);
  for (let i = 1; i < lines.length; i++) {
    out += "\n" + pad;
    out += startCol >= 0 && lines[i].length >= startCol ? lines[i].slice(startCol) : lines[i];
  }
  return out;
}

// Re-indent a javadoc-shaped block comment: first line as-is, continuations at
// `column0+1` with a `*` prefix.
function indentJavadoc(lines: string[], column0: number): string {
  let out = lines[0].trim();
  const pad = " ".repeat(column0 + 1);
  for (let i = 1; i < lines.length; i++) {
    out += "\n" + pad;
    let line = lines[i].trim();
    if (!line.startsWith("*")) {
      out += "* ";
    }
    out += line;
  }
  return out;
}

// Wrap and re-indent line comments.
function indentLineComments(lines: string[], column0: number): string {
  lines = wrapLineComments(lines, column0);
  let out = lines[0].trim();
  const pad = " ".repeat(column0);
  for (let i = 1; i < lines.length; i++) out += "\n" + pad + lines[i].trim();
  return out;
}

// Preserve special `//noinspection` / `//$NON-NLS-x$` comments (no leading space).
const MISSING_SPACE_PREFIX = /^(\/\/+)(?!noinspection|\$NON-NLS-\d+\$)[^\s/]/;

function wrapLineComments(lines: string[], column0: number): string[] {
  const result: string[] = [];
  for (let line of lines) {
    const m = line.match(MISSING_SPACE_PREFIX);
    if (m) {
      const length = m[1].length;
      line = "/".repeat(length) + " " + line.slice(length);
    }
    if (line.startsWith("// MOE:")) {
      result.push(line);
      continue;
    }
    while (line.length + column0 > MAX_LINE_LENGTH) {
      let idx = MAX_LINE_LENGTH - column0;
      while (idx >= 2 && !/\s/.test(line[idx])) idx--;
      if (idx <= 2) break;
      result.push(line.slice(0, idx));
      line = "//" + line.slice(idx);
    }
    result.push(line);
  }
  return result;
}

function javadocShaped(lines: string[]): boolean {
  if (lines.length === 0) return false;
  const first = lines[0].trim();
  if (first.startsWith("/**")) return true;
  if (!first.startsWith("/*")) return false;
  for (let i = 1; i < lines.length; i++) {
    if (!lines[i].trim().startsWith("*")) return false;
  }
  return true;
}

function trimTrailing(s: string): string {
  return s.replace(/\s+$/, "");
}
