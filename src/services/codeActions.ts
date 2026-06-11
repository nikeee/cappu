// Refactor / quick-fix code actions. Each action is computed as plain text
// changes (offset ranges + replacement text) over a single file, so the logic is
// pure and testable; the server maps them to LSP WorkspaceEdits. Actions are
// offered for a [start, end) selection range in one source file.

import type { Checker } from "../compiler/checker.ts";
import { getIdentifierAtPosition } from "./nodeAtPosition.ts";
import { forEachChild } from "../compiler/parser.ts";
import type { Program } from "../compiler/program.ts";
import { findReferences, getSourceFileOfNode } from "../compiler/resolver.ts";
import {
  type AssignmentExpression,
  type CallExpression,
  type Identifier,
  type ImportDeclaration,
  type LocalVariableDeclarationStatement,
  type MethodDeclaration,
  type Node,
  type PropertyAccessExpression,
  type SourceFile,
  SymbolFlags,
  SyntaxKind,
  type VariableDeclarator,
} from "../compiler/types.ts";
import { entityNameToString, skipTrivia } from "../compiler/utilities.ts";

function forEachDescendant(node: Node, cb: (n: Node) => void): void {
  cb(node);
  forEachChild(node, child => {
    forEachDescendant(child, cb);
    return undefined;
  });
}

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

// --- organize imports --------------------------------------------------------------

function importText(imp: ImportDeclaration): string {
  const star = imp.isOnDemand ? ".*" : "";
  return `import ${imp.isStatic ? "static " : ""}${entityNameToString(imp.name)}${star};`;
}

function organizeImports(sourceFile: SourceFile): CodeActionResult[] {
  const imports = sourceFile.imports;
  if (imports.length === 0) return [];

  // Simple names used anywhere in the body (a conservative "is this import used?"
  // check: keep the import if its type name appears at all, so a used import is
  // never removed).
  const used = new Set<string>();
  for (const statement of sourceFile.statements) {
    forEachDescendant(statement, n => {
      if (n.kind === SyntaxKind.Identifier) used.add((n as Identifier).text);
    });
  }

  const kept = imports.filter(imp => {
    if (imp.isStatic || imp.isOnDemand) return true; // cannot tell precisely: keep
    const fqn = entityNameToString(imp.name);
    return used.has(fqn.slice(fqn.lastIndexOf(".") + 1));
  });

  // Non-static group first, then static; alphabetical within each.
  const sorted = [...kept].sort((a, b) => {
    if (a.isStatic !== b.isStatic) return a.isStatic ? 1 : -1;
    const ta = importText(a);
    const tb = importText(b);
    return ta < tb ? -1 : ta > tb ? 1 : 0;
  });

  const start = skipTrivia(sourceFile.text, imports[0]!.pos);
  const end = imports.at(-1)!.end;
  const newText = sorted.map(importText).join("\n");
  if (newText === sourceFile.text.slice(start, end)) return []; // already organized
  return [
    {
      title: "Organize imports",
      kind: "source.organizeImports",
      changes: [{ start, end, newText }],
    },
  ];
}

// --- extract local variable --------------------------------------------------------

const EXPRESSION_KINDS = new Set<SyntaxKind>([
  SyntaxKind.BinaryExpression,
  SyntaxKind.CallExpression,
  SyntaxKind.PropertyAccessExpression,
  SyntaxKind.ElementAccessExpression,
  SyntaxKind.ParenthesizedExpression,
  SyntaxKind.ConditionalExpression,
  SyntaxKind.CastExpression,
  SyntaxKind.ObjectCreationExpression,
  SyntaxKind.ArrayCreationExpression,
  SyntaxKind.PrefixUnaryExpression,
  SyntaxKind.PostfixUnaryExpression,
  SyntaxKind.InstanceofExpression,
  SyntaxKind.SwitchExpression,
  SyntaxKind.MethodReferenceExpression,
  SyntaxKind.NumericLiteral,
  SyntaxKind.StringLiteral,
  SyntaxKind.TextBlockLiteral,
  SyntaxKind.CharacterLiteral,
]);

