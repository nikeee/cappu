// Robustness tests over a local Java corpus (e.g. OpenJDK sources and the javac
// langtools test suite). The corpus is NOT committed (it is large and GPL); drop
// .java files under ./corpus, or point JAVA_CORPUS_DIR at a checkout:
//
//   JAVA_CORPUS_DIR=/path/to/jdk/src node --run test
//
// For every file the parser must terminate, return a SourceFile and not throw
// while binding. Diagnostics are reported (not asserted) since a heterogeneous
// corpus includes intentionally malformed compiler-test inputs. When the corpus
// is absent the suite is skipped, so CI without it still passes.

import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

import { expect } from "expect";

import { bindSourceFile } from "./binder.ts";
import { createChecker } from "./checker.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { forEachChild, parseSourceFile } from "./parser.ts";
import { createProgram } from "./program.ts";
import { type Identifier, type Node, SyntaxKind } from "./types.ts";
import { pathToUri } from "./workspace.ts";

const here = dirname(fileURLToPath(import.meta.url));
const corpusDir =
  process.env.JAVA_CORPUS_DIR ?? join(here, "..", "test-fixtures", "parser", "corpus");

function findJavaFiles(dir: string): string[] {
  const result: string[] = [];
  if (!existsSync(dir)) return result;
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      result.push(...findJavaFiles(full));
    } else if (entry.endsWith(".java")) {
      result.push(full);
    }
  }
  return result;
}

const files = findJavaFiles(corpusDir);

test(
  "Java corpus parses without crashing",
  { skip: files.length === 0 ? "no corpus present" : false },
  () => {
    let totalDiagnostics = 0;
    let cleanFiles = 0;
    for (const file of files) {
      const source = readFileSync(file, "utf8");
      const sf = parseSourceFile(file, source);
      expect(sf.kind).toBe(SyntaxKind.SourceFile);
      bindSourceFile(sf); // must not throw
      totalDiagnostics += sf.parseDiagnostics.length;
      if (sf.parseDiagnostics.length === 0) cleanFiles++;
    }
    console.log(
      `corpus: ${files.length} files, ${cleanFiles} clean, ${totalDiagnostics} parse diagnostics total`,
    );
  },
);

// Conformance/robustness for the semantic layer: load the whole corpus into one
// Program (+ JDK stub), then resolve every identifier and type every expression.
// Real code must not crash the resolver/checker, and a healthy fraction of names
// must resolve (the rest are JDK types outside the minimal stub).
test(
  "Java corpus resolves and types without crashing",
  { skip: files.length === 0 ? "no corpus present" : false },
  () => {
    const program = createProgram();
    loadJdkStub(program);
    for (const file of files)
      program.setOpenDocument(pathToUri(file), readFileSync(file, "utf8"), 1);
    const checker = createChecker(program);

    let identifiers = 0;
    let resolved = 0;
    const walk = (node: Node): void => {
      if (node.kind === SyntaxKind.Identifier) {
        identifiers++;
        if (checker.resolveName(node as Identifier)) resolved++;
      }
      checker.getTypeOfExpression(node); // must not throw
      forEachChild(node, child => {
        walk(child);
        return undefined;
      });
    };
    for (const file of files) {
      const sf = program.getSourceFile(pathToUri(file));
      if (sf) walk(sf);
    }

    const rate = identifiers === 0 ? 1 : resolved / identifiers;
    console.log(
      `corpus semantics: ${identifiers} identifiers, ${(rate * 100).toFixed(1)}% resolved`,
    );
    expect(identifiers).toBeGreaterThan(0);
    expect(rate).toBeGreaterThan(0.5); // regression floor; real code resolves well above this
  },
);
