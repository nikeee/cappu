import { test } from "node:test";
import { expect } from "expect";

import {
  computeLineStarts,
  getLineAndCharacterOfPosition,
  getPositionOfLineAndCharacter,
} from "./lineMap.ts";

test("computeLineStarts handles \\n, \\r\\n and \\r", () => {
  const text = "a\nbb\r\nccc\rd";
  expect(computeLineStarts(text)).toEqual([0, 2, 6, 10]);
});

test("offset -> line/character", () => {
  const text = "ab\ncd\nef";
  const starts = computeLineStarts(text);
  expect(getLineAndCharacterOfPosition(starts, 0)).toEqual({ line: 0, character: 0 });
  expect(getLineAndCharacterOfPosition(starts, 1)).toEqual({ line: 0, character: 1 });
  expect(getLineAndCharacterOfPosition(starts, 3)).toEqual({ line: 1, character: 0 });
  expect(getLineAndCharacterOfPosition(starts, 7)).toEqual({ line: 2, character: 1 });
});

test("line/character -> offset round-trips", () => {
  const text = "package p;\nclass C {\n  int x;\n}\n";
  const starts = computeLineStarts(text);
  for (let offset = 0; offset <= text.length; offset++) {
    const lc = getLineAndCharacterOfPosition(starts, offset);
    expect(getPositionOfLineAndCharacter(starts, lc.line, lc.character)).toBe(offset);
  }
});

test("empty source has a single line start", () => {
  expect(computeLineStarts("")).toEqual([0]);
  expect(getLineAndCharacterOfPosition([0], 0)).toEqual({ line: 0, character: 0 });
});
