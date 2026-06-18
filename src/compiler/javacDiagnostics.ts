// javac stderr -> located diagnostics. A leaf module (no compiler imports) so
// both the compiler driver and the annotation-processing runner can use it
// without a cycle.

/** A source diagnostic located for display (1-based line/column). */
export interface CompileDiagnostic {
  severity: "error" | "warning";
  /** The input file the diagnostic belongs to, if it has one. */
  file?: string;
  line?: number;
  column?: number;
  code?: number;
  message: string;
}

/**
 * Parse javac's stderr: located `file:line: error|warning: msg` lines map
 * 1:1; indented continuations (carets, code excerpts, stack traces) and the
 * `N errors` summary are dropped. If nothing located parses but something
 * was printed, that something collapses into one unlocated error.
 */
export function parseJavacDiagnostics(stderr: string): CompileDiagnostic[] {
  const diagnostics: CompileDiagnostic[] = [];
  const leftovers: string[] = [];
  for (const line of stderr.split("\n")) {
    const match = /^(.+?):(\d+): (error|warning): (.*)$/.exec(line);
    if (match) {
      diagnostics.push({
        severity: match[3] === "warning" ? "warning" : "error",
        file: match[1]!,
        line: Number(match[2]),
        message: match[4]!,
      });
    } else if (line.trim() && !/^\s/.test(line) && !/^\d+ (error|warning)s?$/.test(line)) {
      leftovers.push(line);
    }
  }
  if (diagnostics.length === 0 && leftovers.length > 0) {
    diagnostics.push({ severity: "error", message: leftovers.join(" ") });
  }
  return diagnostics;
}
