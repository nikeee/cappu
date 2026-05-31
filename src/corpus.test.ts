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

import { test } from "node:test";
import { expect } from "expect";
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { bindSourceFile } from "./binder.ts";
import { parseSourceFile } from "./parser.ts";
import { SyntaxKind } from "./types.ts";

const here = dirname(fileURLToPath(import.meta.url));
const corpusDir = process.env.JAVA_CORPUS_DIR ?? join(here, "..", "corpus");

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
