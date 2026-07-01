import { test } from "node:test";

import { expect } from "expect";

import { checkDateTimePattern } from "./dateTimePattern.ts";

test("valid patterns report nothing", () => {
  const r = checkDateTimePattern("yyyy-MM-dd HH:mm:ss");
  expect(r.invalidLetters).toEqual([]);
  expect(r.footguns).toEqual([]);
});

test("unknown pattern letters are reported", () => {
  expect(checkDateTimePattern("yyyy-jj").invalidLetters).toContain("j");
});

test("quoted literals are ignored", () => {
  const r = checkDateTimePattern("yyyy 'at' HH'h'");
  expect(r.invalidLetters).toEqual([]); // the letters in 'at'/'h' are literal
});

test("Y without a week field is flagged as a footgun", () => {
  const r = checkDateTimePattern("YYYY-MM-dd");
  expect(r.footguns.map(f => f.letter)).toContain("Y");
  // ...but Y with a week field is legitimate
  expect(checkDateTimePattern("YYYY-'W'ww").footguns).toEqual([]);
});

test("D alongside a month is flagged (day-of-year vs day-of-month)", () => {
  expect(checkDateTimePattern("yyyy-MM-DD").footguns.map(f => f.letter)).toContain("D");
});

test("h without am/pm is flagged (12h vs 24h)", () => {
  expect(checkDateTimePattern("hh:mm").footguns.map(f => f.letter)).toContain("h");
  expect(checkDateTimePattern("hh:mm a").footguns).toEqual([]);
});
