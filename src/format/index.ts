// Public entry point for the Java source formatter. Parses with cappu's own
// parser and regenerates layout via the Doc IR (printer.ts / doc.ts), targeting
// google-java-format compatibility. Default style is "google" (2-space indent);
// "aosp" is the 4-space variant.

import { parseSourceFile } from "../compiler/parser.ts";
import { type FormatOptions, formatSourceFile, UnsupportedSyntaxError } from "./printer.ts";

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
  return formatSourceFile(sf, options);
}
