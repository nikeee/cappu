import { rmSync } from "node:fs";
import TempDir from "../TempDir.ts";
import { test } from "node:test";

import { expect } from "expect";

import { coordinatesToString, toCoordinates } from "../packages/index.ts";
import {
  cachedFetchJson,
  cveAliases,
  type FetchJson,
  fixedVersionsOf,
  OsvSource,
  osvSeverity,
} from "./osv.ts";

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
    toCoordinates("org.foo", "foo", "1.2"),
    toCoordinates("org.bar", "bar", "2.0"),
    toCoordinates("org.clean", "clean", "9.0"),
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

test("cachedFetchJson caches vuln details but never the querybatch lookup", async () => {
  using store = TempDir.create("cappu-osv-");
  const previous = process.env.CAPPU_PACKAGE_STORE;
  process.env.CAPPU_PACKAGE_STORE = store.path;
  try {
    let calls = 0;
    const inner: FetchJson = (_url, body) => {
      calls++;
      return Promise.resolve(body === undefined ? { id: "GHSA-x", summary: "s" } : { results: [] });
    };
    const cached = cachedFetchJson(inner);

    const first = await cached("https://api.osv.dev/v1/vulns/GHSA-x");
    const second = await cached("https://api.osv.dev/v1/vulns/GHSA-x");
    expect(second).toEqual(first);
    expect(calls).toBe(1); // the second read came from disk

    // the affected-version lookup is never cached: fresh findings must surface
    await cached("https://api.osv.dev/v1/querybatch", { queries: [] });
    await cached("https://api.osv.dev/v1/querybatch", { queries: [] });
    expect(calls).toBe(3);
  } finally {
    if (previous === undefined) delete process.env.CAPPU_PACKAGE_STORE;
    else process.env.CAPPU_PACKAGE_STORE = previous;
    rmSync(store.path, { recursive: true, force: true });
  }
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
      toCoordinates("g", "a", "1.5"),
    ),
  ).toEqual(["2"]); // only the matching package's fix
});

test("osvSeverity buckets the label, then the CVSS base score", () => {
  expect(osvSeverity({ id: "x", database_specific: { severity: "CRITICAL" } })).toBe("critical");
  expect(osvSeverity({ id: "x", database_specific: { severity: "low" } })).toBe("low");
  // No label: bucket the CVSS base score the way GHSA/npm do.
  const cvss = (score: string) => osvSeverity({ id: "x", severity: [{ type: "CVSS_V3", score }] });
  expect(cvss("9.5")).toBe("critical");
  expect(cvss("7.5")).toBe("high");
  expect(cvss("4.5")).toBe("moderate");
  expect(cvss("2.0")).toBe("low");
  // A CVSS vector string is not a bare numeric score -> unknown.
  expect(cvss("CVSS:3.1/AV:N/AC:L")).toBe("unknown");
});

test("advisory summary falls back to the first line of details, then a placeholder", async () => {
  // toAdvisory is internal; drive it through findVulnerabilities. The vuln
  // details serve as the summary source when no summary is present.
  const details: Record<string, unknown> = {
    "GHSA-det": { id: "GHSA-det", details: "first line\nsecond line" },
    "GHSA-none": { id: "GHSA-none" }, // neither summary nor details
  };
  const fetchJson = (url: string, body?: unknown): Promise<unknown> => {
    if (url.endsWith("/v1/querybatch")) {
      const queries = (body as { queries: { package: { name: string } }[] }).queries;
      return Promise.resolve({
        results: queries.map(q =>
          q.package.name === "g:det"
            ? { vulns: [{ id: "GHSA-det" }] }
            : q.package.name === "g:none"
              ? { vulns: [{ id: "GHSA-none" }] }
              : {},
        ),
      });
    }
    return Promise.resolve(details[url.split("/v1/vulns/")[1]!]);
  };
  const source = new OsvSource(fetchJson);
  const coords = [toCoordinates("g", "det", "1"), toCoordinates("g", "none", "1")];
  const result = await source.findVulnerabilities(coords);
  expect(result.get(coordinatesToString(coords[0]!))![0]!.summary).toBe("first line");
  expect(result.get(coordinatesToString(coords[1]!))![0]!.summary).toBe("(no summary)");
});
