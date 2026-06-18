// Parent-chain resolution against real Maven Central POMs, snapshotted under
// test-fixtures/packages/central-poms (same maven2 layout as the live repo,
// saved by resolving each artifact once with a recording fetchText). These
// libraries cover the version-declaration styles Central actually serves:
// properties up the parent chain (jackson, httpclient5), grandparent
// dependencyManagement (commons-io), and plain literal versions (guava, gson).

import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import { MavenRepositorySource } from "./maven.ts";
import { toCoordinates } from "./types.ts";

const BASE = "https://central.example/maven2";
const FIXTURES = join(import.meta.dirname, "../../test-fixtures/packages/central-poms");

const source = new MavenRepositorySource(BASE, async url => {
  const file = join(FIXTURES, url.slice(BASE.length + 1));
  return existsSync(file) ? readFileSync(file, "utf8") : undefined;
});

/** The dependencies `cappu install` would follow: non-optional compile/runtime. */
async function compileDependencies(groupId: string, artifactId: string, version: string) {
  const metadata = await source.getMetadata(toCoordinates(groupId, artifactId, version));
  expect(metadata).toBeDefined();
  return metadata!.dependencies
    .filter(
      d => !d.optional && (d.scope === undefined || d.scope === "compile" || d.scope === "runtime"),
    )
    .map(d => `${d.groupId}:${d.artifactId}@${d.version}`);
}

test("jackson-databind: versions come from properties in the jackson-bom grandparent", async () => {
  // the databind pom declares ${jackson.version.core} etc.; without the
  // parent chain none of these resolved
  expect(
    await compileDependencies("com.fasterxml.jackson.core", "jackson-databind", "2.18.3"),
  ).toEqual([
    "com.fasterxml.jackson.core:jackson-annotations@2.18.3",
    "com.fasterxml.jackson.core:jackson-core@2.18.3",
  ]);
});

test("httpclient5: versions come from httpclient5-parent properties", async () => {
  expect(
    await compileDependencies("org.apache.httpcomponents.client5", "httpclient5", "5.4.3"),
  ).toEqual([
    "org.apache.httpcomponents.core5:httpcore5@5.3.4",
    "org.apache.httpcomponents.core5:httpcore5-h2@5.3.4",
    "org.slf4j:slf4j-api@1.7.36",
  ]);
});

test("guava: literal versions resolve completely", async () => {
  const metadata = await source.getMetadata(
    toCoordinates("com.google.guava", "guava", "33.4.8-jre"),
  );
  expect(metadata?.incomplete).toBe(false);
  expect(await compileDependencies("com.google.guava", "guava", "33.4.8-jre")).toEqual([
    "com.google.guava:failureaccess@1.0.3",
    "com.google.guava:listenablefuture@9999.0-empty-to-avoid-conflict-with-guava",
    "org.jspecify:jspecify@1.0.0",
    "com.google.errorprone:error_prone_annotations@2.36.0",
    "com.google.j2objc:j2objc-annotations@3.0.0",
  ]);
});

test("gson: compile dependencies resolve through gson-parent", async () => {
  expect(await compileDependencies("com.google.code.gson", "gson", "2.13.1")).toEqual([
    "com.google.errorprone:error_prone_annotations@2.38.0",
  ]);
});

test("commons-io: only test-scoped dependencies, none of them compile/runtime", async () => {
  const metadata = await source.getMetadata(toCoordinates("commons-io", "commons-io", "2.19.0"));
  // versions come from commons-parent's dependencyManagement and the junit
  // BOM it imports (scope=import) - every declared dependency resolves
  expect(metadata?.dependencies.length).toBe(9);
  expect(metadata?.dependencies.every(d => d.scope === "test")).toBe(true);
  expect(metadata?.incomplete).toBe(false);
  expect(await compileDependencies("commons-io", "commons-io", "2.19.0")).toEqual([]);
});

test("every snapshot artifact resolves all declared dependencies (BOMs followed)", async () => {
  for (const [groupId, artifactId, version] of [
    ["com.fasterxml.jackson.core", "jackson-databind", "2.18.3"],
    ["org.apache.httpcomponents.client5", "httpclient5", "5.4.3"],
    ["com.google.guava", "guava", "33.4.8-jre"],
    ["com.google.code.gson", "gson", "2.13.1"],
  ] as const) {
    const metadata = await source.getMetadata(toCoordinates(groupId, artifactId, version));
    expect(metadata?.incomplete).toBe(false);
  }
});
