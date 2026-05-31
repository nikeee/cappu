// Refactor / quick-fix code actions. Each action is computed as plain text
// changes (offset ranges + replacement text) over a single file, so the logic is
// pure and testable; the server maps them to LSP WorkspaceEdits. Actions are
// offered for a [start, end) selection range in one source file.

import type { Checker } from "./checker.ts";
import { getIdentifierAtPosition } from "./nodeAtPosition.ts";
import type { Program } from "./program.ts";
import { entityNameToString } from "./utilities.ts";
import { type Identifier, type ImportDeclaration, type SourceFile } from "./types.ts";

export interface TextChange {
  readonly start: number;
  readonly end: number;
  readonly newText: string;
}

export interface CodeActionResult {
  readonly title: string;
  /** LSP CodeActionKind, e.g. "quickfix" or "refactor.extract". */
  readonly kind: string;
  readonly changes: TextChange[];
}

function packageOf(fqn: string): string {
  const dot = fqn.lastIndexOf(".");
  return dot < 0 ? "" : fqn.slice(0, dot);
}

function filePackage(sourceFile: SourceFile): string {
  return sourceFile.packageDeclaration
    ? entityNameToString(sourceFile.packageDeclaration.name)
    : "";
}

function singleTypeImportFqns(sourceFile: SourceFile): Set<string> {
  const out = new Set<string>();
  for (const imp of sourceFile.imports) {
    if (!imp.isStatic && !imp.isOnDemand) out.add(entityNameToString(imp.name));
  }
  return out;
}

// Where a new `import` line should go, as a zero-width insertion: after the last
// existing import, else after the package declaration, else at the file start.
function importInsertion(sourceFile: SourceFile, statement: string): TextChange {
  if (sourceFile.imports.length > 0) {
    const last = sourceFile.imports[sourceFile.imports.length - 1]!;
    return { start: last.end, end: last.end, newText: `\n${statement}` };
  }
  if (sourceFile.packageDeclaration) {
    const end = sourceFile.packageDeclaration.end;
    return { start: end, end, newText: `\n\n${statement}` };
  }
  return { start: 0, end: 0, newText: `${statement}\n\n` };
}

// --- add missing import ------------------------------------------------------------

function addMissingImport(
  program: Program,
  checker: Checker,
  sourceFile: SourceFile,
  start: number,
): CodeActionResult[] {
  const identifier = getIdentifierAtPosition(sourceFile, start) as Identifier | undefined;
  if (!identifier || checker.resolveName(identifier)) return []; // already resolves
  const name = identifier.text;
  if (!name) return [];

  const here = filePackage(sourceFile);
  const alreadyImported = singleTypeImportFqns(sourceFile);
  const candidates = program
    .getGlobalIndex()
    .findFqnsBySimpleName(name)
    .filter(fqn => {
      const pkg = packageOf(fqn);
      // skip the default package (cannot be imported), the current package and
      // java.lang (both already in scope), and anything already imported.
      return pkg !== "" && pkg !== here && pkg !== "java.lang" && !alreadyImported.has(fqn);
    })
    .sort();

  return candidates.map(fqn => ({
    title: `Import '${fqn}'`,
    kind: "quickfix",
    changes: [importInsertion(sourceFile, `import ${fqn};`)],
  }));
}

export function getCodeActions(
  program: Program,
  checker: Checker,
  sourceFile: SourceFile,
  start: number,
  _end: number,
): CodeActionResult[] {
  return [...addMissingImport(program, checker, sourceFile, start)];
}
