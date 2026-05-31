// AST baseline tests, in the style of the TypeScript compiler. Each .java file
// under __fixtures__/cases is parsed and bound, the resulting tree + diagnostics
// are serialized to a stable text form, and compared to the checked-in baseline
// under __fixtures__/baselines.
//
// To (re)generate baselines after an intentional change:
//   UPDATE_BASELINES=1 node --run test
// then review the diff before committing.

import { test } from "node:test";
import { expect } from "expect";
import { existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { bindSourceFile } from "./binder.ts";
import { forEachChild, parseSourceFile } from "./parser.ts";
import { syntaxKindToString } from "./utilities.ts";
import {
  type Diagnostic,
  type Identifier,
  type LiteralExpression,
  type Node,
  NodeFlags,
  SyntaxKind,
} from "./types.ts";

const here = dirname(fileURLToPath(import.meta.url));
const casesDir = join(here, "__fixtures__", "cases");
const baselinesDir = join(here, "__fixtures__", "baselines");
const shouldUpdate = process.env.UPDATE_BASELINES === "1";

function flagSuffix(flags: NodeFlags): string {
  const parts: string[] = [];
  if (flags & NodeFlags.ThisNodeHasError) parts.push("HasError");
  if (flags & NodeFlags.ThisNodeOrAnySubNodesHasError) parts.push("SubtreeHasError");
  return parts.length ? ` (${parts.join(", ")})` : "";
}

function nodeLabel(node: Node): string {
  let label = syntaxKindToString(node.kind);
  if (node.kind === SyntaxKind.Identifier) {
    label += ` "${(node as Identifier).text}"`;
  } else if (
    node.kind === SyntaxKind.NumericLiteral ||
    node.kind === SyntaxKind.StringLiteral ||
    node.kind === SyntaxKind.CharacterLiteral ||
    node.kind === SyntaxKind.TextBlockLiteral
  ) {
    label += ` ${JSON.stringify((node as LiteralExpression).value)}`;
  }
  return `${label} [${node.pos},${node.end}]${flagSuffix(node.flags)}`;
}

function printNode(node: Node, depth: number, out: string[]): void {
  out.push("  ".repeat(depth) + nodeLabel(node));
  forEachChild(node, child => {
    printNode(child, depth + 1, out);
    return undefined;
  });
}

function formatDiagnostics(title: string, diagnostics: readonly Diagnostic[]): string[] {
  if (diagnostics.length === 0) return [];
  return ["", `${title}:`, ...diagnostics.map(d => `  [${d.pos},${d.end}] ${d.messageText}`)];
}

function serialize(fileName: string, source: string): string {
  const sf = parseSourceFile(fileName, source);
  bindSourceFile(sf);
  const out: string[] = [];
  printNode(sf, 0, out);
  out.push(...formatDiagnostics("Parse diagnostics", sf.parseDiagnostics));
  out.push(...formatDiagnostics("Bind diagnostics", sf.bindDiagnostics ?? []));
  return out.join("\n") + "\n";
}

const cases = existsSync(casesDir)
  ? readdirSync(casesDir)
      .filter(f => f.endsWith(".java"))
      .sort()
  : [];

for (const file of cases) {
  test(`baseline: ${file}`, () => {
    const source = readFileSync(join(casesDir, file), "utf8");
    const actual = serialize(file, source);
    const baselinePath = join(baselinesDir, file.replace(/\.java$/, ".txt"));

    if (shouldUpdate || !existsSync(baselinePath)) {
      mkdirSync(baselinesDir, { recursive: true });
      writeFileSync(baselinePath, actual);
    }
    const expected = readFileSync(baselinePath, "utf8");
    expect(actual).toBe(expected);
  });
}
