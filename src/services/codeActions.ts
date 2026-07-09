// Refactor / quick-fix code actions. Each action is computed as plain text
// changes (offset ranges + replacement text) over a single file, so the logic is
// pure and testable; the server maps them to LSP WorkspaceEdits. Actions are
// offered for a [start, end) selection range in one source file.

import { type Checker, findUnusedImports } from "../compiler/checker.ts";
import { Diagnostics } from "../compiler/diagnostics.ts";
import { getIdentifierAtPosition, getNodeAtPosition } from "./nodeAtPosition.ts";
import { forEachChild } from "../compiler/parser.ts";
import type { Program } from "../compiler/program.ts";
import {
  findReferences,
  getDirectSuperTypeSymbols,
  getSourceFileOfNode,
  resolveTypeEntityName,
} from "../compiler/resolver.ts";
import {
  type Annotation,
  type AssignmentExpression,
  type BinaryExpression,
  type Block,
  type CallExpression,
  type CastExpression,
  type ConditionalExpression,
  type CatchClause,
  type ClassDeclaration,
  type ConstructorDeclaration,
  type ExpressionStatement,
  type FieldDeclaration,
  type Identifier,
  type IfStatement,
  type ImportDeclaration,
  type InstanceofExpression,
  type LambdaExpression,
  type LiteralExpression,
  type LocalVariableDeclarationStatement,
  type MethodDeclaration,
  type Node,
  type ObjectCreationExpression,
  type Parameter,
  type PrefixUnaryExpression,
  type PrimitiveType,
  type PropertyAccessExpression,
  type QualifiedName,
  type ReturnStatement,
  type SourceFile,
  type Statement,
  type Symbol,
  SymbolFlags,
  type SwitchStatement,
  SyntaxKind,
  type ThisExpression,
  type TryStatement,
  type TypeNode,
  type TypeReference,
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
  /** Edits to OTHER documents, keyed by uri. Unset for single-file actions. */
  readonly additionalEdits?: Record<string, TextChange[]>;
}

// Which language-level features the target Java version supports. Computed once
// (from the configured javac --release) and threaded into getCodeActions, so
// each modern-Java rewrite just checks a boolean instead of a version number.
export interface LanguageFeatures {
  readonly supportsDiamond: boolean; // SE7
  readonly supportsMultiCatch: boolean; // SE7
  readonly supportsVar: boolean; // SE10
  readonly supportsLambda: boolean; // SE8
  readonly supportsArrowSwitch: boolean; // SE14
  readonly supportsRecord: boolean; // SE16
  readonly supportsInstanceofPattern: boolean; // SE16
}

