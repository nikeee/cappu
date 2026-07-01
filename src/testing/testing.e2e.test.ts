// End-to-end: with testOptions.outputFormat "junit", run the real JUnit Console
// Launcher over a compiled test and assert a valid junit-XML report file is
// emitted into the resolved reportsDir. Exercises the --reports-dir wiring in
// testRunArgs against the actual launcher (the standalone bundles the jupiter
// API+engine, so the sample test compiles and runs against the launcher jar
// alone - no separate junit download). Gated on javac + java; the launcher is
// fetched from the configured package sources, so the test skips if that (the
// one network step) fails.

import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import TempDir from "../TempDir.ts";
import { loadConfig } from "../config.ts";
import { consoleLauncherJar, resolveJava, testClassesDir, testRunArgs } from "./testing.ts";

// isolate the launcher download in a throwaway store
const STORE = TempDir.create("cappu-test-e2e-store-").path;
process.env.CAPPU_PACKAGE_STORE = STORE;

const HAS_JAVAC = (() => {
  try {
    execFileSync("javac", ["-version"], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
})();

const SAMPLE_TEST = `
import org.junit.jupiter.api.Test;
import static org.junit.jupiter.api.Assertions.assertEquals;

class SampleTest {
  @Test void addition() { assertEquals(2, 1 + 1); }
}
`;

test(
  'testOptions.outputFormat "junit" emits a valid junit-XML report',
  { skip: !HAS_JAVAC, timeout: 120_000 },
  async () => {
    using project = TempDir.create("cappu-test-e2e-");
    const dir = project.path;
    const config = loadConfig(undefined, dir);
    config.testOptions.outputFormat = "junit";
    config.testOptions.reportsDir = "./dist/test-results";

    // the standalone launcher bundles the jupiter API+engine: the one network
    // step. Skip the whole test if it cannot be fetched.
    let launcher: string;
    try {
      launcher = await consoleLauncherJar(config);
    } catch {
      return; // offline / no source: nothing to assert
    }

    // compile the sample test against the launcher jar into the runtime dir
    const classes = testClassesDir(config);
    mkdirSync(classes, { recursive: true });
    const src = join(dir, "SampleTest.java");
    writeFileSync(src, SAMPLE_TEST);
    execFileSync("javac", ["-cp", launcher, "-d", classes, src], { stdio: "ignore" });

    const result = spawnSync(resolveJava(config), testRunArgs(config, launcher), { stdio: "ignore" });
    expect(result.status).toBe(0);

    // a report file exists in the resolved reportsDir and is a well-formed
    // junit-XML testsuite recording the one passing test
    const reportsDir = join(dir, "dist", "test-results");
    expect(existsSync(reportsDir)).toBe(true);
    const xmlFiles = readdirSync(reportsDir).filter(f => f.endsWith(".xml"));
    expect(xmlFiles.length).toBeGreaterThan(0);
    const xml = readFileSync(join(reportsDir, xmlFiles[0]!), "utf8");
    expect(xml.trimStart().startsWith("<?xml")).toBe(true);
    expect(xml).toMatch(/<testsuite[\s>]/);
    expect(xml).toContain('tests="1"');
    expect(xml).toContain('failures="0"');
    expect(xml).toContain("addition");
  },
);
