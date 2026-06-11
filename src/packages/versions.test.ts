import { test } from "node:test";

import { expect } from "expect";

import { matchesVersionSpec, matchingVersions } from "./versions.ts";

test("a spec matches itself and segment-wise refinements only", () => {
  expect(matchesVersionSpec("2", "2")).toBe(true);
  expect(matchesVersionSpec("2", "2.10.1")).toBe(true);
  expect(matchesVersionSpec("2", "2-rc1")).toBe(true);
  expect(matchesVersionSpec("2.1", "2.1.3")).toBe(true);
  expect(matchesVersionSpec("2.1", "2.10.1")).toBe(false); // not a segment prefix
  expect(matchesVersionSpec("2", "20.0")).toBe(false);
});

test("matchingVersions filters and returns newest (publish order) first", () => {
  const published = ["1.0", "2.0", "2.1", "2.10", "3.0"];
  expect(matchingVersions(published, "2")).toEqual(["2.10", "2.1", "2.0"]);
  expect(matchingVersions(published)).toEqual(["3.0", "2.10", "2.1", "2.0", "1.0"]);
  expect(matchingVersions(published, "9")).toEqual([]);
});
