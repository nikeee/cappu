// End-to-end over the committed example projects: install from Maven Central
// (lockfiles pin the versions), compile (annotation processors included), run
// the fat jar and compare stdout exactly. Needs a JDK and network access;
// skipped without javac like the other JDK-gated suites.

import { execFileSync } from "node:child_process";
import {
  cpSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  realpathSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, dirname, join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

const here = import.meta.dirname;
const examplesDir = join(here, "..", "examples");
const tsx = join(here, "..", "node_modules", ".bin", "tsx");
const cli = join(here, "cli", "main.ts");

const HAS_JAVAC = (() => {
  try {
    execFileSync("javac", ["-version"], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
})();

// `java` from the same JDK as the `javac` that compiled (a PATH skew between
// the two would otherwise fail on class file versions).
function javaBin(): string {
  try {
    const javac = realpathSync(execFileSync("which", ["javac"], { encoding: "utf8" }).trim());
    return join(dirname(javac), "java");
  } catch {
    return "java";
  }
}

// CI's "experimental" matrix leg sets this to cover cappu's own compiler
// against real Maven Central dependencies.
const EXPERIMENTAL = process.env.CAPPU_EXAMPLES_EXPERIMENTAL === "1";

function runExample(name: string, command: string[] = ["compile"]): string {
  const root = mkdtempSync(join(tmpdir(), "cappu-example-"));
  const store = mkdtempSync(join(tmpdir(), "cappu-example-store-"));
  // the fat jar is named after the project directory: keep the example's name
  const work = join(root, name);
  try {
    // only the committed files; lib/dist/.cappu from local runs stay behind
    for (const entry of ["cappu.json", "cappu-lock.json", "src", ".gitignore"]) {
      cpSync(join(examplesDir, name, entry), join(work, entry), { recursive: true });
    }
    const env = { ...process.env, CAPPU_PACKAGE_STORE: store };
    execFileSync(tsx, [cli, "install"], { cwd: work, env, stdio: ["ignore", "ignore", "pipe"] });
    const flags = EXPERIMENTAL && command[0] === "compile" ? ["--experimental-compiler"] : [];
    const output = execFileSync(tsx, [cli, ...command, ...flags], {
      cwd: work,
      env,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    });
    if (command[0] !== "compile") return output;
    return execFileSync(javaBin(), ["-jar", join(work, "dist", `${name}.jar`)], {
      encoding: "utf8",
    });
  } finally {
    rmSync(root, { recursive: true, force: true });
    rmSync(store, { recursive: true, force: true });
  }
}

test("examples/gson-app builds and runs", { skip: !HAS_JAVAC }, () => {
  expect(runExample("gson-app")).toBe('{"x":1,"y":2}\n');
});

// In experimental mode MapStruct's generated code would run through the
// best-effort emitter; degraded bodies must not flake CI, so this example is
// javac-mode only.
test(
  "examples/mapstruct-app builds and runs (annotation processor)",
  { skip: !HAS_JAVAC || EXPERIMENTAL },
  () => {
    expect(runExample("mapstruct-app")).toBe("Wartburg 353 / 50 hp\n");
  },
);

// Like mapstruct, the Immutables processor generates code that the experimental
// emitter would run through best-effort, so this example is javac-mode only.
test(
  "examples/immutables-app builds and runs (annotation processor)",
  { skip: !HAS_JAVAC || EXPERIMENTAL },
  () => {
    expect(runExample("immutables-app")).toBe("Ant has 6 legs\n");
  },
);

test("examples/junit-app runs its tests with cappu test", { skip: !HAS_JAVAC }, () => {
  const output = runExample("junit-app", ["test"]);
  expect(output).toContain("2 tests successful");
  expect(output).toContain("0 tests failed");
});

// audit needs network (Maven resolve + OSV), not a JDK; gated on HAS_JAVAC
// only so it runs in the same networked legs as the other example e2e tests
// and skips on the hermetic no-JDK leg. The findings exit non-zero, which
// execFileSync surfaces as a throw whose stdout we read.
test("examples/audit-app reports its vulnerable dependency", { skip: !HAS_JAVAC }, () => {
  const root = mkdtempSync(join(tmpdir(), "cappu-example-"));
  const store = mkdtempSync(join(tmpdir(), "cappu-example-store-"));
  const work = join(root, "audit-app");
  try {
    cpSync(join(examplesDir, "audit-app", "cappu.json"), join(work, "cappu.json"));
    let stdout: string;
    let code = 0;
    try {
      stdout = execFileSync(tsx, [cli, "audit"], {
        cwd: work,
        env: { ...process.env, CAPPU_PACKAGE_STORE: store },
        encoding: "utf8",
        stdio: ["ignore", "pipe", "pipe"],
      });
    } catch (e) {
      stdout = (e as { stdout?: string }).stdout ?? "";
      code = (e as { status?: number }).status ?? 1;
    }
    expect(code).toBe(1); // findings -> non-zero exit
    // Log4Shell is a permanent advisory; OSV will always return it
    expect(stdout).toContain("CVE-2021-44228");
    expect(stdout).toContain("org.apache.logging.log4j:log4j-core:2.14.1");

    // --no-cache ignores the now-warm caches and still finds the same advisory
    let freshOut = "";
    let freshCode = 0;
    try {
      freshOut = execFileSync(tsx, [cli, "audit", "--no-cache"], {
        cwd: work,
        env: { ...process.env, CAPPU_PACKAGE_STORE: store },
        encoding: "utf8",
        stdio: ["ignore", "pipe", "pipe"],
      });
    } catch (e) {
      freshOut = (e as { stdout?: string }).stdout ?? "";
      freshCode = (e as { status?: number }).status ?? 1;
    }
    expect(freshCode).toBe(1);
    expect(freshOut).toContain("CVE-2021-44228");
  } finally {
    rmSync(root, { recursive: true, force: true });
    rmSync(store, { recursive: true, force: true });
  }
});

// licenses resolves the graph (no JDK) and prints each dependency's license;
// gson declares Apache-2.0, which maps cleanly to an SPDX id. Networked-leg
// gated on HAS_JAVAC like the other example e2e tests.
test("examples/gson-app reports dependency licenses (human + --json)", { skip: !HAS_JAVAC }, () => {
  const root = mkdtempSync(join(tmpdir(), "cappu-example-"));
  const store = mkdtempSync(join(tmpdir(), "cappu-example-store-"));
  const work = join(root, "gson-app");
  try {
    cpSync(join(examplesDir, "gson-app", "cappu.json"), join(work, "cappu.json"));
    const env = { ...process.env, CAPPU_PACKAGE_STORE: store };
    const human = execFileSync(tsx, [cli, "licenses"], {
      cwd: work,
      env,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    });
    expect(human).toContain("com.google.code.gson:gson:2.13.1");
    expect(human).toContain("Apache-2.0");

    const json = execFileSync(tsx, [cli, "licenses", "--json"], {
      cwd: work,
      env,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    });
    const rows = JSON.parse(json) as { coordinate: string; spdx: string[] }[];
    const gson = rows.find(r => r.coordinate === "com.google.code.gson:gson:2.13.1");
    expect(gson?.spdx).toContain("Apache-2.0");
  } finally {
    rmSync(root, { recursive: true, force: true });
    rmSync(store, { recursive: true, force: true });
  }
});

// A throwaway project pinned to an old gson; `cappu update` should move it to
// a newer stable version, rewrite cappu.json (comment kept) and write a lock.
// Network-only (no JDK); gated on HAS_JAVAC like the other example e2e tests.
test("cappu update bumps an outdated dependency end to end", { skip: !HAS_JAVAC }, () => {
  const root = mkdtempSync(join(tmpdir(), "cappu-example-"));
  const store = mkdtempSync(join(tmpdir(), "cappu-example-store-"));
  const work = join(root, "update-proj");
  try {
    mkdirSync(work, { recursive: true });
    writeFileSync(
      join(work, "cappu.json"),
      '{\n  "dependencies": {\n    "implementation": {\n' +
        "      // pinned old on purpose\n" +
        '      "com.google.code.gson:gson": "2.8.9"\n' +
        "    }\n  }\n}\n",
    );
    execFileSync(tsx, [cli, "update"], {
      cwd: work,
      env: { ...process.env, CAPPU_PACKAGE_STORE: store },
      stdio: ["ignore", "pipe", "pipe"],
    });
    const after = readFileSync(join(work, "cappu.json"), "utf8");
    expect(after).not.toContain("2.8.9"); // bumped away from the old pin
    expect(after).toContain("com.google.code.gson:gson");
    expect(after).toContain("// pinned old on purpose"); // comment preserved
    expect(existsSync(join(work, "cappu-lock.json"))).toBe(true); // lock refreshed
  } finally {
    rmSync(root, { recursive: true, force: true });
    rmSync(store, { recursive: true, force: true });
  }
});

// src/main/resources is bundled into the build output and read at runtime; the
// emitter may degrade the resource-reading main, so the compile/run check is
// javac-only (EXPERIMENTAL skips it). cappu test always uses javac.
test(
  "examples/resources-app bundles main resources into the jar",
  {
    skip: !HAS_JAVAC || EXPERIMENTAL,
  },
  () => {
    expect(runExample("resources-app")).toBe("hello from main resources\n");
  },
);

test(
  "examples/resources-app reads main and test resources under cappu test",
  {
    skip: !HAS_JAVAC,
  },
  () => {
    const output = runExample("resources-app", ["test"]);
    expect(output).toContain("2 tests successful");
    expect(output).toContain("0 tests failed");
  },
);

// A minimal Spring Boot app: cappu resolves the whole starter tree and compiles
// it, then it runs from a classpath of the individual jars (NOT a fat jar -
// Spring auto-config needs each jar's separate META-INF). Networked + JDK-gated;
// java expands the `<dir>/*` classpath entry itself, so no shell is involved.
test(
  "examples/spring-boot-app boots Spring Boot from a classpath build",
  {
    skip: !HAS_JAVAC,
  },
  () => {
    const root = mkdtempSync(join(tmpdir(), "cappu-example-"));
    const store = mkdtempSync(join(tmpdir(), "cappu-example-store-"));
    const work = join(root, "spring-boot-app");
    try {
      for (const entry of ["cappu.json", "cappu-lock.json", "src", ".gitignore"]) {
        cpSync(join(examplesDir, "spring-boot-app", entry), join(work, entry), { recursive: true });
      }
      const env = { ...process.env, CAPPU_PACKAGE_STORE: store };
      execFileSync(tsx, [cli, "install"], { cwd: work, env, stdio: ["ignore", "ignore", "pipe"] });
      execFileSync(tsx, [cli, "compile", "-o", "classes"], {
        cwd: work,
        env,
        stdio: ["ignore", "ignore", "pipe"],
      });
      const classpath = `${join(work, "dist")}${delimiter}${join(work, ".cappu", "lib", "classes")}/*`;
      const output = execFileSync(javaBin(), ["-cp", classpath, "com.example.App"], {
        encoding: "utf8",
      });
      expect(output).toContain("Spring Boot"); // the startup banner
      expect(output).toContain("Started App"); // the context booted
    } finally {
      rmSync(root, { recursive: true, force: true });
      rmSync(store, { recursive: true, force: true });
    }
  },
);

// With full coordinates, `cappu compile -o jar` produces the publishable pair:
// <artifactId>-<version>.jar plus its generated POM. Javac-gated like the rest.
test("cappu compile -o jar emits a publishable jar and POM", { skip: !HAS_JAVAC }, () => {
  const root = mkdtempSync(join(tmpdir(), "cappu-example-"));
  const store = mkdtempSync(join(tmpdir(), "cappu-example-store-"));
  const work = join(root, "pub-proj");
  try {
    mkdirSync(join(work, "src", "main", "java", "com", "example"), { recursive: true });
    writeFileSync(
      join(work, "cappu.json"),
      JSON.stringify({
        groupId: "com.example",
        artifactId: "demo-lib",
        version: "1.0.0",
        license: "MIT",
        dependencies: { implementation: { "com.google.code.gson:gson": "2.13.1" } },
      }),
    );
    writeFileSync(
      join(work, "src", "main", "java", "com", "example", "Hello.java"),
      "package com.example; public class Hello {}",
    );
    execFileSync(tsx, [cli, "compile", "-o", "jar"], {
      cwd: work,
      env: { ...process.env, CAPPU_PACKAGE_STORE: store },
      stdio: ["ignore", "pipe", "pipe"],
    });
    expect(existsSync(join(work, "dist", "demo-lib-1.0.0.jar"))).toBe(true);
    const pom = readFileSync(join(work, "dist", "demo-lib-1.0.0.pom"), "utf8");
    expect(pom).toContain("<artifactId>demo-lib</artifactId>");
    expect(pom).toContain("<version>1.0.0</version>");
    expect(pom).toMatch(/<artifactId>gson<\/artifactId>[\s\S]*?<scope>runtime<\/scope>/);
  } finally {
    rmSync(root, { recursive: true, force: true });
    rmSync(store, { recursive: true, force: true });
  }
});
