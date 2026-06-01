// Source-file driver for the bytecode backend. Walks a parsed source file and
// emits one .class per top-level class via the class-file writer in bytecode.ts.
// Higher-level, source-level transformations (e.g. constant folding) belong here
// rather than in the low-level instruction emitter.

import { type EmittedClass, emitClass } from "./bytecode.ts";
import { forEachChild } from "./parser.ts";
import type { Program } from "./program.ts";
import type { Checker } from "./checker.ts";
import { type ClassDeclaration, type Node, type SourceFile, SyntaxKind } from "./types.ts";

export type { EmittedClass } from "./bytecode.ts";

/**
 * Emit a .class file for every class declaration in a source file: top-level
 * classes and their nested classes (each gets its own class file named
 * Outer$Inner, the binary name the class-file writer derives from the symbol).
 */
export function emitSourceFile(
  sourceFile: SourceFile,
  program: Program,
  checker: Checker,
): EmittedClass[] {
  const result: EmittedClass[] = [];
  const visit = (node: Node): void => {
    if (node.kind === SyntaxKind.ClassDeclaration) {
      result.push(emitClass(node as ClassDeclaration, program, checker));
    }
    forEachChild(node, child => {
      visit(child);
      return undefined;
    });
  };
  visit(sourceFile);
  return result;
}
