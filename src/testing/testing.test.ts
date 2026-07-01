import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { delimiter, join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import TempDir from "../TempDir.ts";
import { loadConfig } from "../config.ts";
import { InMemoryPackageSource, type SourceName } from "../packages/index.ts";
import {
  compileTests,
  compileTestsArgs,
  CONSOLE_LAUNCHER,
  consoleLauncherJar,
  findTestSources,
  mainClassesDir,
  testClassesDir,
  testRunArgs,
} from "./testing.ts";

// the launcher download lands in the package store: isolate it
const STORE = TempDir.create("cappu-test-store-").path;
process.env.CAPPU_PACKAGE_STORE = STORE;

function tempProject(): TempDir {
  return TempDir.create("cappu-testing-");
}

test("test sources come from src/test/java; a missing dir means none", () => {
  using project = tempProject();
  const dir = project.path;
  const config = loadConfig(undefined, dir);
  expect(findTestSources(config)).toEqual([]);
  mkdirSync(join(dir, "src", "test", "java"), { recursive: true });
  writeFileSync(join(dir, "src", "test", "java", "ATest.java"), "class ATest {}");
  expect(findTestSources(config)).toEqual([join(dir, "src", "test", "java", "ATest.java")]);
});

test("test compile and run classpaths are ordered and jar-expanded", () => {
  using project = tempProject();
  const dir = project.path;
  mkdirSync(join(dir, ".cappu", "lib", "classes"), { recursive: true });
  mkdirSync(join(dir, ".cappu", "lib", "test-classes"), { recursive: true });
  writeFileSync(join(dir, ".cappu", "lib", "classes", "dep.jar"), "x");
  writeFileSync(join(dir, ".cappu", "lib", "test-classes", "junit.jar"), "x");
  const config = loadConfig(undefined, dir);

  const args = compileTestsArgs(config, ["/t/ATest.java"]);
  const cp = args[args.indexOf("-cp") + 1]!;
  expect(args[args.indexOf("-d") + 1]).toBe(join(dir, ".cappu", "test-build", "test-classes"));
  expect(cp.split(delimiter)).toEqual([
    mainClassesDir(config),
    join(dir, ".cappu", "lib", "classes"),
    join(dir, ".cappu", "lib", "classes", "dep.jar"),
    join(dir, ".cappu", "lib", "test-classes"),
    join(dir, ".cappu", "lib", "test-classes", "junit.jar"),
  ]);

  const run = testRunArgs(config, "/store/launcher.jar");
  expect(run.slice(0, 3)).toEqual(["-jar", "/store/launcher.jar", "execute"]);
  const runCp = run[run.indexOf("--class-path") + 1]!;
  expect(runCp.split(delimiter)[0]).toBe(join(dir, ".cappu", "test-build", "test-classes"));
  expect(run.at(-1)).toBe("--scan-class-path");
  // default outputFormat is "text": no report is written
  expect(run).not.toContain("--reports-dir");
});

test("testRunArgs writes junit reports to the resolved reportsDir only for junit", () => {
  using project = tempProject();
  const dir = project.path;
  const config = loadConfig(undefined, dir);

  config.testOptions.outputFormat = "junit";
  const run = testRunArgs(config, "/store/launcher.jar");
  expect(run[run.indexOf("--reports-dir") + 1]).toBe(join(dir, "dist", "test-results"));
  expect(run.at(-1)).toBe("--scan-class-path"); // still last

  config.testOptions.reportsDir = "./build/reports";
  const custom = testRunArgs(config, "/store/launcher.jar");
  expect(custom[custom.indexOf("--reports-dir") + 1]).toBe(join(dir, "build", "reports"));
});

test("compileTests wipes stale class files first (no phantom tests)", () => {
  using project = tempProject();
  const dir = project.path;
  const config = loadConfig(undefined, dir);
  const classes = testClassesDir(config);
  mkdirSync(join(classes, "old"), { recursive: true });
  writeFileSync(join(classes, "old", "GoneTest.class"), "stale");
  // a no-op javac (status 0) still triggers the pre-compile wipe
  const diagnostics = compileTests(config, ["/t/ATest.java"], () => ({ status: 0, stderr: "" }));
  expect(diagnostics).toEqual([]);
  expect(existsSync(join(classes, "old", "GoneTest.class"))).toBe(false);
});

test("test-compile failures map javac stderr to diagnostics", () => {
  using project = tempProject();
  const dir = project.path;
  const config = loadConfig(undefined, dir);
  const diagnostics = compileTests(config, ["/t/ATest.java"], () => ({
    status: 1,
    stderr: "/t/ATest.java:4: error: cannot find symbol\n1 error\n",
  }));
  expect(diagnostics).toEqual([
    { severity: "error", file: "/t/ATest.java", line: 4, message: "cannot find symbol" },
  ]);
  const missing = compileTests(config, ["/t/ATest.java"], () => ({
    status: null,
    stderr: "",
    error: new Error("ENOENT"),
  }));
  expect(missing[0]!.message).toContain("needs javac");
});

test("the console launcher is fetched once into the package store", async () => {
  using project = tempProject();
  const dir = project.path;
  const config = loadConfig(undefined, dir);
  let downloads = 0;
  const source = {
    name: "mem" as SourceName,
    search: () => Promise.resolve([]),
    listVersions: () => Promise.resolve([]),
    getMetadata: () => Promise.resolve(undefined),
    getArtifact: () => {
      downloads++;
      return Promise.resolve(new TextEncoder().encode("launcher-bytes"));
    },
  };

  const first = await consoleLauncherJar(config, [source]);
  expect(first).toContain(CONSOLE_LAUNCHER.artifactId);
  expect(first.startsWith(STORE)).toBe(true);
  const second = await consoleLauncherJar(config, [source]);
  expect(second).toBe(first);
  expect(downloads).toBe(1);

  await expect(consoleLauncherJar(config, [new InMemoryPackageSource("empty", [])])).resolves.toBe(
    first,
  ); // still cached even with a useless source
});
