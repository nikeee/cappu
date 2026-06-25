// Flow-aware nullness narrowing (nikeee/cappu#25). A syntactic dominator walk - not
// a full control-flow graph - that figures out what the code has proven about a
// local/parameter at a use site, the analog of TypeScript's getTypeOfSymbolAtLocation.
// Covers the common Java idioms: if/else guards, early-exit (if (x==null) return),
// && / || short-circuit, ternary, instanceof, Objects.requireNonNull / assert, and
// reassignment. Loops' back-edges and branch-merge of reassignments are not modeled.

import {
  type BinaryExpression,
  type CallExpression,
  type ConditionalExpression,
  type Identifier,
  type IfStatement,
  type InstanceofExpression,
  type Node,
  type ParenthesizedExpression,
  type PrefixUnaryExpression,
  type PropertyAccessExpression,
  type Block,
  type AssertStatement,
  type ExpressionStatement,
  type WhileStatement,
  type ForStatement,
  type Symbol,
  SyntaxKind,
} from "./types.ts";
import { type Nullness } from "./nullness.ts";

/** Resolve a bare reference node to its symbol (the checker's resolveName). */
export type ResolveRef = (node: Node) => Symbol | undefined;
/** The provable nullness of a value expression (null literal, `new`, @NonNull, ...). */
export type ExprNullness = (node: Node) => Nullness | undefined;

/** What a boolean condition proves about `symbol` when it is true / when false. */
interface Facts {
  readonly whenTrue?: Nullness;
  readonly whenFalse?: Nullness;
}

const isNullLiteral = (n: Node): boolean => n.kind === SyntaxKind.NullKeyword;

// A bare reference (`x`) to the tracked symbol. Locals/params are referenced by a
// plain Identifier, so a qualified `this.x` (a field) is intentionally not matched.
function refersToSymbol(node: Node, symbol: Symbol, resolveRef: ResolveRef): boolean {
  return node.kind === SyntaxKind.Identifier && resolveRef(node as Identifier) === symbol;
}

// The simple (unqualified) name of a call's callee, for Objects.requireNonNull etc.
function calleeName(call: CallExpression): string | undefined {
  const callee = call.expression;
  if (callee.kind === SyntaxKind.Identifier) return (callee as Identifier).text;
  if (callee.kind === SyntaxKind.PropertyAccessExpression) {
    return (callee as PropertyAccessExpression).name.text;
  }
  return undefined;
}

function conditionImplies(cond: Node, symbol: Symbol, resolveRef: ResolveRef): Facts {
  switch (cond.kind) {
    case SyntaxKind.ParenthesizedExpression:
      return conditionImplies((cond as ParenthesizedExpression).expression, symbol, resolveRef);
    case SyntaxKind.PrefixUnaryExpression: {
      const u = cond as PrefixUnaryExpression;
      if (u.operator !== SyntaxKind.ExclamationToken) return {};
      const f = conditionImplies(u.operand, symbol, resolveRef);
      return { whenTrue: f.whenFalse, whenFalse: f.whenTrue };
    }
    case SyntaxKind.BinaryExpression: {
      const b = cond as BinaryExpression;
      const op = b.operatorToken;
      if (op === SyntaxKind.EqualsEqualsToken || op === SyntaxKind.ExclamationEqualsToken) {
        const isNullCheck =
          (refersToSymbol(b.left, symbol, resolveRef) && isNullLiteral(b.right)) ||
          (refersToSymbol(b.right, symbol, resolveRef) && isNullLiteral(b.left));
        if (!isNullCheck) return {};
        // `x == null`: true => null, false => non-null. `x != null`: the inverse.
        return op === SyntaxKind.EqualsEqualsToken
          ? { whenTrue: "nullable", whenFalse: "nonNull" }
          : { whenTrue: "nonNull", whenFalse: "nullable" };
      }
      if (op === SyntaxKind.AmpersandAmpersandToken) {
        const l = conditionImplies(b.left, symbol, resolveRef);
        const r = conditionImplies(b.right, symbol, resolveRef);
        return { whenTrue: l.whenTrue ?? r.whenTrue };
      }
      if (op === SyntaxKind.BarBarToken) {
        const l = conditionImplies(b.left, symbol, resolveRef);
        const r = conditionImplies(b.right, symbol, resolveRef);
        return { whenFalse: l.whenFalse ?? r.whenFalse };
      }
      return {};
    }
    case SyntaxKind.InstanceofExpression: {
      const io = cond as InstanceofExpression;
      // `x instanceof T` is false for null, so true proves x non-null.
      return refersToSymbol(io.expression, symbol, resolveRef) ? { whenTrue: "nonNull" } : {};
    }
    case SyntaxKind.CallExpression: {
      const call = cond as CallExpression;
      const arg0 = call.arguments[0];
      if (!arg0 || !refersToSymbol(arg0, symbol, resolveRef)) return {};
      const name = calleeName(call);
      if (name === "nonNull") return { whenTrue: "nonNull" }; // Objects.nonNull(x)
      if (name === "isNull") return { whenTrue: "nullable", whenFalse: "nonNull" }; // Objects.isNull(x)
      return {};
    }
    default:
      return {};
  }
}

