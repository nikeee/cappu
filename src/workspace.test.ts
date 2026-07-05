import { mkdirSync, rmSync, utimesSync, writeFileSync } from "node:fs";
import TempDir from "./TempDir.ts";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import { loadConfig } from "./config.ts";
import { classpathFingerprint, configWatchGlobs, findJavaFiles } from "./workspace.ts";

test("a missing directory yields no files instead of throwing", () => {
  expect(findJavaFiles("/definitely/not/here")).toEqual([]);
});

test("build directories (node_modules, ...) are skipped", () => {
  using dir = TempDir.create("cappu-ws-");
  mkdirSync(join(dir.path, "node_modules", "x"), { recursive: true });
  mkdirSync(join(dir.path, "src"), { recursive: true });
  writeFileSync(join(dir.path, "A.java"), "class A {}");
  writeFileSync(join(dir.path, "src", "B.java"), "class B {}");
  writeFileSync(join(dir.path, "node_modules", "x", "C.java"), "class C {}");
  expect(findJavaFiles(dir.path).sort()).toEqual([
    join(dir.path, "A.java"),
    join(dir.path, "src", "B.java"),
  ]);
});

test("classpathFingerprint tracks jars and class files, reacts to add/remove/modify", () => {
  using dir = TempDir.create("cappu-ws-");
  writeFileSync(
    join(dir.path, "cappu.json"),
    JSON.stringify({ compilerOptions: { classPath: ["lib", "direct.jar"] } }),
  );
  const config = loadConfig(undefined, dir.path);
  mkdirSync(join(dir.path, "lib", "nested"), { recursive: true });

  // a missing direct .jar entry and an empty dir contribute nothing
  expect(classpathFingerprint(config).size).toBe(0);

  writeFileSync(join(dir.path, "direct.jar"), "jar");
  writeFileSync(join(dir.path, "lib", "a.jar"), "a");
  writeFileSync(join(dir.path, "lib", "nested", "B.class"), "b");
  writeFileSync(join(dir.path, "lib", "README.md"), "ignored");
  const fp = classpathFingerprint(config);
  expect([...fp.keys()].sort()).toEqual([
    join(dir.path, "direct.jar"),
    join(dir.path, "lib", "a.jar"),
    join(dir.path, "lib", "nested", "B.class"),
  ]);

  // touching a jar changes its entry
  const later = new Date(Date.now() + 60_000);
  utimesSync(join(dir.path, "lib", "a.jar"), later, later);
  expect(classpathFingerprint(config).get(join(dir.path, "lib", "a.jar"))).not.toBe(
    fp.get(join(dir.path, "lib", "a.jar")),
  );

  // removing a jar shrinks the map
  rmSync(join(dir.path, "lib", "a.jar"));
  expect(classpathFingerprint(config).size).toBe(2);
});

test("configWatchGlobs covers sources, the config file, and classpath entries", () => {
  expect(configWatchGlobs(undefined)).toEqual(["**/*.java"]);

  using dir = TempDir.create("cappu-ws-");
  writeFileSync(
    join(dir.path, "cappu.json"),
    JSON.stringify({ compilerOptions: { classPath: ["lib", "direct.jar"] } }),
  );
  const config = loadConfig(undefined, dir.path);
  const posix = dir.path.replaceAll("\\", "/");
  expect(configWatchGlobs(config)).toEqual([
    "**/*.java",
    "**/cappu.json",
    posix + "/lib/**/*.{jar,class}",
    posix + "/direct.jar",
  ]);
});
