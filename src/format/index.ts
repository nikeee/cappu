// Public entry point for the Java source formatter. Parses with cappu's own
// parser and regenerates layout via the Doc IR (printer.ts / doc.ts), targeting
// google-java-format compatibility. Default style is "google" (2-space indent);
// "aosp" is the 4-space variant.

import { parseSourceFile } from "../compiler/parser.ts";
import { type FormatOptions, formatSourceFile, UnsupportedSyntaxError } from "./printer.ts";
import { wrapLongStrings } from "./string-wrapper.ts";

export type { FormatOptions } from "./printer.ts";
export { UnsupportedSyntaxError } from "./printer.ts";

/**
 * Format Java source text. `fileName` is only used for parser diagnostics.
 * Throws {@link UnsupportedSyntaxError} when the input cannot be reformatted
 * without losing information - a syntax error, or a comment in a position the
 * formatter does not yet handle - so callers can leave such files untouched.
 */
export function formatSource(
  text: string,
  options: FormatOptions = { style: "google" },
  fileName = "input.java",
): string {
  const sf = parseSourceFile(fileName, text);
  if (sf.parseDiagnostics.length > 0) {
    throw new UnsupportedSyntaxError("source has syntax errors");
  }
  const once = formatSourceFile(sf, options);
  // google-java-format reflows over-long string literals as a post-pass over its
  // own output and then formats again (Formatter.formatSource -> StringWrapper).
  const wrapped = wrapLongStrings(once);
  if (wrapped === once) return once;
  const rewritten = parseSourceFile(fileName, wrapped);
  if (rewritten.parseDiagnostics.length > 0) return once; // never ship a bad rewrite
  return formatSourceFile(rewritten, options);
}