// The expression node whose token span equals the selection [start, end).
function expressionInRange(sourceFile: SourceFile, start: number, end: number): Node | undefined {
  let found: Node | undefined;
  const visit = (node: Node): void => {
    const nodeStart = skipTrivia(sourceFile.text, node.pos);
    if (nodeStart === start && node.end === end && EXPRESSION_KINDS.has(node.kind) && !found) {
      found = node;
    }
    forEachChild(node, child => {
      visit(child);
      return undefined;
    });
  };
  visit(sourceFile);
  return found;
}

// The statement directly inside a Block that encloses the node, or undefined if
// the node is not within a block (e.g. a field initializer).
function enclosingStatementInBlock(node: Node): Node | undefined {
  let current: Node | undefined = node.parent;
  let child: Node = node;
  while (current) {
    if (current.kind === SyntaxKind.Block) return child;
    child = current;
    current = current.parent;
  }
  return undefined;
}

function indentationAt(text: string, offset: number): string {
  const lineStart = text.lastIndexOf("\n", offset - 1) + 1;
  return text.slice(lineStart, offset);
}

function extractLocalVariable(
  sourceFile: SourceFile,
  start: number,
  end: number,
): CodeActionResult[] {
  if (end <= start) return [];
  const expression = expressionInRange(sourceFile, start, end);
  if (!expression) return [];
  const statement = enclosingStatementInBlock(expression);
  if (!statement) return [];

  const statementStart = skipTrivia(sourceFile.text, statement.pos);
  const indent = indentationAt(sourceFile.text, statementStart);
  const exprText = sourceFile.text.slice(start, end);
  const name = "extracted";

  return [
    {
      title: "Extract local variable",
      kind: "refactor.extract",
      changes: [
        {
          start: statementStart,
          end: statementStart,
          newText: `var ${name} = ${exprText};\n${indent}`,
        },
        { start, end, newText: name },
      ],
    },
  ];
}

// --- inline local variable ---------------------------------------------------------

// Initializers whose precedence is below a primary need wrapping in parentheses
// when substituted into a larger expression.
const NEEDS_PARENTHESES = new Set<SyntaxKind>([
  SyntaxKind.BinaryExpression,
  SyntaxKind.ConditionalExpression,
  SyntaxKind.InstanceofExpression,
  SyntaxKind.AssignmentExpression,
  SyntaxKind.CastExpression,
  SyntaxKind.LambdaExpression,
]);

function isAssignmentTarget(use: Node): boolean {
  return (
    use.parent.kind === SyntaxKind.AssignmentExpression &&
    (use.parent as AssignmentExpression).left === use
  );
}

function inlineLocalVariable(
  program: Program,
  checker: Checker,
  sourceFile: SourceFile,
  start: number,
): CodeActionResult[] {
  const identifier = getIdentifierAtPosition(sourceFile, start) as Identifier | undefined;
  if (!identifier) return [];
  const symbol = checker.resolveName(identifier);
  if (!symbol || !(symbol.flags & SymbolFlags.LocalVariable)) return [];

  const declarator = symbol.valueDeclaration as VariableDeclarator | undefined;
  if (!declarator || declarator.kind !== SyntaxKind.VariableDeclarator) return [];
  const initializer = declarator.initializer;
  if (!initializer) return []; // nothing to inline

  const statement = declarator.parent as LocalVariableDeclarationStatement;
  if (
    statement.kind !== SyntaxKind.LocalVariableDeclarationStatement ||
    statement.declarators.length !== 1
  ) {
    return []; // multi-declarator statements are not handled
  }

  const uses = findReferences(symbol, program, checker.resolveName).filter(
    node => node !== declarator.name,
  );
  if (uses.some(isAssignmentTarget)) return []; // reassigned: inlining would change semantics

  const initText = sourceFile.text.slice(
    skipTrivia(sourceFile.text, initializer.pos),
    initializer.end,
  );
  const replacement = NEEDS_PARENTHESES.has(initializer.kind) ? `(${initText})` : initText;

  const changes: TextChange[] = uses.map(use => ({
    start: skipTrivia(sourceFile.text, use.pos),
    end: use.end,
    newText: replacement,
  }));

  // Delete the whole declaration line (indentation through the trailing newline).
  const statementStart = skipTrivia(sourceFile.text, statement.pos);
  const lineStart = sourceFile.text.lastIndexOf("\n", statementStart - 1) + 1;
  const afterNewline = sourceFile.text.indexOf("\n", statement.end);
  const lineEnd = afterNewline < 0 ? sourceFile.text.length : afterNewline + 1;
  changes.push({ start: lineStart, end: lineEnd, newText: "" });

  return [{ title: "Inline local variable", kind: "refactor.inline", changes }];
}

