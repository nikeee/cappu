import { test } from "node:test";

import { expect } from "expect";

import { annotationsEnabled, formatAnnotation } from "./annotations.ts";
import { renderDiagnostics } from "./renderDiagnostics.ts";

test("annotationsEnabled detects the GitHub-compatible runners only", () => {
  expect(annotationsEnabled({ GITHUB_ACTIONS: "true" })).toBe(true);
  expect(annotationsEnabled({ FORGEJO_ACTIONS: "true" })).toBe(true);
  expect(annotationsEnabled({ GITEA_ACTIONS: "true" })).toBe(true);
  // unset / empty / any non-"true" value does not count
  expect(annotationsEnabled({})).toBe(false);
  expect(annotationsEnabled({ GITHUB_ACTIONS: "" })).toBe(false);
  expect(annotationsEnabled({ GITHUB_ACTIONS: "false" })).toBe(false);
  expect(annotationsEnabled({ GITHUB_ACTIONS: "1" })).toBe(false);
  // bare CI=true is not a trigger: no generic-CI annotation format exists
  expect(annotationsEnabled({ CI: "true" })).toBe(false);
});

test("formatAnnotation builds workflow commands with and without a location", () => {
  expect(
    formatAnnotation("error", "cannot find symbol", { file: "Foo.java", line: 3, column: 5 }),
  ).toBe("::error file=Foo.java,line=3,col=5::cannot find symbol");
  expect(formatAnnotation("warning", "deprecated API")).toBe("::warning::deprecated API");
  // partial location: only the present properties are emitted
  expect(formatAnnotation("error", "boom", { file: "A.java", line: 2 })).toBe(
    "::error file=A.java,line=2::boom",
  );
});

test("formatAnnotation escapes data and property values", () => {
  // message (data): % \r \n
  expect(formatAnnotation("error", "100% done\nnext\r")).toBe(
    "::error::100%25 done%0Anext%0D",
  );
  // property values: additionally : and ,
  expect(formatAnnotation("error", "x", { file: "a:b,c.java", line: 1 })).toBe(
    "::error file=a%3Ab%2Cc.java,line=1::x",
  );
});

// Capture process.stderr.write for the duration of fn.
function captureStderr(fn: () => void): string {
  const chunks: string[] = [];
  const original = process.stderr.write.bind(process.stderr);
  process.stderr.write = ((chunk: string) => {
    chunks.push(String(chunk));
    return true;
  }) as typeof process.stderr.write;
  try {
    fn();
  } finally {
    process.stderr.write = original;
  }
  return chunks.join("");
}

test("renderDiagnostics emits annotations only under a supporting runner", () => {
  const diagnostics = [
    { severity: "error", file: "Foo.java", line: 3, column: 5, message: "cannot find symbol" },
  ] as const;
  const saved = process.env.GITHUB_ACTIONS;
  const savedForgejo = process.env.FORGEJO_ACTIONS;
  const savedGitea = process.env.GITEA_ACTIONS;
  try {
    delete process.env.GITHUB_ACTIONS;
    delete process.env.FORGEJO_ACTIONS;
    delete process.env.GITEA_ACTIONS;
    const off = captureStderr(() => renderDiagnostics(diagnostics));
    expect(off).toBe("Foo.java:3:5: error: cannot find symbol\n");
    expect(off).not.toContain("::error");

    process.env.GITHUB_ACTIONS = "true";
    const on = captureStderr(() => renderDiagnostics(diagnostics));
    expect(on).toContain("Foo.java:3:5: error: cannot find symbol\n");
    expect(on).toContain("::error file=Foo.java,line=3,col=5::cannot find symbol\n");
  } finally {
    if (saved === undefined) delete process.env.GITHUB_ACTIONS;
    else process.env.GITHUB_ACTIONS = saved;
    if (savedForgejo !== undefined) process.env.FORGEJO_ACTIONS = savedForgejo;
    if (savedGitea !== undefined) process.env.GITEA_ACTIONS = savedGitea;
  }
});
