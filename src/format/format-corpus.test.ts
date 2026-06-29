// Formatter compatibility ratchet over google-java-format's own source tree,
// checked out as a git submodule at test-fixtures/format/corpus/gjf. gjf
// dogfoods its own formatter, so the committed *.java files are (very nearly)
// gjf's canonical output - a perfect formatter is a fixpoint: it maps each file
// to itself. We therefore assert formatSource(file) === file and track how many
// of the core sources match.
//
// This is a regression ratchet, not a 100% gate: RATCHET is the current floor
// and must only ever go UP. Raise it whenever a formatter fix lands more
// matches (the failing files report below shows what is left). The test needs
// no JDK and is skipped when the submodule is not checked out, so CI without it
// still passes:
//
//   git submodule update --init test-fixtures/format/corpus/gjf
//
// To see the live match rate / remaining diffs:  node --run format:corpus

import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import { formatSource, UnsupportedSyntaxError } from "./index.ts";

const here = import.meta.dirname;
const corpusRoot = join(here, "..", "..", "test-fixtures", "format", "corpus", "gjf", "core");

// The current number of core sources we format byte-identically. Ratchet UP only.
const RATCHET = 44;

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

const files = existsSync(corpusRoot) ? findJavaFiles(corpusRoot) : [];

if (files.length === 0) {
  test("gjf corpus fixpoint", { skip: "gjf submodule not checked out" }, () => {});
} else {
  test(`gjf corpus: >= ${RATCHET}/${files.length} sources are formatting fixpoints`, () => {
    let matched = 0;
    const threw: string[] = [];
    for (const f of files) {
      const src = readFileSync(f, "utf8");
      try {
        if (formatSource(src, { style: "google" }, f) === src) matched++;
      } catch (e) {
        if (e instanceof UnsupportedSyntaxError) continue; // counts as a non-match
        threw.push(`${f.split("/").pop()}: ${(e as Error).message}`);
      }
    }
    expect(threw).toEqual([]); // formatting a parseable file must never crash
    if (matched < files.length) {
      process.stdout.write(`\n  gjf corpus fixpoint: ${matched}/${files.length} matched\n`);
    }
    expect(matched).toBeGreaterThanOrEqual(RATCHET);
  });
}
