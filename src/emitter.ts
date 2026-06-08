// Source-file driver for the bytecode backend. Walks a parsed source file and
// emits one .class per top-level class via the class-file writer in bytecode.ts.
// Higher-level, source-level transformations (e.g. constant folding) belong here
// rather than in the low-level instruction emitter.

import {
  type EmittedClass,
  computeNestMembers,
  emitAnonymousClassIfPossible,
  emitClass,
  emitEnum,
  emitInterface,
  emitRecord,
} from "./bytecode.ts";
import { forEachChild } from "./parser.ts";
import type { Program } from "./program.ts";
import type { Checker } from "./checker.ts";
import {
  type ClassDeclaration,
  type EnumDeclaration,
  type InterfaceDeclaration,
  type Node,
  type ObjectCreationExpression,
  type RecordDeclaration,
  type SourceFile,
  SyntaxKind,
} from "./types.ts";

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
  // Nest grouping (host -> members) so each class gets NestHost / NestMembers,
  // letting nestmates share private access.
  const nest = computeNestMembers(sourceFile, program);
  const visit = (node: Node): void => {
    if (node.kind === SyntaxKind.ClassDeclaration) {
      const decl = node as ClassDeclaration;
      // Anonymous classes (new T(){...}) live as ObjectCreationExpression.classBody,
      // not ClassDeclaration; a ClassDeclaration with no name/symbol is skipped.
      if (decl.symbol || decl.name) result.push(emitClass(decl, program, checker, nest));
    } else if (node.kind === SyntaxKind.InterfaceDeclaration) {
      result.push(emitInterface(node as InterfaceDeclaration, program, checker, nest));
    } else if (node.kind === SyntaxKind.EnumDeclaration) {
      result.push(emitEnum(node as EnumDeclaration, program, checker, nest));
    } else if (node.kind === SyntaxKind.RecordDeclaration) {
      result.push(emitRecord(node as RecordDeclaration, program, checker, nest));
    } else if (node.kind === SyntaxKind.ObjectCreationExpression) {
      // Anonymous class (new T(){...}): emitted as its own Outer$N when supported.
      const anon = emitAnonymousClassIfPossible(
        node as ObjectCreationExpression,
        program,
        checker,
        nest,
      );
      if (anon) result.push(anon);
    }
    forEachChild(node, child => {
      visit(child);
      return undefined;
    });
  };
  visit(sourceFile);
  return result;
}
