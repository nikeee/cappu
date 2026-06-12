// End-to-end over the committed example projects: install from Maven Central
// (lockfiles pin the versions), compile (annotation processors included), run
// the fat jar and compare stdout exactly. Needs a JDK and network access;
// skipped without javac like the other JDK-gated suites.

import { execFileSync } from "node:child_process";
import { cpSync, mkdtempSync, realpathSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
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

test("examples/junit-app runs its tests with cappu test", { skip: !HAS_JAVAC }, () => {
  const output = runExample("junit-app", ["test"]);
  expect(output).toContain("2 tests successful");
  expect(output).toContain("0 tests failed");
});
