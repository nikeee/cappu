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

/** Emit a .class file for every top-level class declaration in a source file. */
export function emitSourceFile(
  sourceFile: SourceFile,
  program: Program,
  checker: Checker,
): EmittedClass[] {
  const result: EmittedClass[] = [];
  forEachChild(sourceFile, (node: Node) => {
    if (node.kind === SyntaxKind.ClassDeclaration) {
      result.push(emitClass(node as ClassDeclaration, program, checker));
    }
    return undefined;
  });
  return result;
}
