import { test } from "node:test";

import { expect } from "expect";

import { coordinatesToString } from "../packages/index.ts";
import { cveAliases, fixedVersionsOf, OsvSource, osvSeverity } from "./osv.ts";

const VULNS: Record<string, unknown> = {
  "GHSA-aaaa": {
    id: "GHSA-aaaa",
    summary: "RCE in foo",
    aliases: ["CVE-2021-1", "GHSA-aaaa"],
    database_specific: { severity: "CRITICAL" },
    affected: [
      {
        package: { name: "org.foo:foo" },
        ranges: [{ events: [{ introduced: "1.0" }, { fixed: "1.5" }] }],
      },
    ],
  },
  "GHSA-bbbb": {
    id: "GHSA-bbbb",
    summary: "XXE in bar",
    aliases: ["CVE-2022-2"],
    database_specific: { severity: "MODERATE" },
    affected: [],
  },
};

// Records the batch queries and serves canned id lists + vuln details.
function fakeOsv() {
  const queried: unknown[] = [];
  const fetchJson = (url: string, body?: unknown): Promise<unknown> => {
    if (url.endsWith("/v1/querybatch")) {
      queried.push(body);
      const queries = (body as { queries: { package: { name: string } }[] }).queries;
      return Promise.resolve({
        results: queries.map(q => {
          if (q.package.name === "org.foo:foo") return { vulns: [{ id: "GHSA-aaaa" }] };
          if (q.package.name === "org.bar:bar") {
            return { vulns: [{ id: "GHSA-aaaa" }, { id: "GHSA-bbbb" }] }; // shares GHSA-aaaa
          }
          return {}; // clean
        }),
      });
    }
    const id = url.split("/v1/vulns/")[1]!;
    return Promise.resolve(VULNS[id]);
  };
  return { fetchJson, queried };
}

test("OsvSource maps batch ids back to coordinates and hydrates once", async () => {
  const { fetchJson, queried } = fakeOsv();
  let hydrations = 0;
  const counting = (url: string, body?: unknown): Promise<unknown> => {
    if (url.includes("/v1/vulns/")) hydrations++;
    return fetchJson(url, body);
  };
  const source = new OsvSource(counting);

  const coords = [
    { groupId: "org.foo", artifactId: "foo", version: "1.2" },
    { groupId: "org.bar", artifactId: "bar", version: "2.0" },
    { groupId: "org.clean", artifactId: "clean", version: "9.0" },
  ];
  const result = await source.findVulnerabilities(coords);

  expect(result.get(coordinatesToString(coords[0]!))!.map(a => a.id)).toEqual(["GHSA-aaaa"]);
  expect(result.get(coordinatesToString(coords[1]!))!.map(a => a.id)).toEqual([
    "GHSA-aaaa",
    "GHSA-bbbb",
  ]);
  expect(result.has(coordinatesToString(coords[2]!))).toBe(false); // clean: no entry
  // GHSA-aaaa is shared by two packages but fetched only once
  expect(hydrations).toBe(2);
  expect(queried).toHaveLength(1); // one batch

  const foo = result.get(coordinatesToString(coords[0]!))![0]!;
  expect(foo.severity).toBe("critical");
  expect(foo.aliases).toEqual(["CVE-2021-1"]); // only CVE aliases
  expect(foo.fixedVersions).toEqual(["1.5"]);
  expect(foo.url).toBe("https://osv.dev/vulnerability/GHSA-aaaa");
});

test("severity, aliases and fixed-version extraction", () => {
  expect(osvSeverity({ id: "x", database_specific: { severity: "HIGH" } })).toBe("high");
  expect(osvSeverity({ id: "x", database_specific: { severity: "moderate" } })).toBe("moderate");
  expect(osvSeverity({ id: "x" })).toBe("unknown"); // no label, CVSS vector not scorable
  expect(cveAliases({ id: "x", aliases: ["CVE-1", "GHSA-z", "OSV-2"] })).toEqual(["CVE-1"]);
  expect(
    fixedVersionsOf(
      {
        id: "x",
        affected: [
          { package: { name: "g:a" }, ranges: [{ events: [{ introduced: "1" }, { fixed: "2" }] }] },
          { package: { name: "other:x" }, ranges: [{ events: [{ fixed: "9" }] }] },
        ],
      },
      { groupId: "g", artifactId: "a", version: "1.5" },
    ),
  ).toEqual(["2"]); // only the matching package's fix
});
