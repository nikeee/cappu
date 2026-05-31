// Type checker. Resolves AST type nodes to the Type model, computes the type of
// expressions, and resolves member access (a.b). This milestone (P5) covers
// declared types, the common expression forms and member typing - enough for
// hover and as the base for assignability/overloads/inference (P6-P8).
// Everything unknown degrades to errorType.

import type { Program } from "./program.ts";
import {
  type ArrayType,
  arrayType,
  type ClassType,
  classType,
  errorType,
  isError,
  nullType,
  primitiveType,
  type Type,
  TypeKind,
  typeVariable,
  type WildcardType,
} from "./checkerTypes.ts";
import {
  getDirectSuperTypeSymbols,
  lookupMember,
  Meaning,
  resolveIdentifier,
  resolveTypeEntityName,
} from "./resolver.ts";
import { tokenToString } from "./utilities.ts";
import {
  type ArrayCreationExpression,
  type ArrayType as AstArrayType,
  type BinaryExpression,
  type CallExpression,
  type CastExpression,
  type ConditionalExpression,
  type ElementAccessExpression,
  type Identifier,
  type LiteralExpression,
  type MethodDeclaration,
  type Node,
  type ObjectCreationExpression,
  type ParenthesizedExpression,
  type PrefixUnaryExpression,
  type PropertyAccessExpression,
  type Symbol,
  SymbolFlags,
  SyntaxKind,
  type TypeNode,
  type TypeReference,
  type WildcardType as AstWildcardType,
} from "./types.ts";

export interface Checker {
  resolveType(typeNode: TypeNode, fromNode: Node): Type;
  getTypeOfSymbol(symbol: Symbol): Type;
  getTypeOfExpression(node: Node): Type;
  /** Resolve a name use OR a member access (a.b) to its symbol. */
  resolveName(identifier: Identifier): Symbol | undefined;
  /** JLS assignment conversion: can a value of `source` be assigned to `target`? */
  isAssignableTo(source: Type, target: Type): boolean;
}

// Primitive widening (JLS 5.1.2) and boxing (JLS 5.1.7).
const WIDENING: Record<string, readonly string[]> = {
  byte: ["short", "int", "long", "float", "double"],
  short: ["int", "long", "float", "double"],
  char: ["int", "long", "float", "double"],
  int: ["long", "float", "double"],
  long: ["float", "double"],
  float: ["double"],
};
const BOX: Record<string, string> = {
  boolean: "java.lang.Boolean",
  byte: "java.lang.Byte",
  short: "java.lang.Short",
  char: "java.lang.Character",
  int: "java.lang.Integer",
  long: "java.lang.Long",
  float: "java.lang.Float",
  double: "java.lang.Double",
};
const UNBOX: Record<string, string> = Object.fromEntries(
  Object.entries(BOX).map(([prim, fqn]) => [fqn, prim]),
);

function primitiveWidens(from: string, to: string): boolean {
  return from === to || (WIDENING[from]?.includes(to) ?? false);
}

const COMPARISON_OPERATORS = new Set<SyntaxKind>([
  SyntaxKind.LessThanToken,
  SyntaxKind.GreaterThanToken,
  SyntaxKind.LessThanEqualsToken,
  SyntaxKind.GreaterThanEqualsToken,
  SyntaxKind.EqualsEqualsToken,
  SyntaxKind.ExclamationEqualsToken,
  SyntaxKind.AmpersandAmpersandToken,
  SyntaxKind.BarBarToken,
]);

function isTypeDeclarationKind(kind: SyntaxKind): boolean {
  return (
    kind === SyntaxKind.ClassDeclaration ||
    kind === SyntaxKind.InterfaceDeclaration ||
    kind === SyntaxKind.EnumDeclaration ||
    kind === SyntaxKind.AnnotationTypeDeclaration ||
    kind === SyntaxKind.RecordDeclaration
  );
}

function enclosingTypeSymbol(node: Node): Symbol | undefined {
  let current: Node | undefined = node;
  while (current) {
    if (isTypeDeclarationKind(current.kind) && current.symbol) return current.symbol;
    current = current.parent;
  }
  return undefined;
}

