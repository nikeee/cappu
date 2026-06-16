import { test } from "node:test";

import { expect } from "expect";

import { bumpSemver } from "./version.ts";

test("bumpSemver bumps the right component and resets the lower ones", () => {
  expect(bumpSemver("1.2.3", "major")).toBe("2.0.0");
  expect(bumpSemver("1.2.3", "minor")).toBe("1.3.0");
  expect(bumpSemver("1.2.3", "patch")).toBe("1.2.4");
});

test("a release drops pre-release / build metadata", () => {
  expect(bumpSemver("1.2.3-SNAPSHOT", "patch")).toBe("1.2.4");
  expect(bumpSemver("2.0.0-rc.1+build.7", "minor")).toBe("2.1.0");
});

test("a non-semver version is rejected", () => {
  expect(() => bumpSemver("1.2", "patch")).toThrow(/semver/);
});
