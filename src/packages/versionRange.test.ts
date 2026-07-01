import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { compareVersions, parseVersionSpec, satisfies, selectVersion } from "./versionRange.ts";

const lt = (a: string, b: string): void => {
  assert.ok(compareVersions(a, b) < 0, `${a} < ${b}`);
  assert.ok(compareVersions(b, a) > 0, `${b} > ${a}`);
};
const eq = (a: string, b: string): void => {
  assert.equal(compareVersions(a, b), 0, `${a} == ${b}`);
  assert.equal(compareVersions(b, a), 0, `${b} == ${a}`);
};

describe("compareVersions", () => {
  it("treats trailing-zero segments as equal", () => {
    eq("1.0", "1.0.0");
    eq("1", "1.0.0");
  });

  it("orders numeric segments numerically, not lexically", () => {
    lt("1.9", "1.10");
    lt("2.0", "10.0");
  });

  it("orders pre-release qualifiers before the release", () => {
    lt("1.0-alpha", "1.0");
    lt("1.0-alpha", "1.0-beta");
    lt("1.0-beta", "1.0-milestone");
    lt("1.0-milestone", "1.0-rc");
    lt("1.0-rc", "1.0-snapshot");
    lt("1.0-snapshot", "1.0");
    lt("1.0", "1.0-sp");
  });

  it("ranks a number above a qualifier at the same position", () => {
    lt("1.1-alpha", "1.1");
    lt("1.0-rc1", "1.0.1");
  });

  it("sorts unknown qualifiers after the release", () => {
    lt("1.0", "1.0-xyz");
  });
});

describe("parseVersionSpec", () => {
  it("returns undefined for a plain exact version", () => {
    assert.equal(parseVersionSpec("1.2.3"), undefined);
    assert.equal(parseVersionSpec("2.0-SNAPSHOT"), undefined);
  });

  it("parses RELEASE / LATEST as newest-wins", () => {
    assert.deepEqual(parseVersionSpec("RELEASE"), { newest: true, restrictions: [] });
    assert.deepEqual(parseVersionSpec("LATEST"), { newest: true, restrictions: [] });
  });

  it("returns undefined for a malformed range", () => {
    assert.equal(parseVersionSpec("[1.0"), undefined);
    assert.equal(parseVersionSpec("[]"), undefined);
  });
});

describe("satisfies", () => {
  const sat = (spec: string, version: string): boolean =>
    satisfies(parseVersionSpec(spec)!, version);

  it("[1.0,2.0) is half-open", () => {
    assert.ok(sat("[1.0,2.0)", "1.0"));
    assert.ok(sat("[1.0,2.0)", "1.9.9"));
    assert.ok(!sat("[1.0,2.0)", "2.0"));
    assert.ok(!sat("[1.0,2.0)", "0.9"));
  });

  it("(,2.0] includes the upper bound and has no lower bound", () => {
    assert.ok(sat("(,2.0]", "0.1"));
    assert.ok(sat("(,2.0]", "2.0"));
    assert.ok(!sat("(,2.0]", "2.0.1"));
  });

  it("[1.0,) is unbounded above and inclusive below", () => {
    assert.ok(sat("[1.0,)", "1.0"));
    assert.ok(sat("[1.0,)", "99"));
    assert.ok(!sat("[1.0,)", "0.9"));
  });

  it("[1.5] is a hard single version", () => {
    assert.ok(sat("[1.5]", "1.5"));
    assert.ok(!sat("[1.5]", "1.6"));
    assert.ok(!sat("[1.5]", "1.4"));
  });

  it("comma-joined sets are OR-ed", () => {
    const spec = parseVersionSpec("[1.0,2.0),[3.0,)")!;
    assert.ok(satisfies(spec, "1.5"));
    assert.ok(!satisfies(spec, "2.5"));
    assert.ok(satisfies(spec, "3.1"));
  });

  it("RELEASE is satisfied by any version", () => {
    assert.ok(satisfies(parseVersionSpec("RELEASE")!, "0.0.1"));
  });
});

describe("selectVersion", () => {
  it("picks the highest matching version from an unordered list", () => {
    const published = ["1.0", "3.1", "1.5", "2.0", "1.9"];
    assert.equal(selectVersion(parseVersionSpec("[1.0,2.0)")!, published), "1.9");
    assert.equal(selectVersion(parseVersionSpec("[1.0,)")!, published), "3.1");
    assert.equal(selectVersion(parseVersionSpec("RELEASE")!, published), "3.1");
  });

  it("returns undefined when nothing matches", () => {
    assert.equal(selectVersion(parseVersionSpec("[5.0,6.0)")!, ["1.0", "2.0"]), undefined);
  });
});
