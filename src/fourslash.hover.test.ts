// Fourslash-style hover (quick-info) baselines. Each fixture in
// test-fixtures/language-service/fourslash-hover/ is a .java file with markers /*name*/ placed
// immediately before an identifier. We resolve the symbol at each marker and
// serialize its hover label, comparing against a checked-in baseline.
// Regenerate after intentional changes:
//   UPDATE_BASELINES=1 node --run test

import { existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

import { expect } from "expect";

import { createChecker } from "./checker.ts";
import { getHoverText } from "./hover.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { getIdentifierAtPosition } from "./nodeAtPosition.ts";
import { createProgram } from "./program.ts";
import type { Identifier } from "./types.ts";
import { type Uri } from "./workspace.ts";

const here = dirname(fileURLToPath(import.meta.url));
const fixturesDir = join(here, "..", "test-fixtures", "language-service", "fourslash-hover");
const baselinesDir = join(
  here,
  "..",
  "test-fixtures",
  "language-service",
  "fourslash-hover-baselines",
);
const shouldUpdate = process.env.UPDATE_BASELINES === "1";

function extractMarkers(text: string): {
  clean: string;
  markers: { name: string; offset: number }[];
} {
  const markers: { name: string; offset: number }[] = [];
  let clean = "";
  let last = 0;
  const re = /\/\*([A-Za-z0-9_]+)\*\//g;
  let match: RegExpExecArray | null;
  while ((match = re.exec(text)) !== null) {
    clean += text.slice(last, match.index);
    markers.push({ name: match[1]!, offset: clean.length });
    last = match.index + match[0].length;
  }
  clean += text.slice(last);
  return { clean, markers };
}

const fixtures = existsSync(fixturesDir)
  ? readdirSync(fixturesDir)
      .filter(f => f.endsWith(".java"))
      .sort()
  : [];

for (const fixture of fixtures) {
  test(`fourslash hover: ${fixture}`, () => {
    const raw = readFileSync(join(fixturesDir, fixture), "utf8");
    const { clean, markers } = extractMarkers(raw);

    const program = createProgram();
    loadJdkStub(program);
    const uri = `file:///${fixture}` as Uri;
    program.setOpenDocument(uri, clean, 1);
    const checker = createChecker(program);
    const sourceFile = program.getSourceFile(uri)!;

    const sections = markers.map(marker => {
      const id = getIdentifierAtPosition(sourceFile, marker.offset) as Identifier | undefined;
      const symbol = id ? checker.resolveName(id) : undefined;
      const text = symbol ? getHoverText(checker, symbol, id) : "(unresolved)";
      return `=== ${marker.name} ===\n  ${text}`;
    });
    const actual = sections.join("\n") + "\n";

    const baselinePath = join(baselinesDir, fixture.replace(/\.java$/, ".txt"));
    if (shouldUpdate || !existsSync(baselinePath)) {
      mkdirSync(baselinesDir, { recursive: true });
      writeFileSync(baselinePath, actual);
    }
    expect(actual).toBe(readFileSync(baselinePath, "utf8"));
  });
}