// A statement definitely completes abruptly (so code after it is unreachable):
// return/throw/break/continue, or a block whose last statement does.
function definitelyExits(stmt: Node | undefined): boolean {
  if (!stmt) return false;
  switch (stmt.kind) {
    case SyntaxKind.ReturnStatement:
    case SyntaxKind.ThrowStatement:
    case SyntaxKind.BreakStatement:
    case SyntaxKind.ContinueStatement:
      return true;
    case SyntaxKind.Block: {
      const s = (stmt as Block).statements;
      return s.length > 0 && definitelyExits(s[s.length - 1]);
    }
    default:
      return false;
  }
}

// `x = <expr>` as a statement, assigning the tracked symbol.
function assignedValue(stmt: Node, symbol: Symbol, resolveRef: ResolveRef): Node | undefined {
  if (stmt.kind !== SyntaxKind.ExpressionStatement) return undefined;
  const expr = (stmt as ExpressionStatement).expression as Node;
  if (expr.kind !== SyntaxKind.AssignmentExpression) return undefined;
  const a = expr as BinaryExpression;
  if (a.operatorToken !== SyntaxKind.EqualsToken || !refersToSymbol(a.left, symbol, resolveRef)) {
    return undefined;
  }
  return a.right;
}

// The statements of a branch (a block's, or the single statement itself).
function branchStatements(branch: Node): readonly Node[] {
  return branch.kind === SyntaxKind.Block ? (branch as Block).statements : [branch];
}

// Whether a branch contains a top-level assignment to the tracked symbol.
function branchAssignsSymbol(branch: Node, symbol: Symbol, resolveRef: ResolveRef): boolean {
  return branchStatements(branch).some(s => assignedValue(s, symbol, resolveRef));
}

// The nullness a branch leaves the symbol with when its last statement assigns it,
// else undefined (we only reason about a trailing assignment).
function branchTrailingNullness(
  branch: Node,
  symbol: Symbol,
  resolveRef: ResolveRef,
  exprNullness: ExprNullness,
): Nullness | undefined {
  const stmts = branchStatements(branch);
  const last = stmts[stmts.length - 1];
  const value = last && assignedValue(last, symbol, resolveRef);
  return value ? exprNullness(value) : undefined;
}

// A `return`/`undefined` from one preceding statement: "narrow" gives a proven
// nullness; "reset" means an assignment whose value we could not prove (the symbol's
// state is unknown from here, so earlier facts no longer apply); undefined = keep looking.
type StmtFact = { kind: "narrow"; nullness: Nullness } | { kind: "reset" } | undefined;

