import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import { loadConfig } from "../config.ts";
import { planUpdates } from "../install.ts";
import {
  type Coordinates,
  type DependencyDeclaration,
  InMemoryPackageSource,
  type PackageMetadata,
  toCoordinates,
} from "../packages/index.ts";
import { applyBumpsToJsonc } from "./update.ts";

function coord(spec: string): Coordinates {
  const [groupId = "", artifactId = "", version = ""] = spec.split(":");
  return toCoordinates(groupId, artifactId, version);
}
function pkg(spec: string, deps: string[] = []): PackageMetadata {
  return {
    coordinates: coord(spec),
    dependencies: deps.map(d => coord(d) as DependencyDeclaration),
  };
}

// a, with a compatible bump (1.5) and a conflicting one (2.0); b pins shared
// to 1.0, so a@2.0 (which needs shared 2.0) must be rejected. c's only newer
// release is a pre-release. Insertion order is the publish order.
function source(): InMemoryPackageSource {
  return new InMemoryPackageSource("mem", [
    pkg("g:shared:1.0"),
    pkg("g:shared:2.0"),
    pkg("g:a:1.0", ["g:shared:1.0"]),
    pkg("g:a:1.5", ["g:shared:1.0"]),
    pkg("g:a:2.0", ["g:shared:2.0"]),
    pkg("g:b:1.0", ["g:shared:1.0"]),
    pkg("g:c:1.0"),
    pkg("g:c:2.0-beta1"),
  ]);
}

test("update picks the newest conflict-free stable version per dependency", async () => {
  const dir = mkdtempSync(join(tmpdir(), "cappu-update-"));
  try {
    writeFileSync(
      join(dir, "cappu.json"),
      JSON.stringify({
        dependencies: { implementation: { "g:a": "1.0", "g:b": "1.0", "g:c": "1.0" } },
      }),
    );
    const bumps = await planUpdates(loadConfig(undefined, dir), [source()]);
    // a -> 1.5 (2.0 would force shared 2.0, conflicting with b's shared 1.0);
    // b is already newest; c's only newer release is a pre-release -> skipped
    expect(bumps).toEqual([
      { configuration: "implementation", key: "g:a", from: "1.0", to: "1.5" },
    ]);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("update stays within the current major version", async () => {
  const dir = mkdtempSync(join(tmpdir(), "cappu-update-"));
  try {
    writeFileSync(
      join(dir, "cappu.json"),
      JSON.stringify({ dependencies: { implementation: { "g:d": "1.0" } } }),
    );
    // 2.0 is a clean, conflict-free bump, but a major jump - it must be skipped
    // in favour of the newest within major 1 (1.4).
    const src = new InMemoryPackageSource("mem", [pkg("g:d:1.0"), pkg("g:d:1.4"), pkg("g:d:2.0")]);
    const bumps = await planUpdates(loadConfig(undefined, dir), [src]);
    expect(bumps).toEqual([
      { configuration: "implementation", key: "g:d", from: "1.0", to: "1.4" },
    ]);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("applyBumpsToJsonc overwrites versions and keeps comments", () => {
  const text = `{
  // my deps
  "dependencies": {
    "implementation": {
      "g:a": "1.0", // pin
    },
    "testImplementation": {
      "g:t": "3.0",
    },
  },
}
`;
  const out = applyBumpsToJsonc(text, [
    { configuration: "implementation", key: "g:a", from: "1.0", to: "1.5" },
    { configuration: "testImplementation", key: "g:t", from: "3.0", to: "3.1" },
  ]);
  expect(out).toContain("// my deps");
  expect(out).toContain("// pin");
  expect(out).toContain('"g:a": "1.5"');
  expect(out).toContain('"g:t": "3.1"');
  expect(out).not.toContain('"1.0"');
});
