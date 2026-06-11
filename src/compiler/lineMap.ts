// Mapping between character offsets (used throughout the parser) and LSP
// line/character positions (0-based, UTF-16 code units - which match JS string
// indices). Mirrors the TS compiler's computeLineStarts / getLineAndCharacter.

import { type Brand } from "../brand.ts";

/**
 * A character offset into a source text (what Node.pos/end and the scanner
 * use), as opposed to a line/character pair - the two number spaces this
 * module converts between and that must never be mixed.
 */
export type Offset = Brand<number, "Offset">;

export interface LineAndCharacter {
  readonly line: number;
  readonly character: number;
}

const enum Ch {
  LineFeed = 0x0a,
  CarriageReturn = 0x0d,
}

/** Offsets at which each line begins. Line 0 starts at offset 0. */
export function computeLineStarts(text: string): number[] {
  const result: number[] = [0];
  let pos = 0;
  while (pos < text.length) {
    const ch = text.charCodeAt(pos);
    pos++;
    if (ch === Ch.CarriageReturn) {
      if (text.charCodeAt(pos) === Ch.LineFeed) pos++;
      result.push(pos);
    } else if (ch === Ch.LineFeed) {
      result.push(pos);
    }
  }
  return result;
}

// Greatest index i with lineStarts[i] <= offset.
function lineIndexOf(lineStarts: readonly number[], offset: number): number {
  let low = 0;
  let high = lineStarts.length - 1;
  while (low < high) {
    const mid = (low + high + 1) >> 1;
    if (lineStarts[mid]! <= offset) {
      low = mid;
    } else {
      high = mid - 1;
    }
  }
  return low;
}

export function getLineAndCharacterOfPosition(
  lineStarts: readonly number[],
  offset: number,
): LineAndCharacter {
  const line = lineIndexOf(lineStarts, offset);
  return { line, character: offset - lineStarts[line]! };
}

export function getPositionOfLineAndCharacter(
  lineStarts: readonly number[],
  line: number,
  character: number,
): Offset {
  if (line < 0) return 0 as Offset;
  if (line >= lineStarts.length) return lineStarts.at(-1)! as Offset;
  return (lineStarts[line]! + character) as Offset;
}
