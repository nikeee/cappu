import { execFileSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

const here = import.meta.dirname;
const cli = join(here, "main.ts");
const tsx = join(here, "..", "..", "node_modules", ".bin", "tsx");

// runInit ends the process, so it is exercised through a real cli invocation.
// -y skips the interactive prompts (no TTY in the test) and takes the defaults.
function runInit(dir: string, ...args: string[]): string {
  return execFileSync(tsx, [cli, "init", "-y", ...args], { cwd: dir, encoding: "utf8" });
}

test("cappu init creates the default project directories (nikeee/cappu#3)", () => {
  const dir = mkdtempSync(join(tmpdir(), "cappu-init-"));
  try {
    runInit(dir);
    expect(existsSync(join(dir, "cappu.json"))).toBe(true);
    expect(existsSync(join(dir, ".cappu", "lib", "classes"))).toBe(true);
    expect(existsSync(join(dir, ".cappu", "lib", "test-classes"))).toBe(true);
    expect(existsSync(join(dir, "src", "main", "java"))).toBe(true);
    expect(existsSync(join(dir, "src", "main", "resources"))).toBe(true);
    expect(existsSync(join(dir, "src", "test", "java"))).toBe(true);
    expect(existsSync(join(dir, "src", "test", "resources"))).toBe(true);
    expect(existsSync(join(dir, "cappu.schema.json"))).toBe(false); // needs --with-schema
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("cappu init writes a .gitignore but never overwrites an existing one (#12)", () => {
  const dir = mkdtempSync(join(tmpdir(), "cappu-init-"));
  try {
    runInit(dir);
    const gitignore = readFileSync(join(dir, ".gitignore"), "utf8");
    expect(gitignore).toContain("/.cappu/");
    expect(gitignore).toContain("/dist/");
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }

  const existing = mkdtempSync(join(tmpdir(), "cappu-init-"));
  try {
    writeFileSync(join(existing, ".gitignore"), "# mine\n");
    runInit(existing);
    expect(readFileSync(join(existing, ".gitignore"), "utf8")).toBe("# mine\n");
  } finally {
    rmSync(existing, { recursive: true, force: true });
  }
});

test("cappu init -y writes coordinates and defaults the output to a fat jar", () => {
  const dir = mkdtempSync(join(tmpdir(), "cappu-init-"));
  try {
    runInit(dir);
    const config = JSON.parse(readFileSync(join(dir, "cappu.json"), "utf8")) as {
      groupId: string;
      artifactId: string;
      version: string;
      compilerOptions: { output: string };
      dependencies: Record<string, unknown>;
    };
    expect(config.groupId).toBe("com.example");
    expect(config.version).toBe("1.0.0");
    expect(config.artifactId.length).toBeGreaterThan(0);
    expect(config.compilerOptions.output).toBe("fat-jar");
    expect(Object.keys(config.dependencies).sort()).toEqual([
      "annotationProcessor",
      "api",
      "implementation",
      "testImplementation",
    ]);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("cappu init --with-schema also writes the schema", () => {
  const dir = mkdtempSync(join(tmpdir(), "cappu-init-"));
  try {
    runInit(dir, "--with-schema");
    expect(existsSync(join(dir, "cappu.schema.json"))).toBe(true);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
