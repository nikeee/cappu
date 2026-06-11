// Fourslash-style language-service baseline tests (analogous to the TypeScript
// compiler's fourslash tests). A fixture is a .java file with markers /*name*/;
// the marker is stripped and its offset becomes a query position. We run
// completion at each marker and serialize the result to a checked-in baseline,
// comparing via text diff. Regenerate after intentional changes:
//   UPDATE_BASELINES=1 node --run test

import { existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

import { expect } from "expect";

import { createChecker } from "../compiler/checker.ts";
import { type CompletionItem, CompletionItemKind, getCompletions } from "./completions.ts";
import { loadJdkStub } from "../compiler/jdkStub.ts";
import { createProgram } from "../compiler/program.ts";
import { type Uri } from "../workspace.ts";

const here = dirname(fileURLToPath(import.meta.url));
const fixturesDir = join(here, "..", "..", "test-fixtures", "language-service", "fourslash");
const baselinesDir = join(
  here,
  "..",
  "..",
  "test-fixtures",
  "language-service",
  "fourslash-baselines",
);
const shouldUpdate = process.env.UPDATE_BASELINES === "1";

interface Markers {
  clean: string;
  markers: Array<{ name: string; offset: number }>;
}

function extractMarkers(text: string): Markers {
  const markers: Array<{ name: string; offset: number }> = [];
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

const KIND_NAMES: Record<number, string> = {
  [CompletionItemKind.Method]: "method",
  [CompletionItemKind.Field]: "field",
  [CompletionItemKind.Variable]: "variable",
  [CompletionItemKind.Class]: "class",
  [CompletionItemKind.Interface]: "interface",
  [CompletionItemKind.Enum]: "enum",
  [CompletionItemKind.EnumMember]: "enum-constant",
  [CompletionItemKind.TypeParameter]: "type-parameter",
};

function serializeCompletions(items: CompletionItem[]): string {
  if (items.length === 0) return "  (none)";
  return items
    .map(i => `  ${KIND_NAMES[i.kind] ?? i.kind} ${i.label}`)
    .sort()
    .join("\n");
}

const fixtures = existsSync(fixturesDir)
  ? readdirSync(fixturesDir)
      .filter(f => f.endsWith(".java"))
      .sort()
  : [];

for (const fixture of fixtures) {
  test(`fourslash completions: ${fixture}`, () => {
    const raw = readFileSync(join(fixturesDir, fixture), "utf8");
    const { clean, markers } = extractMarkers(raw);

    const program = createProgram();
    loadJdkStub(program);
    const uri = `file:///${fixture}` as Uri;
    program.setOpenDocument(uri, clean, 1);
    const checker = createChecker(program);
    const sourceFile = program.getSourceFile(uri)!;

    const sections = markers.map(marker => {
      const items = getCompletions(program, checker, sourceFile, marker.offset);
      return `=== ${marker.name} ===\n${serializeCompletions(items)}`;
    });
    const actual = sections.join("\n\n") + "\n";

    const baselinePath = join(baselinesDir, fixture.replace(/\.java$/, ".txt"));
    if (shouldUpdate || !existsSync(baselinePath)) {
      mkdirSync(baselinesDir, { recursive: true });
      writeFileSync(baselinePath, actual);
    }
    expect(actual).toBe(readFileSync(baselinePath, "utf8"));
  });
}
