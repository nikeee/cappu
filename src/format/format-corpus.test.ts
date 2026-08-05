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
// 62 is the CEILING, not a gap: the 9 files that are not fixpoints were
// committed by an older google-java-format, so gjf 1.25.2 does not reproduce
// them either (`gjf <file> != <file>`), and our output equals gjf 1.25.2's on
// every one of them. Measure against real gjf before treating any of the nine
// as a bug - the committed-basis count cannot go above 62 without deviating
// from the tool we target.
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
const RATCHET = 62;

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

  // Formatting is a normalization, so it must reach a fixpoint in ONE pass:
  // `format(format(x)) === format(x)` for EVERY file, matched or not. The golden
  // fixtures assert this per case; this widens it to the whole corpus, where a
  // wrapped trailing comment used to re-parse as an own-line comment and move.
  test("formatting the gjf corpus is idempotent", () => {
    const unstable: string[] = [];
    for (const f of files) {
      let once: string;
      try {
        once = formatSource(readFileSync(f, "utf8"), { style: "google" }, f);
      } catch {
        continue;
      }
      if (formatSource(once, { style: "google" }, f) !== once) unstable.push(f.split("/").pop()!);
    }
    expect(unstable).toEqual([]);
  });
}
