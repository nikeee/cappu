import { test } from "node:test";

import { expect } from "expect";

import {
  InMemoryPackageSource,
  latestVersion,
  resolveTransitive,
  searchPackages,
} from "./resolver.ts";
import { type Coordinates, coordinatesToString, type PackageMetadata } from "./types.ts";

function pkg(
  spec: string,
  dependencies: (string | { spec: string; scope?: string; optional?: boolean })[] = [],
): PackageMetadata {
  const parse = (s: string): Coordinates => {
    const [groupId, artifactId, version] = s.split(":");
    return { groupId: groupId!, artifactId: artifactId!, version: version! };
  };
  return {
    coordinates: parse(spec),
    dependencies: dependencies.map(d =>
      typeof d === "string" ? parse(d) : { ...parse(d.spec), scope: d.scope, optional: d.optional },
    ),
  };
}

const c = (spec: string): Coordinates => pkg(spec).coordinates;

test("a transitive chain resolves depth-first packages in breadth-first order", async () => {
  const source = new InMemoryPackageSource("test", [
    pkg("org.a:a:1", ["org.b:b:1"]),
    pkg("org.b:b:1", ["org.c:c:1"]),
    pkg("org.c:c:1"),
  ]);
  const resolution = await resolveTransitive([c("org.a:a:1")], [source]);
  expect(resolution.packages.map(p => coordinatesToString(p.coordinates))).toEqual([
    "org.a:a:1",
    "org.b:b:1",
    "org.c:c:1",
  ]);
  expect(resolution.packages.map(p => p.depth)).toEqual([0, 1, 2]);
  expect(resolution.packages[1]!.requestedBy).toEqual(c("org.a:a:1").valueOf());
  expect(resolution.conflicts).toEqual([]);
  expect(resolution.missing).toEqual([]);
});

test("onResolve fires once per resolved package, in discovery order", async () => {
  const source = new InMemoryPackageSource("test", [
    pkg("org.a:a:1", ["org.b:b:1", "org.c:c:1"]),
    pkg("org.b:b:1", ["org.c:c:1"]), // c reached twice but resolved once
    pkg("org.c:c:1"),
    pkg("org.x:x:1"), // a missing dep is still reported (we attempt it)
  ]);
  const seen: string[] = [];
  const resolution = await resolveTransitive(
    [c("org.a:a:1"), c("org.missing:m:1")],
    [source],
    coordinates => seen.push(coordinatesToString(coordinates)),
  );
  // one notification per UNIQUE package (c only once), including the missing one
  expect(seen).toEqual(["org.a:a:1", "org.missing:m:1", "org.b:b:1", "org.c:c:1"]);
  // and it matches what actually got resolved + attempted
  expect(seen.length).toBe(resolution.packages.length + resolution.missing.length);
});

test("nearest version wins a diamond conflict; the loser is recorded", async () => {
  const source = new InMemoryPackageSource("test", [
    pkg("org.a:a:1", ["org.b:b:1", "org.c:c:1"]),
    pkg("org.b:b:1"),
    pkg("org.b:b:2"),
    pkg("org.c:c:1", ["org.b:b:2"]), // farther from the root than a's direct b:1
  ]);
  const resolution = await resolveTransitive([c("org.a:a:1")], [source]);
  const names = resolution.packages.map(p => coordinatesToString(p.coordinates));
  expect(names).toContain("org.b:b:1");
  expect(names).not.toContain("org.b:b:2");
  expect(resolution.conflicts).toEqual([
    { key: "org.b:b", selected: "1", rejected: "2", rejectedBy: c("org.c:c:1") },
  ]);
});

test("dependency cycles terminate", async () => {
  const source = new InMemoryPackageSource("test", [
    pkg("org.a:a:1", ["org.b:b:1"]),
    pkg("org.b:b:1", ["org.a:a:1"]),
  ]);
  const resolution = await resolveTransitive([c("org.a:a:1")], [source]);
  expect(resolution.packages).toHaveLength(2);
});

test("test-scoped and optional dependencies do not propagate", async () => {
  const source = new InMemoryPackageSource("test", [
    pkg("org.a:a:1", [
      { spec: "org.t:t:1", scope: "test" },
      { spec: "org.o:o:1", optional: true },
      { spec: "org.r:r:1", scope: "runtime" },
    ]),
    pkg("org.r:r:1"),
  ]);
  const resolution = await resolveTransitive([c("org.a:a:1")], [source]);
  expect(resolution.packages.map(p => coordinatesToString(p.coordinates))).toEqual([
    "org.a:a:1",
    "org.r:r:1",
  ]);
  expect(resolution.missing).toEqual([]);
});

test("sources are consulted in order; later sources fill gaps", async () => {
  const primary = new InMemoryPackageSource("primary", [pkg("org.a:a:1", ["org.b:b:1"])]);
  const fallback = new InMemoryPackageSource("fallback", [pkg("org.a:a:1"), pkg("org.b:b:1")]);
  const resolution = await resolveTransitive([c("org.a:a:1")], [primary, fallback]);
  expect(resolution.packages.map(p => [coordinatesToString(p.coordinates), p.source])).toEqual([
    ["org.a:a:1", "primary"], // primary wins for a (and its dependency list is used)
    ["org.b:b:1", "fallback"],
  ]);
});

test("unresolvable coordinates are reported once with their requester", async () => {
  const source = new InMemoryPackageSource("test", [
    pkg("org.a:a:1", ["org.gone:gone:9", "org.gone:gone:9"]),
  ]);
  const resolution = await resolveTransitive([c("org.a:a:1")], [source]);
  expect(resolution.missing).toEqual([
    { coordinates: c("org.gone:gone:9"), requestedBy: c("org.a:a:1") },
  ]);
});

test("search merges sources and dedupes by group:artifact", async () => {
  const primary = new InMemoryPackageSource("primary", [pkg("org.x:json-lib:2")]);
  const fallback = new InMemoryPackageSource("fallback", [
    pkg("org.x:json-lib:1"),
    pkg("org.y:json-other:1"),
  ]);
  const hits = await searchPackages("json", [primary, fallback]);
  expect(hits.map(coordinatesToString)).toEqual(["org.x:json-lib:2", "org.y:json-other:1"]);
});

test("latestVersion picks the newest from the first source that knows the package", async () => {
  const source = new InMemoryPackageSource("test", [pkg("org.a:a:1"), pkg("org.a:a:2")]);
  expect(await latestVersion("org.a", "a", [source])).toBe("2");
  expect(await latestVersion("org.nope", "a", [source])).toBeUndefined();
});
