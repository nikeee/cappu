// Inlay hints: parameter names at call sites and inferred types for `var`.
// Offset-based so the unit tests stay position-free; the LSP server converts
// offsets to positions and applies the user's configuration.

import type { Checker } from "../compiler/checker.ts";
import { forEachChild } from "../compiler/parser.ts";
import {
  type CallExpression,
  type ForEachStatement,
  type LocalVariableDeclarationStatement,
  type Node,
  type Parameter,
  type SourceFile,
  SyntaxKind,
  type VariableDeclarator,
} from "../compiler/types.ts";

export interface InlayHintsSettings {
  /** Hints like `count:` before call arguments that are not plain variables. */
  parameterNames: boolean;
  /** Hints like `: String` after a `var` declaration's name. */
  varTypes: boolean;
}

export const DEFAULT_INLAY_HINTS: InlayHintsSettings = { parameterNames: true, varTypes: true };

export interface InlayHintEntry {
  readonly offset: number;
  readonly label: string;
  readonly kind: "parameter" | "type";
}

// A "self-explanatory" argument needs no name hint: a plain variable, `this`,
// or a field access whose final name already reads like the parameter.
function isSelfExplanatoryArgument(arg: Node): boolean {
  switch (arg.kind) {
    case SyntaxKind.Identifier:
    case SyntaxKind.ThisExpression:
    case SyntaxKind.PropertyAccessExpression:
      return true;
    default:
      return false;
  }
}

export function getInlayHints(
  checker: Checker,
  sourceFile: SourceFile,
  startOffset: number,
  endOffset: number,
  settings: InlayHintsSettings = DEFAULT_INLAY_HINTS,
): InlayHintEntry[] {
  const hints: InlayHintEntry[] = [];

  const collectCallHints = (call: CallExpression): void => {
    if (call.arguments.length === 0) return;
    const decl = checker.resolveCall(call);
    if (!decl) return;
    call.arguments.forEach((arg, i) => {
      if (arg.end < startOffset || arg.pos > endOffset) return;
      if (isSelfExplanatoryArgument(arg)) return;
      const param = decl.parameters[i] as Parameter | undefined;
      const name = param?.name?.text;
      if (!name) return;
      if (param.isVarArgs) {
        // Only the first argument of the variable-arity tail gets a `...name:`.
        if (i !== decl.parameters.length - 1) return;
        hints.push({ offset: skipToStart(arg), label: `...${name}:`, kind: "parameter" });
        return;
      }
      hints.push({ offset: skipToStart(arg), label: `${name}:`, kind: "parameter" });
    });
  };

  // `pos` includes leading trivia; the hint goes right before the first token.
  const skipToStart = (node: Node): number => {
    let pos = node.pos;
    const text = sourceFile.text;
    while (pos < node.end && /\s/.test(text[pos]!)) pos++;
    return pos;
  };

  const varTypeHint = (declarator: VariableDeclarator | Parameter): void => {
    const name = (declarator as { name?: { text: string; end: number } }).name;
    const symbol = (declarator as Node).symbol;
    if (!name || !symbol) return;
    if (name.end < startOffset || name.end > endOffset) return;
    const type = checker.typeStringOfSymbol(symbol);
    if (type === "<error>" || type === "var" || type === "") return;
    hints.push({ offset: name.end, label: `: ${type}`, kind: "type" });
  };

  const visit = (node: Node): void => {
    if (node.end < startOffset || node.pos > endOffset) return; // outside the range
    if (settings.parameterNames && node.kind === SyntaxKind.CallExpression) {
      collectCallHints(node as CallExpression);
    }
    if (settings.varTypes && node.kind === SyntaxKind.LocalVariableDeclarationStatement) {
      const s = node as LocalVariableDeclarationStatement;
      if (s.type.kind === SyntaxKind.VarType) {
        for (const d of s.declarators) varTypeHint(d as VariableDeclarator);
      }
    }
    if (settings.varTypes && node.kind === SyntaxKind.ForEachStatement) {
      const parameter = (node as ForEachStatement).parameter;
      if (parameter.type.kind === SyntaxKind.VarType) varTypeHint(parameter);
    }
    forEachChild(node, child => {
      visit(child);
      return undefined;
    });
  };
  visit(sourceFile);
  hints.sort((a, b) => a.offset - b.offset);
  return hints;
}
