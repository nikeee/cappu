// Source-file driver for the bytecode backend. Walks a parsed source file and
// emits one .class per top-level class via the class-file writer in bytecode.ts.
// Higher-level, source-level transformations (e.g. constant folding) belong here
// rather than in the low-level instruction emitter.

import {
  type EmittedClass,
  computeInnerClassInfo,
  computeNestMembers,
  emitAnnotationType,
  emitAnonymousClassIfPossible,
  emitClass,
  emitEnum,
  emitInterface,
  emitRecord,
  setEmitDebugInfo,
} from "./bytecode.ts";
import type { Checker } from "./checker.ts";
import { forEachChild } from "./parser.ts";
import { entityNameToString } from "./utilities.ts";
import type { Program } from "./program.ts";
import {
  type AnnotationTypeDeclaration,
  type ClassDeclaration,
  type EnumDeclaration,
  type InterfaceDeclaration,
  type MethodDeclaration,
  type Node,
  type ObjectCreationExpression,
  type Parameter,
  type RecordDeclaration,
  type SourceFile,
  SyntaxKind,
  type TypeReference,
} from "./types.ts";

export type { EmittedClass } from "./bytecode.ts";

/** Whether a member list declares an entry point: public static void main(String[]). */
export function declaresMainMethod(members: readonly Node[] | undefined): boolean {
  return (members ?? []).some(member => {
    if (member.kind !== SyntaxKind.MethodDeclaration) return false;
    const method = member as MethodDeclaration;
    if (method.name.text !== "main" || method.parameters.length !== 1) return false;
    const modifiers = method.modifiers ?? [];
    const isPublicStatic =
      modifiers.some(m => m.kind === SyntaxKind.PublicKeyword) &&
      modifiers.some(m => m.kind === SyntaxKind.StaticKeyword);
    if (!isPublicStatic) return false;
    const returnsVoid =
      method.returnType.kind === SyntaxKind.PrimitiveType &&
      (method.returnType as { keyword?: SyntaxKind }).keyword === SyntaxKind.VoidKeyword;
    if (!returnsVoid) return false;
    // String[] args or String... args
    const parameter = method.parameters[0] as Parameter;
    const isStringRef = (t: Node): boolean =>
      t.kind === SyntaxKind.TypeReference &&
      /^(java\.lang\.)?String$/.test(entityNameToString((t as TypeReference).typeName));
    if (parameter.isVarArgs || parameter.arrayRankAfterName === 1) {
      return isStringRef(parameter.type);
    }
    return (
      parameter.type.kind === SyntaxKind.ArrayType &&
      isStringRef((parameter.type as { elementType: Node }).elementType)
    );
  });
}

/**
 * Emit a .class file for every class declaration in a source file: top-level
 * classes and their nested classes (each gets its own class file named
 * Outer$Inner, the binary name the class-file writer derives from the symbol).
 */
export function emitSourceFile(
  sourceFile: SourceFile,
  program: Program,
  checker: Checker,
  options?: { debugInfo?: boolean },
): EmittedClass[] {
  const result: EmittedClass[] = [];
  // Nest grouping (host -> members) so each class gets NestHost / NestMembers,
  // letting nestmates share private access.
  const nest = computeNestMembers(sourceFile, program);
  // Nested-class records for the InnerClasses attribute (JVMS 4.7.6).
  const inner = computeInnerClassInfo(sourceFile, program);
  // -g-equivalent debug info (LocalVariableTable); restored after this file.
  const previousDebugInfo = setEmitDebugInfo(options?.debugInfo ?? false);
  const visit = (node: Node): void => {
    if (node.kind === SyntaxKind.ClassDeclaration) {
      const decl = node as ClassDeclaration;
      // Anonymous classes (new T(){...}) live as ObjectCreationExpression.classBody,
      // not ClassDeclaration; a ClassDeclaration with no name/symbol is skipped.
      if (decl.symbol || decl.name) {
        result.push({
          ...emitClass(decl, program, checker, nest, inner),
          hasMainMethod: declaresMainMethod(decl.members),
        });
      }
    } else if (node.kind === SyntaxKind.InterfaceDeclaration) {
      result.push(emitInterface(node as InterfaceDeclaration, program, checker, nest, inner));
    } else if (node.kind === SyntaxKind.EnumDeclaration) {
      result.push(...emitEnum(node as EnumDeclaration, program, checker, nest, inner));
    } else if (node.kind === SyntaxKind.RecordDeclaration) {
      result.push(emitRecord(node as RecordDeclaration, program, checker, nest, inner));
    } else if (node.kind === SyntaxKind.AnnotationTypeDeclaration) {
      result.push(
        emitAnnotationType(node as AnnotationTypeDeclaration, program, checker, nest, inner),
      );
    } else if (node.kind === SyntaxKind.ObjectCreationExpression) {
      // Anonymous class (new T(){...}): emitted as its own Outer$N when supported.
      const anon = emitAnonymousClassIfPossible(
        node as ObjectCreationExpression,
        program,
        checker,
        nest,
        inner,
      );
      if (anon) result.push(anon);
    }
    forEachChild(node, child => {
      visit(child);
      return undefined;
    });
  };
  try {
    visit(sourceFile);
  } finally {
    setEmitDebugInfo(previousDebugInfo);
  }
  return result;
}
