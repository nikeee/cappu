// The parser discards trivia, so comments are not in the AST. To avoid losing
// them when reformatting, this pass recovers every comment from the source with
// its offset, classified as "own line" (stands on its own, attaches to the
// following construct) or "trailing" (sits after code on the same line). The
// printer re-emits them at member/statement granularity.
//
// Comments are found in the gaps between scanner tokens, so text inside string
// and character literals is never mistaken for a comment.

import { createScanner } from "../compiler/scanner.ts";
import { SyntaxKind } from "../compiler/types.ts";

export interface Comment {
  /** Offset of the comment's first character. */
  pos: number;
  /** Offset just past the comment. */
  end: number;
  /** The verbatim comment text: a line comment or a block/javadoc comment. */
  text: string;
  /** A `//` line comment (vs a block comment). */
  line: boolean;
  /** True when only whitespace precedes the comment on its line (a standalone comment). */
  ownLine: boolean;
}

export function collectComments(source: string): Comment[] {
  const comments: Comment[] = [];
  const scanner = createScanner(source, () => {});
  scanner.setText(source);
  let prevEnd = 0;
  for (;;) {
    const kind = scanner.scan();
    const start = kind === SyntaxKind.EndOfFileToken ? source.length : scanner.getTokenStart();
    extractFromGap(source, prevEnd, start, comments);
    if (kind === SyntaxKind.EndOfFileToken) break;
    prevEnd = scanner.getTokenEnd();
  }
  return comments;
}

function extractFromGap(source: string, from: number, to: number, out: Comment[]): void {
  let i = from;
  // A comment is on its own line when nothing but whitespace precedes it back to
  // the last newline (or the gap began right after the previous token's line).
  let sawNewlineSinceCode = from === 0;
  while (i < to) {
    const ch = source.charCodeAt(i);
    if (ch === 0x0a) {
      sawNewlineSinceCode = true;
      i++;
      continue;
    }
    if (ch === 0x20 || ch === 0x09 || ch === 0x0d || ch === 0x0c || ch === 0x0b) {
      i++;
      continue;
    }
    if (ch === 0x2f /* / */ && source.charCodeAt(i + 1) === 0x2f /* / */) {
      let j = i + 2;
      while (j < to && source.charCodeAt(j) !== 0x0a) j++;
      const text = source.slice(i, j).replace(/\s+$/, "");
      out.push({ pos: i, end: j, text, line: true, ownLine: sawNewlineSinceCode });
      i = j;
      sawNewlineSinceCode = false;
      continue;
    }
    if (ch === 0x2f /* / */ && source.charCodeAt(i + 1) === 0x2a /* * */) {
      let j = i + 2;
      while (j < to && !(source.charCodeAt(j) === 0x2a && source.charCodeAt(j + 1) === 0x2f)) j++;
      j = Math.min(j + 2, to);
      out.push({
        pos: i,
        end: j,
        text: source.slice(i, j),
        line: false,
        ownLine: sawNewlineSinceCode,
      });
      i = j;
      sawNewlineSinceCode = false;
      continue;
    }
    // Any other character would be code, which the scanner tokenizes; gaps only
    // hold whitespace and comments, so this is unreachable in practice.
    i++;
  }
}
