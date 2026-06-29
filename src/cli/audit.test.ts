import { test } from "node:test";

import { expect } from "expect";

import { type Advisory, type AuditReport } from "../audit/index.ts";
import {
  type Coordinates,
  packageKey,
  type ResolvedPackage,
  toCoordinates,
} from "../packages/index.ts";
import { buildAuditSarif } from "./audit.ts";

const coord = (spec: string): Coordinates => {
  const [g, a, v] = spec.split(":");
  return toCoordinates(g!, a!, v!);
};

const advisory = (a: Partial<Advisory> & Pick<Advisory, "id" | "severity">): Advisory => ({
  aliases: [],
  summary: "",
  fixedVersions: [],
  url: "",
  ...a,
});

test("buildAuditSarif emits a code-scanning SARIF 2.1.0 log", () => {
  const root = coord("org.app:app:1.0");
  const vuln = coord("org.apache.logging.log4j:log4j-core:2.14.1");
  // dependencyPath only reads coordinates/requestedBy, so stub the rest.
  const byKey = new Map<string, ResolvedPackage>([
    [packageKey(root), { coordinates: root } as ResolvedPackage],
    [packageKey(vuln), { coordinates: vuln, requestedBy: root } as ResolvedPackage],
  ]);
  const report: AuditReport = {
    scanned: 2,
    counts: { critical: 1, high: 0, moderate: 0, low: 1, unknown: 0 },
    vulnerable: [
      {
        coordinates: vuln,
        advisories: [
          advisory({
            id: "GHSA-jfh8-c2jp-5v3q" as Advisory["id"],
            aliases: ["CVE-2021-44228"],
            summary: "Log4Shell",
            severity: "critical",
            fixedVersions: ["2.15.0"],
            url: "https://example/ghsa",
          }),
          advisory({ id: "GHSA-minor" as Advisory["id"], summary: "minor issue", severity: "low" }),
        ],
      },
    ],
  };

  // buildAuditSarif returns `object`; narrow it for the assertions below.
  const sarif = buildAuditSarif(report, byKey, "9.9.9") as {
    $schema: string;
    version: string;
    runs: {
      tool: {
        driver: {
          name: string;
          version: string;
          rules: { id: string; properties: { "security-severity"?: string } }[];
        };
      };
      results: {
        ruleId: string;
        level: string;
        message: { text: string };
        locations: { physicalLocation: { artifactLocation: { uri: string } } }[];
        properties: { coordinate: string; severity: string; path: string[] };
      }[];
    }[];
  };

  expect(sarif.version).toBe("2.1.0");
  expect(sarif.$schema).toContain("sarif-2.1.0");
  expect(sarif.runs).toHaveLength(1);

  const driver = sarif.runs[0]!.tool.driver;
  expect(driver.name).toBe("cappu");
  expect(driver.version).toBe("9.9.9");
  expect(driver.rules).toHaveLength(2); // one rule per distinct advisory
  const scores = Object.fromEntries(
    driver.rules.map(r => [r.id, r.properties["security-severity"]]),
  );
  expect(scores["GHSA-jfh8-c2jp-5v3q"]).toBe("9.0");
  expect(scores["GHSA-minor"]).toBe("1.0");

  const results = sarif.runs[0]!.results;
  expect(results).toHaveLength(2);
  const crit = results.find(r => r.ruleId === "GHSA-jfh8-c2jp-5v3q")!;
  expect(crit.level).toBe("error");
  expect(crit.properties.severity).toBe("critical");
  expect(crit.locations[0]!.physicalLocation.artifactLocation.uri).toBe("cappu.json");
  expect(crit.properties.path).toEqual([
    "org.app:app:1.0",
    "org.apache.logging.log4j:log4j-core:2.14.1",
  ]);
  expect(crit.message.text).toContain("CVE-2021-44228");
  expect(crit.message.text).toContain("log4j-core:2.14.1");
  expect(crit.message.text).toContain("Fixed in: 2.15.0.");
});