function precedingStatementFact(
  stmt: Node,
  symbol: Symbol,
  resolveRef: ResolveRef,
  exprNullness: ExprNullness,
): StmtFact {
  // x = <expr>;  -> the value's nullness; an assignment supersedes earlier facts.
  if (stmt.kind === SyntaxKind.ExpressionStatement) {
    const expr = (stmt as ExpressionStatement).expression as Node;
    if (expr.kind === SyntaxKind.AssignmentExpression) {
      const a = expr as BinaryExpression;
      if (
        a.operatorToken === SyntaxKind.EqualsToken &&
        refersToSymbol(a.left, symbol, resolveRef)
      ) {
        const n = exprNullness(a.right);
        return n ? { kind: "narrow", nullness: n } : { kind: "reset" };
      }
    }
    // Objects.requireNonNull(x[, ...]);  -> x is non-null afterwards.
    if (expr.kind === SyntaxKind.CallExpression) {
      const call = expr as CallExpression;
      const arg0 = call.arguments[0];
      if (
        calleeName(call) === "requireNonNull" &&
        arg0 &&
        refersToSymbol(arg0, symbol, resolveRef)
      ) {
        return { kind: "narrow", nullness: "nonNull" };
      }
    }
    return undefined;
  }
  // assert x != null;  -> x is non-null afterwards.
  if (stmt.kind === SyntaxKind.AssertStatement) {
    const f = conditionImplies((stmt as AssertStatement).condition, symbol, resolveRef).whenTrue;
    return f === "nonNull" ? { kind: "narrow", nullness: "nonNull" } : undefined;
  }
  if (stmt.kind === SyntaxKind.IfStatement) {
    const ifs = stmt as IfStatement;
    const facts = conditionImplies(ifs.condition, symbol, resolveRef);
    // Early-exit: if (COND) <abrupt>;  -> after the if, only the fall-through path
    // remains, so x has whatever COND-false proves (e.g. if (x==null) return; -> non-null).
    if (!ifs.elseStatement && definitelyExits(ifs.thenStatement) && facts.whenFalse) {
      return { kind: "narrow", nullness: facts.whenFalse };
    }
    // Branch-merge: if (x == null) x = <non-null>;  -> both the then-branch (just
    // assigned) and the fall-through (COND was false, so non-null) agree x is non-null.
    if (
      !ifs.elseStatement &&
      facts.whenTrue === "nullable" &&
      !definitelyExits(ifs.thenStatement) &&
      branchTrailingNullness(ifs.thenStatement, symbol, resolveRef, exprNullness) === "nonNull"
    ) {
      return { kind: "narrow", nullness: "nonNull" };
    }
    // Soundness: any other assignment to x inside a branch leaves its state
    // unprovable here, so earlier facts must not leak past this if.
    if (
      branchAssignsSymbol(ifs.thenStatement, symbol, resolveRef) ||
      (ifs.elseStatement && branchAssignsSymbol(ifs.elseStatement, symbol, resolveRef))
    ) {
      return { kind: "reset" };
    }
  }
  return undefined;
}

/**
 * The narrowed nullness of `symbol` at use site `use`, or undefined when nothing is
 * proven (the declared nullness then applies). Caller must only pass locals/params.
 */
export function narrowNullnessAt(
  use: Node,
  symbol: Symbol,
  resolveRef: ResolveRef,
  exprNullness: ExprNullness,
): Nullness | undefined {
  let node: Node = use;
  for (let parent = node.parent; parent; node = parent, parent = parent.parent) {
    // Preceding statements in an enclosing block (nearest first). An assignment or
    // guard here is checked before the enclosing condition, so a write between a
    // guard and the use correctly invalidates the guard.
    if (parent.kind === SyntaxKind.Block) {
      const stmts = (parent as Block).statements;
      const idx = stmts.indexOf(node as never);
      for (let i = idx - 1; i >= 0; i--) {
        const fact = precedingStatementFact(stmts[i]!, symbol, resolveRef, exprNullness);
        if (fact?.kind === "narrow") return fact.nullness;
        if (fact?.kind === "reset") return undefined;
      }
      continue;
    }
    // Conditional branch position: we are inside a branch whose guard proves a fact.
    if (parent.kind === SyntaxKind.IfStatement) {
      const ifs = parent as IfStatement;
      const facts = conditionImplies(ifs.condition, symbol, resolveRef);
      if (node === ifs.thenStatement && facts.whenTrue) return facts.whenTrue;
      if (node === ifs.elseStatement && facts.whenFalse) return facts.whenFalse;
    } else if (parent.kind === SyntaxKind.ConditionalExpression) {
      const c = parent as ConditionalExpression;
      const facts = conditionImplies(c.condition, symbol, resolveRef);
      if (node === c.whenTrue && facts.whenTrue) return facts.whenTrue;
      if (node === c.whenFalse && facts.whenFalse) return facts.whenFalse;
    } else if (parent.kind === SyntaxKind.BinaryExpression) {
      const b = parent as BinaryExpression;
      if (node === b.right) {
        if (b.operatorToken === SyntaxKind.AmpersandAmpersandToken) {
          const f = conditionImplies(b.left, symbol, resolveRef).whenTrue;
          if (f) return f;
        } else if (b.operatorToken === SyntaxKind.BarBarToken) {
          const f = conditionImplies(b.left, symbol, resolveRef).whenFalse;
          if (f) return f;
        }
      }
    } else if (parent.kind === SyntaxKind.WhileStatement) {
      // The body runs only when the condition held. (A do-while body runs once
      // before the test, so its condition is intentionally not consulted.)
      const w = parent as WhileStatement;
      if (node === w.statement) {
        const f = conditionImplies(w.condition, symbol, resolveRef).whenTrue;
        if (f) return f;
      }
    } else if (parent.kind === SyntaxKind.ForStatement) {
      const fs = parent as ForStatement;
      if (node === fs.statement && fs.condition) {
        const f = conditionImplies(fs.condition, symbol, resolveRef).whenTrue;
        if (f) return f;
      }
    }
  }
  return undefined;
}
