// Map a source line to a JDWP code location. JDWP's Method.LineTable gives, per
// method, the (codeIndex, lineNumber) pairs from the class's LineNumberTable.
// A breakpoint on a line binds to the lowest code index that reports that line;
// if the exact line has no entry (blank line, comment, brace) we bind to the
// next executable line, which is what javac-based debuggers do.
//
// Port reference for togo/internal/dapserver/linemapping.go.

import type { LineTableEntry } from "../../jdwp/commands.ts";

export interface MethodLines {
  methodId: bigint;
  lines: LineTableEntry[];
}

export interface ResolvedLocation {
  methodId: bigint;
  index: bigint;
  line: number;
}

/**
 * Pick the code location for `requestedLine` across all methods of a class.
 * Prefers an exact line match (lowest code index); failing that, the next
 * executable line at or after the request. Returns null when nothing at or
 * after the line exists (the breakpoint stays unverified).
 */
export function resolveLine(
  methods: MethodLines[],
  requestedLine: number,
): ResolvedLocation | null {
  let best: ResolvedLocation | null = null;
  for (const m of methods) {
    for (const e of m.lines) {
      if (e.lineNumber < requestedLine) continue;
      const candidate: ResolvedLocation = {
        methodId: m.methodId,
        index: e.lineCodeIndex,
        line: e.lineNumber,
      };
      if (best === null || candidate.line < best.line) {
        best = candidate;
      } else if (candidate.line === best.line && candidate.index < best.index) {
        best = candidate;
      }
    }
  }
  return best;
}
