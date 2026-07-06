// Refactor / quick-fix code actions. Each action is computed as plain text
// changes (offset ranges + replacement text) over a single file, so the logic is
// pure and testable; the server maps them to LSP WorkspaceEdits. Actions are
// offered for a [start, end) selection range in one source file.

import { type Checker, findUnusedImports } from "../compiler/checker.ts";
import { Diagnostics } from "../compiler/diagnostics.ts";
import { getIdentifierAtPosition, getNodeAtPosition } from "./nodeAtPosition.ts";
import { forEachChild } from "../compiler/parser.ts";
import type { Program } from "../compiler/program.ts";
import { findReferences, getSourceFileOfNode } from "../compiler/resolver.ts";
import {
  type Annotation,
  type AssignmentExpression,
  type Block,
  type CallExpression,
  type ClassDeclaration,
  type ConstructorDeclaration,
  type ExpressionStatement,
  type FieldDeclaration,
  type Identifier,
  type ImportDeclaration,
  type LocalVariableDeclarationStatement,
  type MethodDeclaration,
  type Node,
  type ObjectCreationExpression,
  type PrimitiveType,
  type PropertyAccessExpression,
  type QualifiedName,
  type ReturnStatement,
  type SourceFile,
  SymbolFlags,
  SyntaxKind,
  type ThisExpression,
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
  readonly supportsVar: boolean; // SE10
  readonly supportsLambda: boolean; // SE8
  readonly supportsRecord: boolean; // SE16
  readonly supportsInstanceofPattern: boolean; // SE16
}

// An unset release means the toolchain default (a modern JDK): everything on.
export function languageFeatures(release: number | undefined): LanguageFeatures {
  const at = (min: number) => release === undefined || release >= min;
  return {
    supportsVar: at(10),
    supportsLambda: at(8),
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
    ...(features.supportsRecord ? convertClassToRecord(program, checker, sourceFile, start) : []),
    ...(features.supportsVar ? convertToVar(sourceFile, start) : []),
  ];
}
