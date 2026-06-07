// Emit-robustness test over the graph-engine submodule (a real-world Java
// project). For every source file the emitter must produce class bytes without
// throwing - anything it cannot compile must degrade to a verifiable placeholder,
// never crash. Skipped when the submodule is not checked out, so CI without it
// still passes:
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
const corpusDir = join(here, "..", "test-corpus", "graph-engine", "GraphEngine", "src");

function findJavaFiles(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) out.push(...findJavaFiles(full));
    else if (entry.endsWith(".java")) out.push(full);
  }
  return out;
}

test(
  "graph-engine corpus emits without crashing",
  { skip: existsSync(corpusDir) ? false : "submodule not checked out" },
  () => {
    const files = findJavaFiles(corpusDir);
    expect(files.length).toBeGreaterThan(40);

    // One program over all sources (+ the stub) so project-internal types resolve.
    const program = createProgram();
    loadJdkStub(program);
    const uris = files.map(f => {
      const uri = pathToUri(f);
      program.addProjectFile(uri, readFileSync(f, "utf8"));
      return uri;
    });
    const checker = createChecker(program);

    let emittedClasses = 0;
    for (const uri of uris) {
      const sourceFile = program.getSourceFile(uri)!;
      // Must not throw: unsupported constructs degrade to placeholder bodies.
      const classes = emitSourceFile(sourceFile, program, checker);
      emittedClasses += classes.length;
    }
    // The bulk of the ~60 top-level types should emit (some files hold several).
    expect(emittedClasses).toBeGreaterThan(50);
  },
);
