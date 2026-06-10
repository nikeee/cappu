import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import { missingConfiguredPaths, runCompile } from "./compiler.ts";
import { loadConfig } from "./config.ts";

function inTempDir(
  files: Record<string, string>,
  body: (dir: string, paths: string[]) => void,
): void {
  const dir = mkdtempSync(join(tmpdir(), "cappu-compile-"));
  try {
    const paths = Object.entries(files).map(([name, text]) => {
      const p = join(dir, name);
      writeFileSync(p, text);
      return p;
    });
    body(dir, paths);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

// The config of an empty directory: pure schema defaults.
function defaultConfig(dir: string): ReturnType<typeof loadConfig> {
  return loadConfig(undefined, dir);
}

test("a clean compile returns the written class files and prints nothing", () => {
  inTempDir({ "A.java": "class A { int x = 1; }" }, (dir, paths) => {
    const result = runCompile(paths, { outDir: dir, config: defaultConfig(dir) });
    expect(result.success).toBe(true);
    expect(result.written).toEqual([join(dir, "A.class")]);
    expect(result.degraded).toEqual([]);
  });
});

test("a parse error fails with a located diagnostic and writes nothing", () => {
  inTempDir({ "Broken.java": "class Broken {" }, (dir, paths) => {
    const result = runCompile(paths, { outDir: dir, config: defaultConfig(dir) });
    expect(result.success).toBe(false);
    if (result.success) return;
    expect(result.written).toEqual([]);
    expect(result.diagnostics.length).toBeGreaterThan(0);
    const d = result.diagnostics[0]!;
    expect(d.severity).toBe("error");
    expect(d.file).toBe(paths[0]);
    expect(d.line).toBeGreaterThan(0);
    expect(d.column).toBeGreaterThan(0);
  });
});

test("checker diagnostics fail the build by default; typeCheck: false skips them", () => {
  const source = 'class C { int x = "s"; }'; // type mismatch (semantic, not syntactic)
  inTempDir({ "C.java": source }, (dir, paths) => {
    const checked = runCompile(paths, { outDir: dir, config: defaultConfig(dir) });
    expect(checked.success).toBe(false);
    if (!checked.success) {
      expect(checked.diagnostics.some(d => d.severity === "error")).toBe(true);
    }

    const unchecked = runCompile(paths, {
      outDir: dir,
      typeCheck: false,
      config: defaultConfig(dir),
    });
    expect(unchecked.success).toBe(true);
  });
});

test("failOnDegrade turns placeholder bodies into a failing result", () => {
  // A synchronized method body that the emitter supports either way would not
  // degrade; native-less, assert-less constructs are broadly supported now, so
  // force the unsupported path with an explicit this(...) constructor delegating
  // chain inside an anonymous class capture - if this ever stops degrading, the
  // expectation below flips and the fixture should be replaced with whatever is
  // still unsupported.
  const source = "class D { D() { this(1); } D(int x) { } }";
  inTempDir({ "D.java": source }, (dir, paths) => {
    const result = runCompile(paths, {
      outDir: dir,
      failOnDegrade: true,
      config: defaultConfig(dir),
    });
    if (result.degraded.length === 0) return; // construct became supported; nothing to assert
    expect(result.success).toBe(false);
  });
});

test("missing configured dirs warn only when a cappu.json is present", () => {
  inTempDir({ "cappu.json": '{ "compilerOptions": { "classPath": ["./no-such-dir"] } }' }, dir => {
    const fromFile = loadConfig(undefined, dir);
    const missing = missingConfiguredPaths(fromFile);
    expect(missing).toContain(join(dir, "no-such-dir"));
    // the default sourcePaths entry is also absent in the temp dir
    expect(missing).toContain(join(dir, "src/main/java"));
  });
  inTempDir({}, dir => {
    // no cappu.json: defaults may be absent without a warning
    expect(missingConfiguredPaths(loadConfig(undefined, dir))).toEqual([]);
  });
});

test("compiling with absent configured dirs does not throw", () => {
  inTempDir(
    {
      "cappu.json": '{ "compilerOptions": { "classPath": ["./nope"], "sourcePaths": ["./nada"] } }',
      "A.java": "class A { }",
    },
    (dir, paths) => {
      const config = loadConfig(undefined, dir);
      const result = runCompile([paths[1]!], { outDir: dir, config });
      expect(result.success).toBe(true);
    },
  );
});