// --- change signature: remove an unused parameter ----------------------------------

// Delete the element at `index` from a comma-separated list, taking the adjacent
// comma with it (the following one, or the preceding one for the last element).
function listItemRemoval(text: string, nodes: readonly Node[], index: number): TextChange {
  const startOf = (n: Node) => skipTrivia(text, n.pos);
  if (nodes.length === 1) return { start: startOf(nodes[0]!), end: nodes[0]!.end, newText: "" };
  if (index < nodes.length - 1) {
    return { start: startOf(nodes[index]!), end: startOf(nodes[index + 1]!), newText: "" };
  }
  return { start: nodes[index - 1]!.end, end: nodes[index]!.end, newText: "" };
}

// The call expression whose callee is this method-name reference, or undefined
// (e.g. the declaration name, or a non-call reference).
function callForMethodReference(reference: Node): CallExpression | undefined {
  const parent = reference.parent;
  if (
    parent.kind === SyntaxKind.CallExpression &&
    (parent as CallExpression).expression === reference
  ) {
    return parent as CallExpression;
  }
  if (
    parent.kind === SyntaxKind.PropertyAccessExpression &&
    (parent as PropertyAccessExpression).name === reference &&
    parent.parent.kind === SyntaxKind.CallExpression &&
    (parent.parent as CallExpression).expression === parent
  ) {
    return parent.parent as CallExpression;
  }
  return undefined;
}

function removeUnusedParameter(
  program: Program,
  checker: Checker,
  sourceFile: SourceFile,
  start: number,
): CodeActionResult[] {
  const identifier = getIdentifierAtPosition(sourceFile, start) as Identifier | undefined;
  if (!identifier) return [];
  const symbol = checker.resolveName(identifier);
  if (!symbol || !(symbol.flags & SymbolFlags.Parameter)) return [];

  const parameter = symbol.valueDeclaration;
  if (!parameter || parameter.kind !== SyntaxKind.Parameter) return [];
  const method = parameter.parent as MethodDeclaration;
  if (method.kind !== SyntaxKind.MethodDeclaration || !method.symbol) return [];
  const paramIndex = method.parameters.indexOf(parameter as never);
  if (paramIndex < 0) return [];

  // Only when the parameter is genuinely unused in the body.
  const uses = findReferences(symbol, program, checker.resolveName).filter(
    n => n !== (parameter as { name?: Node }).name,
  );
  if (uses.length > 0) return [];

  // Overloads share a symbol; removing an argument by position would corrupt the
  // other overloads' calls, so only handle a uniquely-named method.
  if ((method.symbol.declarations?.length ?? 0) !== 1) return [];

  // Gather call sites. Bail if any is in another file (this action edits one file).
  const calls: CallExpression[] = [];
  for (const reference of findReferences(method.symbol, program, checker.resolveName)) {
    const call = callForMethodReference(reference);
    if (!call) continue;
    if (getSourceFileOfNode(call).fileName !== sourceFile.fileName) return [];
    if (call.arguments.length === method.parameters.length) calls.push(call);
  }

  const name = (parameter as unknown as { name: Identifier }).name.text;
  const changes: TextChange[] = [listItemRemoval(sourceFile.text, method.parameters, paramIndex)];
  for (const call of calls) {
    changes.push(listItemRemoval(sourceFile.text, call.arguments, paramIndex));
  }
  return [{ title: `Remove unused parameter '${name}'`, kind: "refactor.rewrite", changes }];
}

export function getCodeActions(
  program: Program,
  checker: Checker,
  sourceFile: SourceFile,
  start: number,
  end: number,
): CodeActionResult[] {
  return [
    ...addMissingImport(program, checker, sourceFile, start),
    ...organizeImports(sourceFile),
    ...extractLocalVariable(sourceFile, start, end),
    ...inlineLocalVariable(program, checker, sourceFile, start),
    ...removeUnusedParameter(program, checker, sourceFile, start),
  ];
}
