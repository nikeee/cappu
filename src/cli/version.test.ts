import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

const here = import.meta.dirname;
const tsx = join(here, "..", "..", "node_modules", ".bin", "tsx");
const cli = join(here, "main.ts");

const HAS_GIT = (() => {
  try {
    execFileSync("git", ["--version"], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
})();

test(
  "cappu version bumps cappu.json (comments kept) and tags at a git root",
  { skip: !HAS_GIT },
  () => {
    const dir = mkdtempSync(join(tmpdir(), "cappu-version-"));
    const git = (...args: string[]): void => {
      execFileSync("git", args, { cwd: dir, stdio: "ignore" });
    };
    try {
      git("init");
      git("config", "user.email", "test@example.com");
      git("config", "user.name", "Test");
      writeFileSync(
        join(dir, "cappu.json"),
        '{\n  // my project\n  "groupId": "com.example",\n  "artifactId": "lib",\n  "version": "1.2.3"\n}\n',
      );
      git("add", ".");
      git("commit", "-m", "init");

      execFileSync(tsx, [cli, "version", "minor"], { cwd: dir, stdio: ["ignore", "pipe", "pipe"] });

      const after = readFileSync(join(dir, "cappu.json"), "utf8");
      expect(after).toContain('"version": "1.3.0"');
      expect(after).toContain("// my project"); // comment preserved
      const tags = execFileSync("git", ["tag"], { cwd: dir, encoding: "utf8" });
      expect(tags).toContain("v1.3.0");
      // the bump was committed as its own commit
      const subject = execFileSync("git", ["log", "-1", "--pretty=%s"], {
        cwd: dir,
        encoding: "utf8",
      });
      expect(subject.trim()).toBe("v1.3.0");
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  },
);

test("cappu version still bumps cappu.json outside a git repo", { skip: !HAS_GIT }, () => {
  const dir = mkdtempSync(join(tmpdir(), "cappu-version-"));
  try {
    writeFileSync(join(dir, "cappu.json"), '{\n  "version": "0.9.0"\n}\n');
    execFileSync(tsx, [cli, "version", "patch"], { cwd: dir, stdio: ["ignore", "pipe", "pipe"] });
    expect(readFileSync(join(dir, "cappu.json"), "utf8")).toContain('"version": "0.9.1"');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