export function createChecker(program: Program): Checker {
  const symbolTypes = new WeakMap<Symbol, Type>();
  const expressionTypes = new WeakMap<Node, Type>();

  const booleanType = primitiveType("boolean");
  const intType = primitiveType("int");
  const charType = primitiveType("char");

  function classTypeByFqn(fqn: string): Type {
    const symbol = program.getGlobalIndex().getType(fqn);
    return symbol ? classType(symbol) : errorType;
  }

  function resolveType(typeNode: TypeNode, fromNode: Node): Type {
    switch (typeNode.kind) {
      case SyntaxKind.PrimitiveType:
        return primitiveType(
          tokenToString((typeNode as { keyword: SyntaxKind }).keyword) ?? "<error>",
        );
      case SyntaxKind.ArrayType:
        return arrayType(resolveType((typeNode as AstArrayType).elementType, fromNode));
      case SyntaxKind.WildcardType: {
        const w = typeNode as AstWildcardType;
        return {
          kind: TypeKind.Wildcard,
          isExtends: w.hasExtends,
          isSuper: w.hasSuper,
          bound: w.type ? resolveType(w.type, fromNode) : undefined,
        };
      }
      case SyntaxKind.VarType:
        return errorType; // 'var' inference is P8
      case SyntaxKind.TypeReference: {
        const ref = typeNode as TypeReference;
        const symbol = resolveTypeEntityName(ref.typeName, fromNode, program);
        if (!symbol) return errorType;
        if (symbol.flags & SymbolFlags.TypeParameter) return typeVariable(symbol);
        const args = ref.typeArguments?.map(a => resolveType(a as TypeNode, fromNode)) ?? [];
        return classType(symbol, args);
      }
      default:
        return errorType;
    }
  }

  function declaredTypeNodeOf(symbol: Symbol): { typeNode: TypeNode; from: Node } | undefined {
    const declaration = symbol.valueDeclaration ?? symbol.declarations?.[0];
    if (!declaration) return undefined;
    switch (declaration.kind) {
      case SyntaxKind.VariableDeclarator: {
        const parent = declaration.parent as { type?: TypeNode };
        return parent.type ? { typeNode: parent.type, from: declaration } : undefined;
      }
      case SyntaxKind.Parameter:
      case SyntaxKind.RecordComponent: {
        const t = (declaration as { type?: TypeNode }).type;
        return t ? { typeNode: t, from: declaration } : undefined;
      }
      case SyntaxKind.MethodDeclaration:
        return { typeNode: (declaration as MethodDeclaration).returnType, from: declaration };
      default:
        return undefined;
    }
  }

  function getTypeOfSymbol(symbol: Symbol): Type {
    const cached = symbolTypes.get(symbol);
    if (cached) return cached;

    let type: Type = errorType;
    if (symbol.flags & SymbolFlags.TypeParameter) {
      type = typeVariable(symbol);
    } else if (symbol.flags & SymbolFlags.Type) {
      type = classType(symbol);
    } else if (symbol.flags & SymbolFlags.EnumConstant) {
      type = symbol.parent ? classType(symbol.parent) : errorType;
    } else {
      const declared = declaredTypeNodeOf(symbol);
      if (declared) type = resolveType(declared.typeNode, declared.from);
    }
    symbolTypes.set(symbol, type);
    return type;
  }

  function resolveMemberAccess(access: PropertyAccessExpression): Symbol | undefined {
    const targetType = getTypeOfExpression(access.expression);
    if (targetType.kind !== TypeKind.Class) return undefined;
    return lookupMember((targetType as ClassType).symbol, access.name.text, Meaning.Any, program);
  }

  function resolveName(identifier: Identifier): Symbol | undefined {
    const parent = identifier.parent;
    if (
      parent &&
      parent.kind === SyntaxKind.PropertyAccessExpression &&
      (parent as PropertyAccessExpression).name === identifier
    ) {
      return resolveMemberAccess(parent as PropertyAccessExpression);
    }
    return resolveIdentifier(identifier, program);
  }

  function numericLiteralType(value: string): Type {
    if (/[lL]$/.test(value)) return primitiveType("long");
    if (/[fF]$/.test(value)) return primitiveType("float");
    if (/[dD]$/.test(value) || /[.eEpP]/.test(value)) return primitiveType("double");
    return intType;
  }

  function widerNumeric(a: Type, b: Type): Type {
    const order = ["int", "long", "float", "double"];
    const rank = (t: Type) => (t.kind === TypeKind.Primitive ? order.indexOf(t.name) : -1);
    const ra = rank(a);
    const rb = rank(b);
    if (ra < 0 && rb < 0) return errorType;
    return rb > ra ? b : a;
  }

  function isString(type: Type, stringType: Type): boolean {
    return (
      type.kind === TypeKind.Class &&
      stringType.kind === TypeKind.Class &&
      type.symbol === stringType.symbol
    );
  }

  function getTypeOfExpression(node: Node): Type {
    const cached = expressionTypes.get(node);
    if (cached) return cached;
    const type = computeExpressionType(node);
    expressionTypes.set(node, type);
    return type;
  }

  function computeExpressionType(node: Node): Type {
    switch (node.kind) {
      case SyntaxKind.NumericLiteral:
        return numericLiteralType((node as LiteralExpression).value);
      case SyntaxKind.StringLiteral:
      case SyntaxKind.TextBlockLiteral:
        return classTypeByFqn("java.lang.String");
      case SyntaxKind.CharacterLiteral:
        return charType;
      case SyntaxKind.TrueKeyword:
      case SyntaxKind.FalseKeyword:
        return booleanType;
      case SyntaxKind.NullKeyword:
        return nullType;
      case SyntaxKind.Identifier: {
        const symbol = resolveIdentifier(node as Identifier, program);
        return symbol ? getTypeOfSymbol(symbol) : errorType;
      }
      case SyntaxKind.ThisExpression: {
        const enclosing = enclosingTypeSymbol(node);
        return enclosing ? classType(enclosing) : errorType;
      }
      case SyntaxKind.SuperExpression:
        return classTypeByFqn("java.lang.Object");
      case SyntaxKind.ParenthesizedExpression:
        return getTypeOfExpression((node as ParenthesizedExpression).expression);
      case SyntaxKind.CastExpression:
        return resolveType((node as CastExpression).type, node);
      case SyntaxKind.PropertyAccessExpression: {
        const symbol = resolveMemberAccess(node as PropertyAccessExpression);
        return symbol ? getTypeOfSymbol(symbol) : errorType;
      }
      case SyntaxKind.CallExpression: {
        const callee = (node as CallExpression).expression;
        let methodSymbol: Symbol | undefined;
        if (callee.kind === SyntaxKind.Identifier) {
          methodSymbol = resolveIdentifier(callee as Identifier, program);
        } else if (callee.kind === SyntaxKind.PropertyAccessExpression) {
          methodSymbol = resolveMemberAccess(callee as PropertyAccessExpression);
        }
        return methodSymbol ? getTypeOfSymbol(methodSymbol) : errorType;
      }
      case SyntaxKind.ObjectCreationExpression:
        return resolveType((node as ObjectCreationExpression).type, node);
      case SyntaxKind.ArrayCreationExpression: {
        const n = node as ArrayCreationExpression;
        let t = resolveType(n.elementType, node);
        for (let i = 0; i < n.dimensions.length + n.additionalRank; i++) t = arrayType(t);
        return t;
      }
      case SyntaxKind.ElementAccessExpression: {
        const target = getTypeOfExpression((node as ElementAccessExpression).expression);
        return target.kind === TypeKind.Array ? target.elementType : errorType;
      }
      case SyntaxKind.InstanceofExpression:
        return booleanType;
      case SyntaxKind.PrefixUnaryExpression: {
        const u = node as PrefixUnaryExpression;
        return u.operator === SyntaxKind.ExclamationToken
          ? booleanType
          : getTypeOfExpression(u.operand);
      }
      case SyntaxKind.PostfixUnaryExpression:
        return getTypeOfExpression((node as unknown as { operand: Node }).operand);
      case SyntaxKind.ConditionalExpression: {
        const whenTrue = getTypeOfExpression((node as ConditionalExpression).whenTrue);
        return whenTrue.kind === TypeKind.Error
          ? getTypeOfExpression((node as ConditionalExpression).whenFalse)
          : whenTrue;
      }
      case SyntaxKind.BinaryExpression: {
        const b = node as BinaryExpression;
        if (COMPARISON_OPERATORS.has(b.operatorToken)) return booleanType;
        const left = getTypeOfExpression(b.left);
        const right = getTypeOfExpression(b.right);
        if (b.operatorToken === SyntaxKind.PlusToken) {
          const stringType = classTypeByFqn("java.lang.String");
          if (isString(left, stringType) || isString(right, stringType)) return stringType;
        }
        return widerNumeric(left, right);
      }
      default:
        return errorType;
    }
  }

  function objectSymbol(): Symbol | undefined {
    return program.getGlobalIndex().getType("java.lang.Object");
  }

  // source's class symbol is a subtype of target's (incl. implicit Object).
  function isClassSubtype(sourceSym: Symbol, targetSym: Symbol): boolean {
    if (sourceSym === targetSym) return true;
    if (targetSym === objectSymbol()) return true;
    const seen = new Set<Symbol>();
    const queue: Symbol[] = [sourceSym];
    while (queue.length > 0) {
      const current = queue.shift()!;
      if (current === targetSym) return true;
      if (seen.has(current)) continue;
      seen.add(current);
      queue.push(...getDirectSuperTypeSymbols(current, program));
    }
    return false;
  }

  function typesEqual(a: Type, b: Type): boolean {
    if (a.kind !== b.kind) return false;
    switch (a.kind) {
      case TypeKind.Primitive:
        return a.name === (b as { name: string }).name;
      case TypeKind.Class: {
        const bc = b as ClassType;
        return (
          a.symbol === bc.symbol &&
          a.typeArguments.length === bc.typeArguments.length &&
          a.typeArguments.every((t, i) => typesEqual(t, bc.typeArguments[i]!))
        );
      }
      case TypeKind.Array:
        return typesEqual(a.elementType, (b as ArrayType).elementType);
      case TypeKind.TypeVariable:
        return a.symbol === (b as { symbol: Symbol }).symbol;
      default:
        return true;
    }
  }

  // Type-argument compatibility for two invocations of the same generic type,
  // honouring wildcard variance (JLS 4.5.1, 4.10.2).
  function typeArgumentsCompatible(srcArgs: readonly Type[], tgtArgs: readonly Type[]): boolean {
    if (srcArgs.length === 0 || tgtArgs.length === 0) return true; // raw type involved
    if (srcArgs.length !== tgtArgs.length) return true;
    return tgtArgs.every((tgt, i) => {
      const src = srcArgs[i]!;
      if (tgt.kind === TypeKind.Wildcard) {
        const w = tgt as WildcardType;
        if (w.isExtends && w.bound) return isAssignableTo(src, w.bound);
        if (w.isSuper && w.bound) return isAssignableTo(w.bound, src);
        return true; // unbounded ?
      }
      return typesEqual(src, tgt);
    });
  }

  function isAssignableToClass(source: Type, target: ClassType): boolean {
    switch (source.kind) {
      case TypeKind.Null:
        return true;
      case TypeKind.Primitive: {
        const boxed = BOX[source.name];
        return boxed ? isAssignableToClass(classTypeByFqn(boxed), target) : false;
      }
      case TypeKind.Class:
        if (source.symbol === target.symbol) {
          return typeArgumentsCompatible(source.typeArguments, target.typeArguments);
        }
        return isClassSubtype(source.symbol, target.symbol);
      case TypeKind.Array:
        return target.symbol === objectSymbol();
      case TypeKind.TypeVariable:
        return target.symbol === objectSymbol();
      default:
        return false;
    }
  }

  function isAssignableToPrimitive(source: Type, target: { name: string }): boolean {
    if (source.kind === TypeKind.Primitive) return primitiveWidens(source.name, target.name);
    // unboxing then widening (Integer -> int -> long)
    if (source.kind === TypeKind.Class) {
      const unboxed = UNBOX[fqnOf(source)];
      if (unboxed) return primitiveWidens(unboxed, target.name);
    }
    return false;
  }

  function fqnOf(type: ClassType): string {
    const parts: string[] = [type.symbol.escapedName];
    let parent = type.symbol.parent;
    while (parent && parent.escapedName) {
      parts.unshift(parent.escapedName);
      parent = parent.parent;
    }
    return parts.join(".");
  }

  function isAssignableTo(source: Type, target: Type): boolean {
    if (isError(source) || isError(target)) return true; // degrade, never a false error
    if (source === target) return true;
    switch (target.kind) {
      case TypeKind.Primitive:
        return isAssignableToPrimitive(source, target);
      case TypeKind.Class:
        return isAssignableToClass(source, target);
      case TypeKind.Array:
        if (source.kind === TypeKind.Null) return true;
        if (source.kind !== TypeKind.Array) return false;
        // primitive element arrays are invariant; reference element arrays covariant
        if (
          source.elementType.kind === TypeKind.Primitive ||
          target.elementType.kind === TypeKind.Primitive
        ) {
          return typesEqual(source.elementType, target.elementType);
        }
        return isAssignableTo(source.elementType, target.elementType);
      case TypeKind.TypeVariable:
        return source.kind === TypeKind.Null;
      default:
        return source.kind === TypeKind.Null || isError(source);
    }
  }

  return { resolveType, getTypeOfSymbol, getTypeOfExpression, resolveName, isAssignableTo };
}
