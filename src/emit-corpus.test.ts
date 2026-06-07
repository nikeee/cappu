// Emit-robustness tests over real-world Java projects checked out as git
// submodules under test-corpus/. Every submodule directory is auto-discovered,
// so adding a project is just `git submodule add <url> test-corpus/<name>` - no
// change here. For every source file the emitter must produce class bytes
// without throwing; anything it cannot compile degrades to a verifiable
// placeholder, never a crash. Skipped when no submodule is checked out, so CI
// without them still passes:
//
//   git submodule update --init

import { test } from "node:test";
import { expect } from "expect";
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { createChecker } from "./checker.ts";
import { emitSourceFile } from "./emitter.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { createProgram } from "./program.ts";
import { pathToUri } from "./workspace.ts";

const here = dirname(fileURLToPath(import.meta.url));
const corpusRoot = join(here, "..", "test-corpus");

function findJavaFiles(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    if (entry === ".git") continue;
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) out.push(...findJavaFiles(full));
    else if (entry.endsWith(".java")) out.push(full);
  }
  return out;
}

// Each checked-out submodule (a directory under test-corpus/ holding .java files).
const projects: { name: string; files: string[] }[] = existsSync(corpusRoot)
  ? readdirSync(corpusRoot)
      .map(name => join(corpusRoot, name))
      .filter(p => statSync(p).isDirectory())
      .map(p => ({ name: p.split("/").pop()!, files: findJavaFiles(p) }))
      .filter(p => p.files.length > 0)
  : [];

if (projects.length === 0) {
  test("corpus submodules emit without crashing", { skip: "no submodule checked out" }, () => {});
}

for (const project of projects) {
  test(`corpus: ${project.name} emits without crashing`, () => {
    // One program over all of the project's sources (+ the stub) so its own
    // cross-file types resolve; JDK types outside the stub degrade gracefully.
    const program = createProgram();
    loadJdkStub(program);
    const uris = project.files.map(f => {
      const uri = pathToUri(f);
      program.addProjectFile(uri, readFileSync(f, "utf8"));
      return uri;
    });
    const checker = createChecker(program);

    let emittedClasses = 0;
    const failures: string[] = [];
    for (const uri of uris) {
      try {
        emittedClasses += emitSourceFile(program.getSourceFile(uri)!, program, checker).length;
      } catch (e) {
        failures.push(`${uri.split("/").pop()}: ${(e as Error).message || (e as Error).stack}`);
      }
    }
    expect(failures).toEqual([]); // emission must never throw
    expect(emittedClasses).toBeGreaterThan(0);
  });
}