// An unset release means the toolchain default (a modern JDK): everything on.
export function languageFeatures(release: number | undefined): LanguageFeatures {
  const at = (min: number) => release === undefined || release >= min;
  return {
    supportsDiamond: at(7),
    supportsMultiCatch: at(7),
    supportsVar: at(10),
    supportsLambda: at(8),
    supportsArrowSwitch: at(14),
    supportsRecord: at(16),
    supportsInstanceofPattern: at(16),
  };
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
    const last = sourceFile.imports.at(-1)!;
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
  const sorted = kept.toSorted((a, b) => {
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

// --- remove unused import ----------------------------------------------------

function removeUnusedImport(
  sourceFile: SourceFile,
  start: number,
  end: number,
): CodeActionResult[] {
  if (sourceFile.parseDiagnostics.length > 0) return []; // same gate as the diagnostic
  return findUnusedImports(sourceFile)
    .filter(imp => skipTrivia(sourceFile.text, imp.pos) <= end && start <= imp.end)
    .map(imp => {
      const importStart = skipTrivia(sourceFile.text, imp.pos);
      // take the trailing line break with the import
      let removeEnd = imp.end;
      if (sourceFile.text[removeEnd] === "\r") removeEnd++;
      if (sourceFile.text[removeEnd] === "\n") removeEnd++;
      return {
        title: `Remove unused import '${entityNameToString(imp.name)}'`,
        kind: "quickfix",
        changes: [{ start: importStart, end: removeEnd, newText: "" }],
      };
    });
}

// --- remove a redundant @Override ----------------------------------------------

// The enclosing method declaration of a position, or undefined.
function enclosingMethod(root: Node, offset: number): MethodDeclaration | undefined {
  let node: Node | undefined = getNodeAtPosition(root, offset);
  while (node && node.kind !== SyntaxKind.MethodDeclaration) node = node.parent;
  return node as MethodDeclaration | undefined;
}

// The @Override annotation among a method's modifiers, or undefined.
function overrideAnnotation(method: MethodDeclaration): Annotation | undefined {
  for (const m of method.modifiers ?? []) {
    if (m.kind !== SyntaxKind.Annotation) continue;
    if (entityNameToString((m as Annotation).typeName).replace(/^.*\./, "") === "Override") {
      return m as Annotation;
    }
  }
  return undefined;
}

// For a method flagged with "does not override a supertype method" (1301),
// offer to remove its erroneous @Override annotation.
function removeRedundantOverride(
  checker: Checker,
  sourceFile: SourceFile,
  start: number,
): CodeActionResult[] {
  if (sourceFile.parseDiagnostics.length > 0) return [];
  const method = enclosingMethod(sourceFile, start);
  if (!method) return [];
  const annotation = overrideAnnotation(method);
  if (!annotation) return [];
  // Only when the checker actually flagged this method's @Override as wrong.
  const wrong = checker
    .getSemanticDiagnostics(sourceFile)
    .some(
      d =>
        d.code === Diagnostics.Method_does_not_override_a_supertype_method.code &&
        d.pos >= method.pos &&
        d.end <= method.end,
    );
  if (!wrong) return [];
  // Delete the annotation and the whitespace up to the next token.
  const from = skipTrivia(sourceFile.text, annotation.pos);
  const to = skipTrivia(sourceFile.text, annotation.end);
  return [
    {
      title: "Remove redundant '@Override'",
      kind: "quickfix",
      changes: [{ start: from, end: to, newText: "" }],
    },
  ];
}

// --- make an effectively-final field explicitly final --------------------------

// For a field the checker flagged as "can be 'final'" (1317), offer to insert
// the modifier. Inserting right before the type lands after all existing
// modifiers/annotations, giving the conventional `private static final T` order.
function makeFieldFinal(
  checker: Checker,
  sourceFile: SourceFile,
  start: number,
): CodeActionResult[] {
  if (sourceFile.parseDiagnostics.length > 0) return [];
  let node: Node | undefined = getNodeAtPosition(sourceFile, start);
  while (node && node.kind !== SyntaxKind.FieldDeclaration) node = node.parent;
  if (!node) return [];
  const field = node as FieldDeclaration;
  // Only when the checker actually flagged this field's declarators.
  const flagged = checker
    .getSemanticDiagnostics(sourceFile)
    .some(
      d =>
        d.code === Diagnostics.Field_0_can_be_final.code &&
        d.pos >= field.pos &&
        d.end <= field.end,
    );
  if (!flagged) return [];
  const at = skipTrivia(sourceFile.text, field.type.pos);
  return [
    {
      title: "Add 'final' modifier",
      kind: "quickfix",
      changes: [{ start: at, end: at, newText: "final " }],
    },
  ];
}

// --- Optional.ofNullable(x).ifPresent(lambda) -> if (x != null) ----------------

// For the checker's "can be replaced with a null check" warning (1318), offer
// the rewrite when it is provably safe: the chain is a whole statement, the
// ofNullable argument is a plain variable (so it is evaluated once either way),
// and the action is a lambda whose parameter can be renamed to that variable.
function replaceOptionalIfPresentWithNullCheck(
  checker: Checker,
  sourceFile: SourceFile,
  start: number,
): CodeActionResult[] {
  if (sourceFile.parseDiagnostics.length > 0) return [];
  let node: Node | undefined = getNodeAtPosition(sourceFile, start);
  while (node && node.kind !== SyntaxKind.ExpressionStatement) node = node.parent;
  if (!node) return [];
  const stmt = node as ExpressionStatement;
  // Only when the checker actually flagged this statement's chain (the FQN
  // check against java.util.Optional lives there).
  const flagged = checker
    .getSemanticDiagnostics(sourceFile)
    .some(
      d =>
        d.code ===
          Diagnostics.Optional_ofNullable_ifPresent_can_be_replaced_with_a_null_check.code &&
        d.pos >= stmt.pos &&
        d.end <= stmt.end,
    );
  if (!flagged) return [];
  if (stmt.expression.kind !== SyntaxKind.CallExpression) return [];
  const outer = stmt.expression as CallExpression;
  if (outer.expression.kind !== SyntaxKind.PropertyAccessExpression) return [];
  const receiver = (outer.expression as PropertyAccessExpression).expression;
  if (receiver.kind !== SyntaxKind.CallExpression) return [];
  const variableNode = (receiver as CallExpression).arguments[0];
  if (variableNode?.kind !== SyntaxKind.Identifier) return []; // expression: warn only
  const variable = (variableNode as Identifier).text;
  const action = outer.arguments[0];
  if (action?.kind !== SyntaxKind.LambdaExpression) return []; // method ref: warn only
  const lambda = action as LambdaExpression;
  if (lambda.parameters.length !== 1) return [];
  const param = lambda.parameters[0]!;
  const paramName =
    param.kind === SyntaxKind.Identifier
      ? (param as Identifier).text
      : (param as Parameter).name?.text;
  if (!paramName) return [];

  // Rename lambda-parameter uses to the variable. A use is a plain identifier
  // reference; the member name of `o.v` is not one.
  // ponytail: ignores a shadowing redeclaration of the parameter name inside
  // the body; resolve identifiers through the checker if that ever bites.
  const renames: { start: number; end: number }[] = [];
  if (paramName !== variable) {
    const collect = (n: Node, parent: Node): void => {
      if (
        n.kind === SyntaxKind.Identifier &&
        (n as Identifier).text === paramName &&
        !(
          parent.kind === SyntaxKind.PropertyAccessExpression &&
          (parent as PropertyAccessExpression).name === n
        )
      ) {
        renames.push({ start: skipTrivia(sourceFile.text, n.pos), end: n.end });
      }
      forEachChild(n, child => {
        collect(child, n);
        return undefined;
      });
    };
    forEachChild(lambda.body, child => {
      collect(child, lambda.body);
      return undefined;
    });
  }
  const renamed = (from: number, to: number): string => {
    let out = "";
    let at = from;
    for (const r of renames) {
      out += sourceFile.text.slice(at, r.start) + variable;
      at = r.end;
    }
    return out + sourceFile.text.slice(at, to);
  };
  const bodyStart = skipTrivia(sourceFile.text, lambda.body.pos);
  const body =
    lambda.body.kind === SyntaxKind.Block
      ? renamed(bodyStart, lambda.body.end) // keeps the `{ ... }`
      : `{ ${renamed(bodyStart, lambda.body.end)}; }`;
  const from = skipTrivia(sourceFile.text, stmt.pos);
  return [
    {
      title: "Replace with null check",
      kind: "quickfix",
      changes: [{ start: from, end: stmt.end, newText: `if (${variable} != null) ${body}` }],
    },
  ];
}

// --- Optional.of(null) -> ofNullable (nikeee/cappu#42 follow-up) ---------------

function replaceOptionalOfNull(
  checker: Checker,
  sourceFile: SourceFile,
  start: number,
): CodeActionResult[] {
  if (sourceFile.parseDiagnostics.length > 0) return [];
  let node: Node | undefined = getNodeAtPosition(sourceFile, start);
  while (node && node.kind !== SyntaxKind.CallExpression) node = node.parent;
  if (!node) return [];
  const call = node as CallExpression;
  const flagged = checker
    .getSemanticDiagnostics(sourceFile)
    .some(
      d =>
        d.code === Diagnostics.Optional_of_null_always_throws.code &&
        d.pos >= call.pos &&
        d.end <= call.end,
    );
  if (!flagged) return [];
  if (call.expression.kind !== SyntaxKind.PropertyAccessExpression) return [];
  const access = call.expression as PropertyAccessExpression;
  const nameStart = skipTrivia(sourceFile.text, access.name.pos);
  return [
    {
      title: "Replace with Optional.ofNullable(null)",
      kind: "quickfix",
      changes: [{ start: nameStart, end: access.name.end, newText: "ofNullable" }],
    },
  ];
}

// --- boolean literal comparison simplification (nikeee/cappu#42 follow-up) -----

// Expression kinds that can have a leading `!` applied without changing
// meaning (Java's primary/postfix expressions). Anything else (a
// binary/ternary/cast/...) must be parenthesized first. Shared by the
// boolean-comparison and boolean-ternary quickfixes.
const SAFE_NOT_OPERAND_KINDS = new Set<SyntaxKind>([
  SyntaxKind.Identifier,
  SyntaxKind.PropertyAccessExpression,
  SyntaxKind.CallExpression,
  SyntaxKind.ParenthesizedExpression,
  SyntaxKind.ThisExpression,
  SyntaxKind.StringLiteral,
  SyntaxKind.TextBlockLiteral,
  SyntaxKind.ObjectCreationExpression,
]);

function negatedText(cond: Node, condText: string): string {
  return SAFE_NOT_OPERAND_KINDS.has(cond.kind) ? `!${condText}` : `!(${condText})`;
}

function replaceBooleanComparison(
  checker: Checker,
  sourceFile: SourceFile,
  start: number,
): CodeActionResult[] {
  if (sourceFile.parseDiagnostics.length > 0) return [];
  let node: Node | undefined = getNodeAtPosition(sourceFile, start);
  while (node && node.kind !== SyntaxKind.BinaryExpression) node = node.parent;
  if (!node) return [];
  const bin = node as BinaryExpression;
  const flagged = checker
    .getSemanticDiagnostics(sourceFile)
    .some(
      d =>
        d.code === Diagnostics.Redundant_boolean_comparison_0_can_be_replaced_with_1.code &&
        d.pos >= bin.pos &&
        d.end <= bin.end,
    );
  if (!flagged) return [];
  const isBoolLiteral = (n: Node) =>
    n.kind === SyntaxKind.TrueKeyword || n.kind === SyntaxKind.FalseKeyword;
  let cond: Node;
  let literalIsTrue: boolean;
  if (isBoolLiteral(bin.right) && !isBoolLiteral(bin.left)) {
    cond = bin.left;
    literalIsTrue = bin.right.kind === SyntaxKind.TrueKeyword;
  } else if (isBoolLiteral(bin.left) && !isBoolLiteral(bin.right)) {
    cond = bin.right;
    literalIsTrue = bin.left.kind === SyntaxKind.TrueKeyword;
  } else {
    return [];
  }
  const negate =
    bin.operatorToken === SyntaxKind.EqualsEqualsToken ? !literalIsTrue : literalIsTrue;
  const condStart = skipTrivia(sourceFile.text, cond.pos);
  const condText = sourceFile.text.slice(condStart, cond.end);
  const binStart = skipTrivia(sourceFile.text, bin.pos);
  return [
    {
      title: "Simplify boolean comparison",
      kind: "quickfix",
      changes: [
        {
          start: binStart,
          end: bin.end,
          newText: negate ? negatedText(cond, condText) : condText,
        },
      ],
    },
  ];
}

// --- if/else returning booleans -> return cond (nikeee/cappu#42 follow-up) -----

// A branch that is a single `return true;`/`return false;`, possibly wrapped
// in a block, or undefined otherwise.
function singleReturnBoolean(stmt: Node | undefined): { value: boolean } | undefined {
  if (!stmt) return undefined;
  let ret = stmt;
  if (stmt.kind === SyntaxKind.Block) {
    const statements = (stmt as Block).statements;
    if (statements.length !== 1) return undefined;
    ret = statements[0]!;
  }
  if (ret.kind !== SyntaxKind.ReturnStatement) return undefined;
  const expr = (ret as ReturnStatement).expression;
  if (expr?.kind === SyntaxKind.TrueKeyword) return { value: true };
  if (expr?.kind === SyntaxKind.FalseKeyword) return { value: false };
  return undefined;
}

function replaceIfElseReturningBoolean(
  checker: Checker,
  sourceFile: SourceFile,
  start: number,
): CodeActionResult[] {
  if (sourceFile.parseDiagnostics.length > 0) return [];
  let node: Node | undefined = getNodeAtPosition(sourceFile, start);
  while (node && node.kind !== SyntaxKind.IfStatement) node = node.parent;
  if (!node) return [];
  const ifStmt = node as IfStatement;
  const flagged = checker
    .getSemanticDiagnostics(sourceFile)
    .some(
      d =>
        d.code === Diagnostics.If_else_returning_booleans_0_can_be_replaced_with_1.code &&
        d.pos >= ifStmt.pos &&
        d.end <= ifStmt.end,
    );
  if (!flagged) return [];
  const thenInfo = singleReturnBoolean(ifStmt.thenStatement);
  const elseInfo = singleReturnBoolean(ifStmt.elseStatement);
  if (!thenInfo || !elseInfo || thenInfo.value === elseInfo.value) return [];
  const negate = !thenInfo.value;
  const condStart = skipTrivia(sourceFile.text, ifStmt.condition.pos);
  const condText = sourceFile.text.slice(condStart, ifStmt.condition.end);
  const newText = negate
    ? `return ${negatedText(ifStmt.condition, condText)};`
    : `return ${condText};`;
  const ifStart = skipTrivia(sourceFile.text, ifStmt.pos);
  return [
    {
      title: "Simplify to return statement",
      kind: "quickfix",
      changes: [{ start: ifStart, end: ifStmt.end, newText }],
    },
  ];
}

// --- ternary with boolean literals (nikeee/cappu#42 follow-up) -----------------

function replaceTernaryBooleanLiterals(
  checker: Checker,
  sourceFile: SourceFile,
  start: number,
): CodeActionResult[] {
  if (sourceFile.parseDiagnostics.length > 0) return [];
  let node: Node | undefined = getNodeAtPosition(sourceFile, start);
  while (node && node.kind !== SyntaxKind.ConditionalExpression) node = node.parent;
  if (!node) return [];
  const expr = node as ConditionalExpression;
  const flagged = checker
    .getSemanticDiagnostics(sourceFile)
    .some(
      d =>
        d.code === Diagnostics.Ternary_with_boolean_literals_0_can_be_replaced_with_1.code &&
        d.pos >= expr.pos &&
        d.end <= expr.end,
    );
  if (!flagged) return [];
  const isBoolLiteral = (n: Node) =>
    n.kind === SyntaxKind.TrueKeyword || n.kind === SyntaxKind.FalseKeyword;
  if (!isBoolLiteral(expr.whenTrue) || !isBoolLiteral(expr.whenFalse)) return [];
  const whenTrueIsTrue = expr.whenTrue.kind === SyntaxKind.TrueKeyword;
  const whenFalseIsTrue = expr.whenFalse.kind === SyntaxKind.TrueKeyword;
  if (whenTrueIsTrue === whenFalseIsTrue) return [];
  const negate = !whenTrueIsTrue;
  const condStart = skipTrivia(sourceFile.text, expr.condition.pos);
  const condText = sourceFile.text.slice(condStart, expr.condition.end);
  const newText = negate ? negatedText(expr.condition, condText) : condText;
  const exprStart = skipTrivia(sourceFile.text, expr.pos);
  return [
    {
      title: "Simplify boolean ternary",
      kind: "quickfix",
      changes: [{ start: exprStart, end: expr.end, newText }],
    },
  ];
}

// --- collapsible nested if -> merge with && (nikeee/cappu#42 follow-up) --------

// The sole `if` inside a statement, unwrapping one block layer if present, or
// undefined otherwise.
function singleStatementIf(stmt: Node): IfStatement | undefined {
  if (stmt.kind === SyntaxKind.IfStatement) return stmt as IfStatement;
  if (stmt.kind === SyntaxKind.Block) {
    const statements = (stmt as Block).statements;
    if (statements.length === 1 && statements[0]!.kind === SyntaxKind.IfStatement) {
      return statements[0] as IfStatement;
    }
  }
  return undefined;
}

function replaceCollapsibleIf(
  checker: Checker,
  sourceFile: SourceFile,
  start: number,
): CodeActionResult[] {
  if (sourceFile.parseDiagnostics.length > 0) return [];
  let node: Node | undefined = getNodeAtPosition(sourceFile, start);
  while (node && node.kind !== SyntaxKind.IfStatement) node = node.parent;
  if (!node) return [];
  const outer = node as IfStatement;
  const flagged = checker
    .getSemanticDiagnostics(sourceFile)
    .some(
      d =>
        d.code === Diagnostics.Nested_if_can_be_collapsed_to_if_0.code &&
        d.pos >= outer.pos &&
        d.end <= outer.end,
    );
  if (!flagged) return [];
  if (outer.elseStatement) return [];
  const inner = singleStatementIf(outer.thenStatement);
  if (!inner || inner.elseStatement) return [];
  const outerCondStart = skipTrivia(sourceFile.text, outer.condition.pos);
  const outerCondText = sourceFile.text.slice(outerCondStart, outer.condition.end);
  const innerCondStart = skipTrivia(sourceFile.text, inner.condition.pos);
  const innerCondText = sourceFile.text.slice(innerCondStart, inner.condition.end);
  const innerThenStart = skipTrivia(sourceFile.text, inner.thenStatement.pos);
  const innerThenText = sourceFile.text.slice(innerThenStart, inner.thenStatement.end);
  const outerStart = skipTrivia(sourceFile.text, outer.pos);
  return [
    {
      title: "Collapse nested if",
      kind: "quickfix",
      changes: [
        {
          start: outerStart,
          end: outer.end,
          newText: `if ((${outerCondText}) && (${innerCondText})) ${innerThenText}`,
        },
      ],
    },
  ];
}

// --- size()/length() compared to 0/1 -> isEmpty()/!isEmpty() (nikeee/cappu#42) ---

// The receiver of a zero-arg `size()`/`length()` call, or undefined. FQN
// isn't re-checked here: the diagnostic gate below already proved it.
function countCallReceiver(n: Node): Node | undefined {
  if (n.kind !== SyntaxKind.CallExpression) return undefined;
  const call = n as CallExpression;
  if (call.arguments.length !== 0 || call.expression.kind !== SyntaxKind.PropertyAccessExpression) {
    return undefined;
  }
  const access = call.expression as PropertyAccessExpression;
  if (access.name.text !== "size" && access.name.text !== "length") return undefined;
  return access.expression;
}

function flipComparison(op: SyntaxKind): SyntaxKind {
  switch (op) {
    case SyntaxKind.LessThanToken:
      return SyntaxKind.GreaterThanToken;
    case SyntaxKind.GreaterThanToken:
      return SyntaxKind.LessThanToken;
    case SyntaxKind.LessThanEqualsToken:
      return SyntaxKind.GreaterThanEqualsToken;
    case SyntaxKind.GreaterThanEqualsToken:
      return SyntaxKind.LessThanEqualsToken;
    default:
      return op;
  }
}

function countCheckNegates(op: SyntaxKind, literal: string): boolean | undefined {
  if (literal === "0") {
    if (op === SyntaxKind.EqualsEqualsToken) return false;
    if (op === SyntaxKind.ExclamationEqualsToken) return true;
    if (op === SyntaxKind.GreaterThanToken) return true;
    if (op === SyntaxKind.LessThanEqualsToken) return false;
  } else if (literal === "1") {
    if (op === SyntaxKind.LessThanToken) return false;
    if (op === SyntaxKind.GreaterThanEqualsToken) return true;
  }
  return undefined;
}

function replaceCountComparedToZero(
  checker: Checker,
  sourceFile: SourceFile,
  start: number,
): CodeActionResult[] {
  if (sourceFile.parseDiagnostics.length > 0) return [];
  let node: Node | undefined = getNodeAtPosition(sourceFile, start);
  while (node && node.kind !== SyntaxKind.BinaryExpression) node = node.parent;
  if (!node) return [];
  const bin = node as BinaryExpression;
  const flagged = checker
    .getSemanticDiagnostics(sourceFile)
    .some(
      d =>
        d.code === Diagnostics.Count_check_0_can_be_replaced_with_1.code &&
        d.pos >= bin.pos &&
        d.end <= bin.end,
    );
  if (!flagged) return [];
  let receiver = countCallReceiver(bin.left);
  let literalNode: LiteralExpression | undefined;
  let op = bin.operatorToken;
  if (receiver && bin.right.kind === SyntaxKind.NumericLiteral) {
    literalNode = bin.right as LiteralExpression;
  } else {
    receiver = countCallReceiver(bin.right);
    if (!receiver || bin.left.kind !== SyntaxKind.NumericLiteral) return [];
    literalNode = bin.left as LiteralExpression;
    op = flipComparison(op);
  }
  const negate = countCheckNegates(op, literalNode.value);
  if (negate === undefined) return [];
  const receiverStart = skipTrivia(sourceFile.text, receiver.pos);
  const receiverText = sourceFile.text.slice(receiverStart, receiver.end);
  const binStart = skipTrivia(sourceFile.text, bin.pos);
  return [
    {
      title: "Replace with isEmpty() check",
      kind: "quickfix",
      changes: [
        {
          start: binStart,
          end: bin.end,
          newText: `${negate ? "!" : ""}${receiverText}.isEmpty()`,
        },
      ],
    },
  ];
}

// --- == / != on Strings -> equals() (nikeee/cappu#42) --------------------------

// Expression kinds that can have `.equals(` appended directly without
// changing meaning (Java's primary/postfix expressions). Anything else (a
// binary/unary/ternary/cast/...) must be parenthesized first.
const SAFE_EQUALS_RECEIVER_KINDS = new Set<SyntaxKind>([
  SyntaxKind.Identifier,
  SyntaxKind.PropertyAccessExpression,
  SyntaxKind.CallExpression,
  SyntaxKind.ParenthesizedExpression,
  SyntaxKind.ThisExpression,
  SyntaxKind.StringLiteral,
  SyntaxKind.TextBlockLiteral,
  SyntaxKind.ObjectCreationExpression,
]);

function replaceStringEquality(
  checker: Checker,
  sourceFile: SourceFile,
  start: number,
): CodeActionResult[] {
  if (sourceFile.parseDiagnostics.length > 0) return [];
  let node: Node | undefined = getNodeAtPosition(sourceFile, start);
  while (node && node.kind !== SyntaxKind.BinaryExpression) node = node.parent;
  if (!node) return [];
  const bin = node as BinaryExpression;
  const flagged = checker
    .getSemanticDiagnostics(sourceFile)
    .some(
      d =>
        d.code === Diagnostics.Strings_should_be_compared_with_equals_not_0.code &&
        d.pos >= bin.pos &&
        d.end <= bin.end,
    );
  if (!flagged) return [];
  const negated = bin.operatorToken === SyntaxKind.ExclamationEqualsToken;
  const leftStart = skipTrivia(sourceFile.text, bin.left.pos);
  const leftText = sourceFile.text.slice(leftStart, bin.left.end);
  const rightStart = skipTrivia(sourceFile.text, bin.right.pos);
  const rightText = sourceFile.text.slice(rightStart, bin.right.end);
  const receiver = SAFE_EQUALS_RECEIVER_KINDS.has(bin.left.kind) ? leftText : `(${leftText})`;
  const binStart = skipTrivia(sourceFile.text, bin.pos);
  return [
    {
      title: "Replace with equals()",
      kind: "quickfix",
      changes: [
        {
          start: binStart,
          end: bin.end,
          newText: `${negated ? "!" : ""}${receiver}.equals(${rightText})`,
        },
      ],
    },
  ];
}

// --- boxed reference == comparison -> equals() (nikeee/cappu#42 follow-up) -----

function replaceBoxedEquality(
  checker: Checker,
  sourceFile: SourceFile,
  start: number,
): CodeActionResult[] {
  if (sourceFile.parseDiagnostics.length > 0) return [];
  let node: Node | undefined = getNodeAtPosition(sourceFile, start);
  while (node && node.kind !== SyntaxKind.BinaryExpression) node = node.parent;
  if (!node) return [];
  const bin = node as BinaryExpression;
  const flagged = checker
    .getSemanticDiagnostics(sourceFile)
    .some(
      d =>
        d.code === Diagnostics.Boxed_types_should_be_compared_with_equals_not_0.code &&
        d.pos >= bin.pos &&
        d.end <= bin.end,
    );
  if (!flagged) return [];
  const negated = bin.operatorToken === SyntaxKind.ExclamationEqualsToken;
  const leftStart = skipTrivia(sourceFile.text, bin.left.pos);
  const leftText = sourceFile.text.slice(leftStart, bin.left.end);
  const rightStart = skipTrivia(sourceFile.text, bin.right.pos);
  const rightText = sourceFile.text.slice(rightStart, bin.right.end);
  const receiver = SAFE_EQUALS_RECEIVER_KINDS.has(bin.left.kind) ? leftText : `(${leftText})`;
  const binStart = skipTrivia(sourceFile.text, bin.pos);
  return [
    {
      title: "Replace with equals()",
      kind: "quickfix",
      changes: [
        {
          start: binStart,
          end: bin.end,
          newText: `${negated ? "!" : ""}${receiver}.equals(${rightText})`,
        },
      ],
    },
  ];
}

// --- boxing constructors (`new Integer(...)`, ...) -> valueOf() (nikeee/cappu#42) ---

function replaceBoxingConstructor(
  checker: Checker,
  sourceFile: SourceFile,
  start: number,
): CodeActionResult[] {
  if (sourceFile.parseDiagnostics.length > 0) return [];
  let node: Node | undefined = getNodeAtPosition(sourceFile, start);
  while (node && node.kind !== SyntaxKind.ObjectCreationExpression) node = node.parent;
  if (!node) return [];
  const creation = node as ObjectCreationExpression;
  const flagged = checker
    .getSemanticDiagnostics(sourceFile)
    .some(
      d =>
        d.code === Diagnostics.Boxing_constructor_new_0_is_deprecated.code &&
        d.pos >= creation.pos &&
        d.end <= creation.end,
    );
  if (!flagged) return [];
  if (creation.type.kind !== SyntaxKind.TypeReference) return [];
  const typeName = entityNameToString((creation.type as TypeReference).typeName);
  const from = skipTrivia(sourceFile.text, creation.pos);
  return [
    {
      title: "Replace with valueOf()",
      kind: "quickfix",
      changes: [{ start: from, end: creation.type.end, newText: `${typeName}.valueOf` }],
    },
  ];
}

// --- indexOf(...) != -1 -> contains(...) (nikeee/cappu#42) ---------------------

function isNegativeOneLiteral(n: Node): boolean {
  return (
    n.kind === SyntaxKind.PrefixUnaryExpression &&
    (n as PrefixUnaryExpression).operator === SyntaxKind.MinusToken &&
    (n as PrefixUnaryExpression).operand.kind === SyntaxKind.NumericLiteral &&
    ((n as PrefixUnaryExpression).operand as LiteralExpression).value === "1"
  );
}

// The `indexOf(...)` call, or undefined. FQN isn't re-checked here: the
// diagnostic gate below already proved it.
function indexOfCall(n: Node): CallExpression | undefined {
  if (n.kind !== SyntaxKind.CallExpression) return undefined;
  const call = n as CallExpression;
  if (
    call.arguments.length !== 1 ||
    call.expression.kind !== SyntaxKind.PropertyAccessExpression ||
    (call.expression as PropertyAccessExpression).name.text !== "indexOf"
  ) {
    return undefined;
  }
  return call;
}

function replaceIndexOfComparedToNegativeOne(
  checker: Checker,
  sourceFile: SourceFile,
  start: number,
): CodeActionResult[] {
  if (sourceFile.parseDiagnostics.length > 0) return [];
  let node: Node | undefined = getNodeAtPosition(sourceFile, start);
  while (node && node.kind !== SyntaxKind.BinaryExpression) node = node.parent;
  if (!node) return [];
  const bin = node as BinaryExpression;
  const flagged = checker
    .getSemanticDiagnostics(sourceFile)
    .some(
      d =>
        d.code === Diagnostics.IndexOf_check_0_can_be_replaced_with_1.code &&
        d.pos >= bin.pos &&
        d.end <= bin.end,
    );
  if (!flagged) return [];
  const call = isNegativeOneLiteral(bin.right)
    ? indexOfCall(bin.left)
    : isNegativeOneLiteral(bin.left)
      ? indexOfCall(bin.right)
      : undefined;
  if (!call) return [];
  const access = call.expression as PropertyAccessExpression;
  const receiverStart = skipTrivia(sourceFile.text, access.expression.pos);
  const receiverText = sourceFile.text.slice(receiverStart, access.expression.end);
  const arg = call.arguments[0]!;
  const argStart = skipTrivia(sourceFile.text, arg.pos);
  const argText = sourceFile.text.slice(argStart, arg.end);
  const negate = bin.operatorToken === SyntaxKind.EqualsEqualsToken;
  const binStart = skipTrivia(sourceFile.text, bin.pos);
  return [
    {
      title: "Replace with contains()",
      kind: "quickfix",
      changes: [
        {
          start: binStart,
          end: bin.end,
          newText: `${negate ? "!" : ""}${receiverText}.contains(${argText})`,
        },
      ],
    },
  ];
}

// --- redundant new String(...) (nikeee/cappu#42) --------------------------------

function removeRedundantNewString(
  checker: Checker,
  sourceFile: SourceFile,
  start: number,
): CodeActionResult[] {
  if (sourceFile.parseDiagnostics.length > 0) return [];
  let node: Node | undefined = getNodeAtPosition(sourceFile, start);
  while (node && node.kind !== SyntaxKind.ObjectCreationExpression) node = node.parent;
  if (!node) return [];
  const creation = node as ObjectCreationExpression;
  const flagged = checker
    .getSemanticDiagnostics(sourceFile)
    .some(
      d =>
        d.code === Diagnostics.New_String_0_can_be_replaced_with_1.code &&
        d.pos >= creation.pos &&
        d.end <= creation.end,
    );
  if (!flagged) return [];
  // The diagnostic already proved: 0 args -> "", or 1 String-typed arg -> unwrap it.
  const args = creation.arguments ?? [];
  const after =
    args.length === 0
      ? '""'
      : sourceFile.text.slice(skipTrivia(sourceFile.text, args[0]!.pos), args[0]!.end);
  const from = skipTrivia(sourceFile.text, creation.pos);
  return [
    {
      title: "Remove redundant String wrapper",
      kind: "quickfix",
      changes: [{ start: from, end: creation.end, newText: after }],
    },
  ];
}

// --- equals("") -> isEmpty() (nikeee/cappu#42) ----------------------------------

function isEmptyStringLiteral(n: Node): boolean {
  return n.kind === SyntaxKind.StringLiteral && (n as LiteralExpression).value === "";
}

// Only the `s.equals("")` direction: `"".equals(s)` is a deliberate null-safe
// idiom whose autofix would change NPE behavior, so it is warn-only (no fix
// offered here - the checker still flags it via the same diagnostic).
function replaceEqualsEmptyString(
  checker: Checker,
  sourceFile: SourceFile,
  start: number,
): CodeActionResult[] {
  if (sourceFile.parseDiagnostics.length > 0) return [];
  let node: Node | undefined = getNodeAtPosition(sourceFile, start);
  while (node && node.kind !== SyntaxKind.CallExpression) node = node.parent;
  if (!node) return [];
  const call = node as CallExpression;
  if (call.expression.kind !== SyntaxKind.PropertyAccessExpression) return [];
  const access = call.expression as PropertyAccessExpression;
  if (access.name.text !== "equals" || call.arguments.length !== 1) return [];
  const arg = call.arguments[0]!;
  if (!isEmptyStringLiteral(arg) || isEmptyStringLiteral(access.expression)) return [];
  const flagged = checker
    .getSemanticDiagnostics(sourceFile)
    .some(
      d =>
        d.code === Diagnostics.Equals_empty_0_can_be_replaced_with_1.code &&
        d.pos >= call.pos &&
        d.end <= call.end,
    );
  if (!flagged) return [];
  const receiverStart = skipTrivia(sourceFile.text, access.expression.pos);
  const receiverText = sourceFile.text.slice(receiverStart, access.expression.end);
  const callStart = skipTrivia(sourceFile.text, call.pos);
  return [
    {
      title: "Replace with isEmpty()",
      kind: "quickfix",
      changes: [{ start: callStart, end: call.end, newText: `${receiverText}.isEmpty()` }],
    },
  ];
}

// --- convert a class to a record ---------------------------------------------

function hasKeyword(modifiers: readonly Node[] | undefined, kind: SyntaxKind): boolean {
  return modifiers?.some(m => m.kind === kind) ?? false;
}

function hasAnnotation(modifiers: readonly Node[] | undefined): boolean {
  return modifiers?.some(m => m.kind === SyntaxKind.Annotation) ?? false;
}

const capitalize = (s: string): string => (s ? s[0]!.toUpperCase() + s.slice(1) : s);

// The single field name a trivial getter returns (`return f;` / `return this.f;`),
// or undefined if the method is not a plain field accessor.
function getterFieldName(method: MethodDeclaration): string | undefined {
  if (!method.body || method.parameters.length > 0) return undefined;
  if (method.typeParameters?.length || method.throws?.length) return undefined;
  const body = method.body as Block;
  if (body.statements.length !== 1) return undefined;
  const stmt = body.statements[0]!;
  if (stmt.kind !== SyntaxKind.ReturnStatement) return undefined;
  const expr = (stmt as ReturnStatement).expression;
  if (!expr) return undefined;
  if (expr.kind === SyntaxKind.Identifier) return (expr as Identifier).text;
  if (
    expr.kind === SyntaxKind.PropertyAccessExpression &&
    (expr as PropertyAccessExpression).expression.kind === SyntaxKind.ThisExpression &&
    ((expr as PropertyAccessExpression).expression as ThisExpression).qualifier === undefined
  ) {
    return (expr as PropertyAccessExpression).name.text;
  }
  return undefined;
}

// The field a `this.f = p` / `f = p` assignment targets and the source it reads
// from, or undefined for any other statement shape.
function ctorAssignment(stmt: Node): { field: string; from: string } | undefined {
  if (stmt.kind !== SyntaxKind.ExpressionStatement) return undefined;
  const expr = (stmt as ExpressionStatement).expression;
  if (expr.kind !== SyntaxKind.AssignmentExpression) return undefined;
  const assign = expr as AssignmentExpression;
  if (assign.operatorToken !== SyntaxKind.EqualsToken) return undefined;
  if (assign.right.kind !== SyntaxKind.Identifier) return undefined;
  const from = (assign.right as Identifier).text;
  const left = assign.left;
  if (left.kind === SyntaxKind.Identifier) return { field: (left as Identifier).text, from };
  if (
    left.kind === SyntaxKind.PropertyAccessExpression &&
    (left as PropertyAccessExpression).expression.kind === SyntaxKind.ThisExpression
  ) {
    return { field: (left as PropertyAccessExpression).name.text, from };
  }
  return undefined;
}

function isBooleanType(type: TypeNode): boolean {
  return (
    type.kind === SyntaxKind.PrimitiveType &&
    (type as PrimitiveType).keyword === SyntaxKind.BooleanKeyword
  );
}

// Offer to convert a POJO (final private fields + trivial getters + one trivial
// canonical constructor) into a record, renaming accessor call sites across the
// workspace. Strict: any member that does not fit this exact shape suppresses the
// action, so the rewrite is only offered when it is guaranteed safe.
function convertClassToRecord(
  program: Program,
  checker: Checker,
  sourceFile: SourceFile,
  start: number,
): CodeActionResult[] {
  if (sourceFile.parseDiagnostics.length > 0) return [];
  let node: Node | undefined = getNodeAtPosition(sourceFile, start);
  while (node && node.kind !== SyntaxKind.ClassDeclaration) node = node.parent;
  if (!node) return [];
  const cls = node as ClassDeclaration;
  const text = sourceFile.text;

  // Eligibility: not abstract, no superclass, a static/top-level type.
  if (hasKeyword(cls.modifiers, SyntaxKind.AbstractKeyword)) return [];
  if (cls.extendsType) return [];
  const isNested = cls.parent.kind !== SyntaxKind.SourceFile;
  if (isNested && !hasKeyword(cls.modifiers, SyntaxKind.StaticKeyword)) return [];
  if (!cls.symbol) return [];

  // Partition members into fields / getters / the sole constructor. Anything else
  // (static member, extra method, initializer block, nested type, ...) disqualifies.
  const fields: FieldDeclaration[] = [];
  const getters: { method: MethodDeclaration; field: string }[] = [];
  let ctor: ConstructorDeclaration | undefined;
  for (const member of cls.members) {
    switch (member.kind) {
      case SyntaxKind.FieldDeclaration: {
        const field = member as FieldDeclaration;
        if (!hasKeyword(field.modifiers, SyntaxKind.PrivateKeyword)) return [];
        if (!hasKeyword(field.modifiers, SyntaxKind.FinalKeyword)) return [];
        if (hasKeyword(field.modifiers, SyntaxKind.StaticKeyword)) return [];
        if (hasAnnotation(field.modifiers)) return [];
        if (field.declarators.length !== 1) return [];
        if (field.declarators[0]!.initializer) return [];
        fields.push(field);
        break;
      }
      case SyntaxKind.MethodDeclaration: {
        const method = member as MethodDeclaration;
        if (hasKeyword(method.modifiers, SyntaxKind.StaticKeyword)) return [];
        const field = getterFieldName(method);
        if (field === undefined) return [];
        getters.push({ method, field });
        break;
      }
      case SyntaxKind.ConstructorDeclaration: {
        if (ctor) return []; // more than one constructor
        ctor = member as ConstructorDeclaration;
        break;
      }
      default:
        return []; // any other member kind is unhandled
    }
  }
  if (!ctor || ctor.throws?.length) return [];

  const fieldNames = fields.map(f => f.declarators[0]!.name.text);
  const typeText = (t: Node) => text.slice(skipTrivia(text, t.pos), t.end);

  // Constructor parameters must equal the fields in declaration order (same type
  // text, same name), so the record's canonical constructor keeps `new C(...)`
  // calls valid without rewriting them.
  if (ctor.parameters.length !== fields.length) return [];
  for (let i = 0; i < fields.length; i++) {
    const p = ctor.parameters[i]!;
    if (p.isVarArgs || !p.name) return [];
    if (p.name.text !== fieldNames[i]) return [];
    if (typeText(p.type) !== typeText(fields[i]!.type)) return [];
  }
  // ... and its body must assign every field exactly once from its own parameter.
  const body = ctor.body as Block;
  if (body.statements.length !== fields.length) return [];
  const assigned = new Set<string>();
  for (const stmt of body.statements) {
    const a = ctorAssignment(stmt);
    if (!a || !fieldNames.includes(a.field) || a.from !== a.field || assigned.has(a.field)) {
      return [];
    }
    assigned.add(a.field);
  }

  // Every getter must map to a declared field and be named getX / isX (isX only
  // for a boolean field).
  for (const { method, field } of getters) {
    const idx = fieldNames.indexOf(field);
    if (idx < 0) return [];
    const name = method.name.text;
    const getName = `get${capitalize(field)}`;
    const isName = `is${capitalize(field)}`;
    const ok = name === getName || (name === isName && isBooleanType(fields[idx]!.type));
    if (!ok) return [];
  }

  // Records are implicitly final: bail if any class in the program extends this one.
  for (const uri of program.getAllUris()) {
    const other = program.getSourceFile(uri);
    if (!other) continue;
    let extended = false;
    forEachDescendant(other, n => {
      if (n.kind !== SyntaxKind.ClassDeclaration) return;
      const ext = (n as ClassDeclaration).extendsType;
      if (!ext || ext.kind !== SyntaxKind.TypeReference) return;
      const tn = (ext as TypeReference).typeName;
      const id = tn.kind === SyntaxKind.Identifier ? tn : (tn as QualifiedName).right;
      if (checker.resolveName(id) === cls.symbol) extended = true;
    });
    if (extended) return [];
  }

  // Build the record header, preserving leading modifiers/annotations by starting
  // the replacement at the `class` keyword.
  const lastMod = cls.modifiers?.[cls.modifiers.length - 1];
  const classKeywordPos = lastMod ? skipTrivia(text, lastMod.end) : skipTrivia(text, cls.pos);
  const typeParams = cls.typeParameters?.length
    ? `<${cls.typeParameters.map(typeText).join(", ")}>`
    : "";
  const components = fields
    .map(f => `${typeText(f.type)} ${f.declarators[0]!.name.text}`)
    .join(", ");
  const impls = cls.implementsTypes?.length
    ? ` implements ${cls.implementsTypes.map(typeText).join(", ")}`
    : "";
  const header = `record ${cls.name.text}${typeParams}(${components})${impls} {\n}`;
  const changes: TextChange[] = [{ start: classKeywordPos, end: cls.end, newText: header }];
  const additionalEdits: Record<string, TextChange[]> = {};

  // Rename accessor call sites getX()/isX() -> x() everywhere, skipping references
  // inside this class (declarations, which are being deleted).
  for (const { method, field } of getters) {
    if ((method.symbol?.declarations?.length ?? 0) !== 1) return [];
    for (const ref of findReferences(method.symbol!, program, checker.resolveName)) {
      const refFile = getSourceFileOfNode(ref);
      const inThisClass =
        refFile.fileName === sourceFile.fileName && ref.pos >= cls.pos && ref.end <= cls.end;
      if (inThisClass) continue;
      const edit: TextChange = {
        start: skipTrivia(refFile.text, ref.pos),
        end: ref.end,
        newText: field,
      };
      if (refFile.fileName === sourceFile.fileName) changes.push(edit);
      else (additionalEdits[refFile.fileName] ??= []).push(edit);
    }
  }

  const result: CodeActionResult = {
    title: "Convert class to record",
    kind: "refactor.rewrite",
    changes,
  };
  return Object.keys(additionalEdits).length > 0 ? [{ ...result, additionalEdits }] : [result];
}

// --- use 'var' for a local variable declaration (SE10) -----------------------

// Initializer kinds whose type is obvious from the RHS, so replacing the written
// type with `var` neither hides it from a reader nor changes it: these are
// standalone (non-poly) expressions, so the inferred type equals the written one.
const VAR_OBVIOUS_INITIALIZERS = new Set<SyntaxKind>([
  SyntaxKind.ObjectCreationExpression,
  SyntaxKind.ArrayCreationExpression,
  SyntaxKind.CastExpression,
  SyntaxKind.NumericLiteral,
  SyntaxKind.StringLiteral,
  SyntaxKind.TextBlockLiteral,
  SyntaxKind.CharacterLiteral,
]);

function convertToVar(sourceFile: SourceFile, start: number): CodeActionResult[] {
  let node: Node | undefined = getNodeAtPosition(sourceFile, start);
  while (node && node.kind !== SyntaxKind.LocalVariableDeclarationStatement) node = node.parent;
  if (!node) return [];
  const decl = node as LocalVariableDeclarationStatement;
  if (decl.type.kind === SyntaxKind.VarType) return []; // already `var`
  if (decl.declarators.length !== 1) return [];
  const initializer = decl.declarators[0]!.initializer;
  if (!initializer || !VAR_OBVIOUS_INITIALIZERS.has(initializer.kind)) return [];
  // `var m = new HashMap<>()` is a compile error: bail on a diamond `new`.
  if (initializer.kind === SyntaxKind.ObjectCreationExpression) {
    const t = (initializer as ObjectCreationExpression).type;
    if (t.kind === SyntaxKind.TypeReference && (t as TypeReference).typeArguments?.length === 0) {
      return [];
    }
  }
  const at = skipTrivia(sourceFile.text, decl.type.pos);
  return [
    {
      title: "Use 'var' for local variable",
      kind: "refactor.rewrite",
      changes: [{ start: at, end: decl.type.end, newText: "var" }],
    },
  ];
}

// --- convert an anonymous class to a lambda (SE8) ----------------------------

// The java.lang.Object public methods that JLS 9.8 says do NOT count toward a
// functional interface's single abstract method, keyed by name and arity.
function isObjectMethod(decl: MethodDeclaration): boolean {
  const name = decl.name.text;
  const arity = decl.parameters.length;
  return (
    (name === "equals" && arity === 1) ||
    (name === "hashCode" && arity === 0) ||
    (name === "toString" && arity === 0)
  );
}

// A method is the abstract kind that a lambda implements only when it has no body
// and is not a default/static/private interface method.
function isAbstractInterfaceMethod(decl: MethodDeclaration): boolean {
  if (decl.body) return false;
  return !(decl.modifiers ?? []).some(
    m =>
      m.kind === SyntaxKind.DefaultKeyword ||
      m.kind === SyntaxKind.StaticKeyword ||
      m.kind === SyntaxKind.PrivateKeyword,
  );
}

// The single abstract method of a functional interface (its SAM), searched
// through inherited interfaces, or undefined when the type is not a genuine
// functional interface (zero or more than one abstract method). Unlike the
// checker's functionalMethod, this counts and excludes default/static/private
// and java.lang.Object methods, so it is a correct SAM test.
function functionalInterfaceSam(
  typeSymbol: Symbol,
  program: Program,
): MethodDeclaration | undefined {
  const abstracts = new Map<string, MethodDeclaration>(); // name/arity -> decl (dedup overrides)
  const seen = new Set<Symbol>();
  const collect = (sym: Symbol): void => {
    if (seen.has(sym)) return;
    seen.add(sym);
    for (const member of sym.members?.values() ?? []) {
      if (!(member.flags & SymbolFlags.Method)) continue;
      const decl = member.declarations?.find(d => d.kind === SyntaxKind.MethodDeclaration) as
        | MethodDeclaration
        | undefined;
      if (!decl || !isAbstractInterfaceMethod(decl) || isObjectMethod(decl)) continue;
      abstracts.set(`${decl.name.text}/${decl.parameters.length}`, decl);
    }
    for (const superSymbol of getDirectSuperTypeSymbols(sym, program)) collect(superSymbol);
  };
  collect(typeSymbol);
  return abstracts.size === 1 ? [...abstracts.values()][0] : undefined;
}

// Offer to convert an anonymous class implementing a functional interface into a
// lambda. Strict: exactly one method whose body does not reference the anonymous
// instance (this/super rebind in a lambda), a resolvable functional interface,
// and a matching SAM - so the rewrite is only offered when it is guaranteed safe.
function convertAnonymousClassToLambda(
  program: Program,
  sourceFile: SourceFile,
  start: number,
): CodeActionResult[] {
  if (sourceFile.parseDiagnostics.length > 0) return [];
  let node: Node | undefined = getNodeAtPosition(sourceFile, start);
  while (node && node.kind !== SyntaxKind.ObjectCreationExpression) node = node.parent;
  if (!node) return [];
  const oce = node as ObjectCreationExpression;
  // An interface instantiation takes no constructor arguments and has a body.
  if (!oce.classBody || oce.classBody.length !== 1 || oce.arguments.length > 0) return [];
  const member = oce.classBody[0]!;
  if (member.kind !== SyntaxKind.MethodDeclaration) return [];
  const method = member as MethodDeclaration;
  if (!method.body) return [];

  if (oce.type.kind !== SyntaxKind.TypeReference) return [];
  const typeSymbol = resolveTypeEntityName((oce.type as TypeReference).typeName, oce, program);
  if (!typeSymbol || !(typeSymbol.flags & SymbolFlags.Interface)) return [];
  const sam = functionalInterfaceSam(typeSymbol, program);
  if (!sam || sam.name.text !== method.name.text) return [];
  if (sam.parameters.length !== method.parameters.length) return [];

  // Lambda parameters keep only their names (legal against the known target type).
  const params: string[] = [];
  for (const p of method.parameters) {
    if (p.isReceiver || !p.name) return [];
    params.push(p.name.text);
  }

  // Bail if the body references the anonymous instance: `this`/`super` would
  // rebind to the enclosing instance in a lambda, changing semantics.
  let referencesInstance = false;
  forEachDescendant(method.body, n => {
    if (n.kind === SyntaxKind.ThisExpression || n.kind === SyntaxKind.SuperExpression) {
      referencesInstance = true;
    }
  });
  if (referencesInstance) return [];

  const text = sourceFile.text;
  const bodyText = text.slice(skipTrivia(text, method.body.pos), method.body.end);
  const from = skipTrivia(text, oce.pos);
  return [
    {
      title: "Convert anonymous class to lambda",
      kind: "refactor.rewrite",
      changes: [{ start: from, end: oce.end, newText: `(${params.join(", ")}) -> ${bodyText}` }],
    },
  ];
}

// --- convert instanceof + cast to a pattern binding (SE16) -------------------

// Offer to fold `if (o instanceof T) { T t = (T) o; ... }` into a pattern
// `if (o instanceof T t) { ... }`, deleting the now-redundant cast declaration.
// Strict: the instanceof must be the whole `if` condition (so the binding is in
// scope for the block) and the first statement must be exactly that cast, so the
// rewrite is only offered when it is guaranteed safe.
function convertInstanceofToPattern(sourceFile: SourceFile, start: number): CodeActionResult[] {
  if (sourceFile.parseDiagnostics.length > 0) return [];
  let node: Node | undefined = getNodeAtPosition(sourceFile, start);
  while (node && node.kind !== SyntaxKind.InstanceofExpression) node = node.parent;
  if (!node) return [];
  const instanceOf = node as InstanceofExpression;
  // Must be a plain type test that is not already a pattern.
  if (!instanceOf.type || instanceOf.name || instanceOf.pattern) return [];

  // It must be exactly the `if` condition (not negated, not a sub-term of &&/||),
  // which keeps the pattern variable's scope trivially correct.
  const parent = instanceOf.parent;
  if (parent.kind !== SyntaxKind.IfStatement) return [];
  const ifStmt = parent as IfStatement;
  if (ifStmt.condition !== instanceOf) return [];
  if (ifStmt.thenStatement.kind !== SyntaxKind.Block) return [];
  const block = ifStmt.thenStatement as Block;
  const first = block.statements[0];
  if (!first || first.kind !== SyntaxKind.LocalVariableDeclarationStatement) return [];
  const decl = first as LocalVariableDeclarationStatement;
  if (decl.modifiers?.length || decl.declarators.length !== 1) return [];
  const declarator = decl.declarators[0]!;
  if (declarator.arrayRankAfterName || !declarator.initializer) return [];
  if (declarator.initializer.kind !== SyntaxKind.CastExpression) return [];
  const cast = declarator.initializer as CastExpression;
  if (cast.bounds?.length) return []; // intersection cast: not a simple binding

  const text = sourceFile.text;
  const span = (n: Node) => text.slice(skipTrivia(text, n.pos), n.end);
  // The cast must recover exactly the tested type from the tested operand.
  if (span(cast.type) !== span(instanceOf.type)) return [];
  if (span(cast.expression) !== span(instanceOf.expression)) return [];

  const name = declarator.name.text;
  // Insert ` name` after the tested type, and delete the whole cast-decl line
  // (indentation through the trailing newline). The binding keeps the local's
  // name, so every later use stays valid without a rename.
  const lineStart = text.lastIndexOf("\n", skipTrivia(text, decl.pos) - 1) + 1;
  const afterNewline = text.indexOf("\n", decl.end);
  const lineEnd = afterNewline < 0 ? text.length : afterNewline + 1;
  return [
    {
      title: "Replace cast with pattern binding",
      kind: "quickfix",
      changes: [
        { start: instanceOf.type.end, end: instanceOf.type.end, newText: ` ${name}` },
        { start: lineStart, end: lineEnd, newText: "" },
      ],
    },
  ];
}

// --- use the diamond operator (SE7) ------------------------------------------

// The explicit type arguments on a generic type reference as source text
// (`<String, Integer>`), or undefined when there are none.
function typeArgumentsText(sourceFile: SourceFile, type: TypeNode): string | undefined {
  if (type.kind !== SyntaxKind.TypeReference) return undefined;
  const ref = type as TypeReference;
  if (!ref.typeArguments?.length) return undefined;
  return sourceFile.text.slice(ref.typeName.end, ref.end);
}

// Offer to drop redundant type arguments on a `new` whose type is fixed by the
// declared type: `List<String> xs = new ArrayList<String>()` -> `... <>()`. Only
// when the RHS arguments equal the LHS arguments, so `<>` infers the same type.
function convertToDiamond(sourceFile: SourceFile, start: number): CodeActionResult[] {
  let node: Node | undefined = getNodeAtPosition(sourceFile, start);
  while (
    node &&
    node.kind !== SyntaxKind.LocalVariableDeclarationStatement &&
    node.kind !== SyntaxKind.FieldDeclaration
  ) {
    node = node.parent;
  }
  if (!node) return [];
  const decl = node as LocalVariableDeclarationStatement | FieldDeclaration;
  if (decl.declarators.length !== 1) return [];
  const lhsArgs = typeArgumentsText(sourceFile, decl.type);
  if (lhsArgs === undefined) return []; // LHS is not an explicit generic type (e.g. var)

  const initializer = decl.declarators[0]!.initializer;
  if (!initializer || initializer.kind !== SyntaxKind.ObjectCreationExpression) return [];
  const oce = initializer as ObjectCreationExpression;
  if (oce.classBody) return []; // anonymous-class diamond is SE9: stay conservative
  if (oce.type.kind !== SyntaxKind.TypeReference) return [];
  const rhs = oce.type as TypeReference;
  const rhsArgs = typeArgumentsText(sourceFile, rhs);
  if (rhsArgs === undefined || rhsArgs !== lhsArgs) return []; // already <> or a type change

  return [
    {
      title: "Use diamond operator",
      kind: "refactor.rewrite",
      changes: [{ start: rhs.typeName.end, end: rhs.end, newText: "<>" }],
    },
  ];
}

// --- convert a string accumulation to StringBuilder --------------------------

const LOOP_KINDS = new Set<SyntaxKind>([
  SyntaxKind.ForStatement,
  SyntaxKind.ForEachStatement,
  SyntaxKind.WhileStatement,
  SyntaxKind.DoStatement,
]);

function isInsideLoop(node: Node): boolean {
  for (let n: Node | undefined = node.parent; n; n = n.parent) {
    if (LOOP_KINDS.has(n.kind)) return true;
  }
  return false;
}

function isStringType(type: TypeNode): boolean {
  if (type.kind !== SyntaxKind.TypeReference) return false;
  const name = entityNameToString((type as TypeReference).typeName);
  return name === "String" || name === "java.lang.String";
}

// Offer to convert `String s = ""; ... s += x; ...` (with an accumulation inside
// a loop) into a StringBuilder: `s += x` becomes `s.append(x)` and every read of
// `s` becomes `s.toString()`. Strict: every use of `s` must be either a plain
// `s += expr` statement or a read, so the type change is always safe. Anything
// else - a reset `s = ...`, an identity `==`, a `+=` used as a value or whose
// right side reads `s` - suppresses the action.
function convertToStringBuilder(
  program: Program,
  checker: Checker,
  sourceFile: SourceFile,
  start: number,
): CodeActionResult[] {
  let node: Node | undefined = getNodeAtPosition(sourceFile, start);
  while (node && node.kind !== SyntaxKind.LocalVariableDeclarationStatement) node = node.parent;
  if (!node) return [];
  const decl = node as LocalVariableDeclarationStatement;
  if (decl.declarators.length !== 1 || !isStringType(decl.type)) return [];
  const declarator = decl.declarators[0]!;
  const init = declarator.initializer;
  // Empty-string init: an empty StringBuilder. (A `""` literal only assigns to
  // java.lang.String, so this also proves the declared type.)
  if (!init || init.kind !== SyntaxKind.StringLiteral || (init as LiteralExpression).value !== "") {
    return [];
  }
  const symbol = checker.resolveName(declarator.name);
  if (!symbol || !(symbol.flags & SymbolFlags.LocalVariable)) return [];

  const refs = findReferences(symbol, program, checker.resolveName).filter(
    n => n !== declarator.name,
  );
  const text = sourceFile.text;

  const accumulations: AssignmentExpression[] = [];
  const reads: Node[] = [];
  for (const ref of refs) {
    const parent = ref.parent;
    if (
      parent.kind === SyntaxKind.AssignmentExpression &&
      (parent as AssignmentExpression).left === ref
    ) {
      const assign = parent as AssignmentExpression;
      if (assign.operatorToken !== SyntaxKind.PlusEqualsToken) return []; // reset/other assign
      if (assign.parent.kind !== SyntaxKind.ExpressionStatement) return []; // += used as a value
      // The appended expression must not itself read `s` (avoids nested rewrites).
      if (refs.some(r => r !== ref && r.pos >= assign.right.pos && r.end <= assign.right.end)) {
        return [];
      }
      accumulations.push(assign);
      continue;
    }
    if (parent.kind === SyntaxKind.BinaryExpression) {
      const op = (parent as BinaryExpression).operatorToken;
      if (op === SyntaxKind.EqualsEqualsToken || op === SyntaxKind.ExclamationEqualsToken)
        return [];
    }
    reads.push(ref);
  }
  if (!accumulations.some(isInsideLoop)) return []; // the whole point is a loop

  const name = declarator.name.text;
  const changes: TextChange[] = [
    { start: skipTrivia(text, decl.type.pos), end: decl.type.end, newText: "StringBuilder" },
    { start: skipTrivia(text, init.pos), end: init.end, newText: "new StringBuilder()" },
  ];
  for (const assign of accumulations) {
    const rhs = text.slice(skipTrivia(text, assign.right.pos), assign.right.end);
    changes.push({
      start: skipTrivia(text, assign.pos),
      end: assign.end,
      newText: `${name}.append(${rhs})`,
    });
  }
  for (const ref of reads) {
    changes.push({ start: skipTrivia(text, ref.pos), end: ref.end, newText: `${name}.toString()` });
  }
  return [{ title: "Convert to StringBuilder", kind: "refactor.rewrite", changes }];
}

// --- convert a colon switch to an arrow switch (SE14) ------------------------

function terminatesClause(stmt: Statement): boolean {
  switch (stmt.kind) {
    case SyntaxKind.BreakStatement:
    case SyntaxKind.ContinueStatement:
    case SyntaxKind.ReturnStatement:
    case SyntaxKind.ThrowStatement:
      return true;
    default:
      return false;
  }
}

function isUnlabeledBreak(stmt: Statement): boolean {
  return stmt.kind === SyntaxKind.BreakStatement && !(stmt as { label?: Node }).label;
}

// True if `stmt` contains an unlabeled `break` that targets the enclosing switch
// (i.e. not one captured by a nested loop or switch). Such a break has no arrow
// equivalent, so its presence suppresses the rewrite.
function hasSwitchBreak(stmt: Node): boolean {
  let found = false;
  const visit = (n: Node): void => {
    if (found) return;
    if (isUnlabeledBreak(n as Statement)) {
      found = true;
      return;
    }
    if (
      LOOP_KINDS.has(n.kind) ||
      n.kind === SyntaxKind.SwitchStatement ||
      n.kind === SyntaxKind.SwitchExpression
    ) {
      return; // this construct captures its own breaks
    }
    forEachChild(n, c => {
      visit(c);
      return undefined;
    });
  };
  visit(stmt);
  return found;
}

// Offer to rewrite a classic colon `switch` into the SE14 arrow form:
// `case A: foo(); break;` -> `case A -> foo();`, with fall-through-only labels
// merged (`case A: case B:` -> `case A, B ->`). Bails on anything the arrow form
// cannot express: real fall-through (a labeled clause with code that reaches the
// next), a switch-targeting break inside a body, a `default` that falls through,
// or an SE21 `when` guard.
function convertToArrowSwitch(sourceFile: SourceFile, start: number): CodeActionResult[] {
  if (sourceFile.parseDiagnostics.length > 0) return [];
  let node: Node | undefined = getNodeAtPosition(sourceFile, start);
  while (node && node.kind !== SyntaxKind.SwitchStatement) node = node.parent;
  if (!node) return [];
  const sw = node as SwitchStatement;
  const clauses = sw.clauses;
  if (clauses.length === 0) return [];
  if (clauses.some(c => c.isArrow)) return []; // already arrow (or mixed): leave alone

  const text = sourceFile.text;
  const span = (n: Node) => text.slice(skipTrivia(text, n.pos), n.end);

  type Group = { label: string; body: Statement[] };
  const groups: Group[] = [];
  let pending: string[] = []; // case labels stacked by empty fall-through clauses
  for (let i = 0; i < clauses.length; i++) {
    const c = clauses[i]!;
    const isLast = i === clauses.length - 1;
    if (c.guard) return []; // SE21 guarded pattern: out of scope
    const labelText = c.isDefault ? "default" : (c.labels ?? []).map(span).join(", ");

    if (c.statements.length === 0) {
      if (isLast) {
        groups.push({
          label: c.isDefault ? "default" : `case ${[...pending, labelText].join(", ")}`,
          body: [],
        });
        pending = [];
      } else {
        if (c.isDefault) return []; // default cannot fall through in the arrow form
        pending.push(labelText);
      }
      continue;
    }

    const stmts = c.statements;
    const last = stmts[stmts.length - 1]!;
    if (!isLast && !terminatesClause(last)) return []; // real fall-through with code
    const body = isUnlabeledBreak(last) ? stmts.slice(0, -1) : stmts.slice();
    if (body.some(hasSwitchBreak)) return []; // a break that targets this switch
    if (c.isDefault) {
      if (pending.length) return []; // a case fell into default
      groups.push({ label: "default", body });
    } else {
      groups.push({ label: `case ${[...pending, labelText].join(", ")}`, body });
    }
    pending = [];
  }
  if (pending.length) return []; // labels with no body (defensive)

  const switchStart = skipTrivia(text, sw.pos);
  const indent = indentationAt(text, switchStart);
  const caseIndent = `${indent}    `;
  const renderBody = (body: Statement[]): string => {
    if (body.length === 0) return "{}";
    const only = body[0]!;
    if (
      body.length === 1 &&
      (only.kind === SyntaxKind.ExpressionStatement || only.kind === SyntaxKind.ThrowStatement)
    ) {
      return span(only);
    }
    // Block form: reindent the original statement lines under the arrow.
    const bodyStart = skipTrivia(text, only.pos);
    const originalIndent = indentationAt(text, bodyStart);
    const inner = `${caseIndent}    `;
    const raw = text.slice(bodyStart, body[body.length - 1]!.end);
    const reindented = raw
      .split("\n")
      .map((line, idx) =>
        idx === 0
          ? inner + line
          : inner +
            (line.startsWith(originalIndent)
              ? line.slice(originalIndent.length)
              : line.replace(/^\s+/, "")),
      )
      .join("\n");
    return `{\n${reindented}\n${caseIndent}}`;
  };

  const lines = [`switch (${span(sw.expression)}) {`];
  for (const g of groups) lines.push(`${caseIndent}${g.label} -> ${renderBody(g.body)}`);
  lines.push(`${indent}}`);

  return [
    {
      title: "Convert to arrow switch",
      kind: "refactor.rewrite",
      changes: [{ start: switchStart, end: sw.end, newText: lines.join("\n") }],
    },
  ];
}

// --- merge catch clauses with identical bodies into a multi-catch (SE7) -------

function catchTypeSymbol(clause: CatchClause, program: Program): Symbol | undefined {
  const t = clause.catchTypes[0];
  if (!t || t.kind !== SyntaxKind.TypeReference) return undefined;
  return resolveTypeEntityName((t as TypeReference).typeName, clause, program);
}

// source's class symbol is a subtype of target's (walks extends/implements).
function isCatchSubtype(sourceSym: Symbol, targetSym: Symbol, program: Program): boolean {
  const seen = new Set<Symbol>();
  const queue: Symbol[] = [sourceSym];
  while (queue.length > 0) {
    const cur = queue.shift()!;
    if (cur === targetSym) return true;
    if (seen.has(cur)) continue;
    seen.add(cur);
    queue.push(...getDirectSuperTypeSymbols(cur, program));
  }
  return false;
}

// Merge each maximal run of adjacent catch clauses that share the same parameter
// (name + modifiers) and byte-identical body into one `catch (A | B e)`. A union
// alternative may not be a subtype of another (JLS 14.20), so the caught types
// must resolve and be pairwise unrelated - otherwise that run is skipped.
function mergeCatchClauses(
  program: Program,
  sourceFile: SourceFile,
  start: number,
): CodeActionResult[] {
  if (sourceFile.parseDiagnostics.length > 0) return [];
  let node: Node | undefined = getNodeAtPosition(sourceFile, start);
  while (node && node.kind !== SyntaxKind.TryStatement) node = node.parent;
  if (!node) return [];
  const clauses = (node as TryStatement).catchClauses;
  if (clauses.length < 2) return [];

  const text = sourceFile.text;
  const span = (n: Node) => text.slice(skipTrivia(text, n.pos), n.end);
  const modText = (c: CatchClause) =>
    c.modifiers?.length
      ? text.slice(skipTrivia(text, c.modifiers[0]!.pos), c.modifiers[c.modifiers.length - 1]!.end)
      : "";
  const mergeable = (a: CatchClause, b: CatchClause) =>
    a.catchTypes.length === 1 &&
    b.catchTypes.length === 1 &&
    a.name.text === b.name.text &&
    modText(a) === modText(b) &&
    span(a.block) === span(b.block);

  const actions: CodeActionResult[] = [];
  let i = 0;
  while (i < clauses.length) {
    let j = i + 1;
    while (j < clauses.length && mergeable(clauses[j - 1]!, clauses[j]!)) j++;
    if (j - i >= 2) {
      const run = clauses.slice(i, j);
      const syms = run.map(c => catchTypeSymbol(c, program));
      const related = (a: number, b: number) =>
        isCatchSubtype(syms[a]!, syms[b]!, program) || isCatchSubtype(syms[b]!, syms[a]!, program);
      let ok = syms.every(Boolean);
      for (let a = 0; ok && a < syms.length; a++)
        for (let b = a + 1; ok && b < syms.length; b++) if (related(a, b)) ok = false;
      if (ok) {
        const first = run[0]!;
        const prefix = modText(first) ? `${modText(first)} ` : "";
        const types = run.map(c => span(c.catchTypes[0]!)).join(" | ");
        actions.push({
          title: "Merge catch clauses",
          kind: "refactor.rewrite",
          changes: [
            {
              start: skipTrivia(text, first.pos),
              end: run[run.length - 1]!.end,
              newText: `catch (${prefix}${types} ${first.name.text}) ${span(first.block)}`,
            },
          ],
        });
      }
    }
    i = j;
  }
  return actions;
}

export function getCodeActions(
  program: Program,
  checker: Checker,
  sourceFile: SourceFile,
  start: number,
  end: number,
  features: LanguageFeatures,
): CodeActionResult[] {
  return [
    ...addMissingImport(program, checker, sourceFile, start),
    ...organizeImports(sourceFile),
    // extract-local emits a `var` declaration (SE10).
    ...(features.supportsVar ? extractLocalVariable(sourceFile, start, end) : []),
    ...inlineLocalVariable(program, checker, sourceFile, start),
    ...removeUnusedParameter(program, checker, sourceFile, start),
    ...removeUnusedImport(sourceFile, start, end),
    ...removeRedundantOverride(checker, sourceFile, start),
    ...makeFieldFinal(checker, sourceFile, start),
    ...replaceOptionalIfPresentWithNullCheck(checker, sourceFile, start),
    ...replaceOptionalOfNull(checker, sourceFile, start),
    ...replaceBooleanComparison(checker, sourceFile, start),
    ...replaceIfElseReturningBoolean(checker, sourceFile, start),
    ...replaceTernaryBooleanLiterals(checker, sourceFile, start),
    ...replaceCollapsibleIf(checker, sourceFile, start),
    ...replaceCountComparedToZero(checker, sourceFile, start),
    ...replaceStringEquality(checker, sourceFile, start),
    ...replaceBoxedEquality(checker, sourceFile, start),
    ...replaceBoxingConstructor(checker, sourceFile, start),
    ...replaceIndexOfComparedToNegativeOne(checker, sourceFile, start),
    ...removeRedundantNewString(checker, sourceFile, start),
    ...replaceEqualsEmptyString(checker, sourceFile, start),
    ...(features.supportsRecord ? convertClassToRecord(program, checker, sourceFile, start) : []),
    ...(features.supportsVar ? convertToVar(sourceFile, start) : []),
    ...(features.supportsLambda ? convertAnonymousClassToLambda(program, sourceFile, start) : []),
    ...(features.supportsInstanceofPattern ? convertInstanceofToPattern(sourceFile, start) : []),
    ...(features.supportsDiamond ? convertToDiamond(sourceFile, start) : []),
    ...(features.supportsArrowSwitch ? convertToArrowSwitch(sourceFile, start) : []),
    ...(features.supportsMultiCatch ? mergeCatchClauses(program, sourceFile, start) : []),
    ...convertToStringBuilder(program, checker, sourceFile, start),
  ];
}
