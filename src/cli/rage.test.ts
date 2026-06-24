import { test } from "node:test";

import { expect } from "expect";

import { rageReport } from "./rage.ts";
import pkg from "../../package.json" with { type: "json" };

test("rageReport includes version, runtime, platform and the tracker URL", () => {
  const report = rageReport();
  expect(report).toContain(`cappu ${pkg.version}`);
  expect(report).toContain(`node ${process.version}`);
  expect(report).toContain(`${process.platform} ${process.arch}`);
  expect(report).toContain(pkg.bugs.url);
});
