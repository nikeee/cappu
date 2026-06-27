// The per-file unit of `cappu format`: read + format one file, returning a
// structured outcome (no writing, no process exit). Kept in its own lean module
// - importing only the formatter - so the worker_threads pool (format-worker.ts)
// loads as little as possible. Writes and output happen serially in the caller
// (format.ts), so results stay deterministic regardless of completion order.

import { readFileSync } from "node:fs";
import { relative } from "node:path";

import { type FormatOptions, formatSource, UnsupportedSyntaxError } from "../format/index.ts";

export interface Outcome {
  /** Path relative to the cwd, for output. */
  rel: string;
  /** The file could not be read. */
  readErr?: string;
  /** An unexpected (non-UnsupportedSyntax) formatting error. */
  fmtErr?: string;
  /** Unsupported syntax: the file is left untouched. */
  skipped?: boolean;
  /** The file differs from its formatted form. */
  changed?: boolean;
  /** The formatted text (present iff `changed`), for the caller to write. */
  formatted?: string;
}

export function formatOne(file: string, cwd: string, style: FormatOptions["style"]): Outcome {
  const rel = relative(cwd, file);
  let source: string;
  try {
    source = readFileSync(file, "utf8");
  } catch (e) {
    return { rel, readErr: (e as Error).message };
  }
  let formatted: string;
  try {
    formatted = formatSource(source, { style }, file);
  } catch (e) {
    if (e instanceof UnsupportedSyntaxError) return { rel, skipped: true };
    return { rel, fmtErr: (e as Error).message };
  }
  if (formatted === source) return { rel };
  return { rel, changed: true, formatted };
}
