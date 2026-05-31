// Type checker. Resolves AST type nodes to the Type model, computes the type of
// expressions, and resolves member access (a.b). This milestone (P5) covers
// declared types, the common expression forms and member typing - enough for
// hover and as the base for assignability/overloads/inference (P6-P8).
// Everything unknown degrades to errorType.

import { createDiagnostic, Diagnostics } from "./diagnostics.ts";
import { forEachChild } from "./parser.ts";
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
  typeToString,
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
  type AssignmentExpression,
  type BinaryExpression,
  type CallExpression,
  type CastExpression,
  type ConditionalExpression,
  type Diagnostic,
  type ElementAccessExpression,
  type Identifier,
  type LiteralExpression,
  type MethodDeclaration,
  type Node,
  type ObjectCreationExpression,
  type ParenthesizedExpression,
  type PrefixUnaryExpression,
  type PropertyAccessExpression,
  type ReturnStatement,
  type SourceFile,
  type Symbol,
  SymbolFlags,
  SyntaxKind,
  type TypeNode,
  type TypeReference,
  type VariableDeclarator,
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
  /** The chosen overload for a call (JLS 15.12.2), or undefined if unresolved. */
  resolveCall(call: CallExpression): MethodDeclaration | undefined;
  /** High-precision semantic diagnostics (type mismatches between known types). */
  getSemanticDiagnostics(sourceFile: SourceFile): Diagnostic[];
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
        const decl = resolveCall(node as CallExpression);
        return decl ? resolveType(decl.returnType, decl) : errorType;
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

  function isAssignableToClass(source: Type, target: ClassType, allowBoxing: boolean): boolean {
    switch (source.kind) {
      case TypeKind.Null:
        return true;
      case TypeKind.Primitive: {
        if (!allowBoxing) return false;
        const boxed = BOX[source.name];
        return boxed ? isAssignableToClass(classTypeByFqn(boxed), target, true) : false;
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

  function isAssignableToPrimitive(
    source: Type,
    target: { name: string },
    allowBoxing: boolean,
  ): boolean {
    if (source.kind === TypeKind.Primitive) return primitiveWidens(source.name, target.name);
    // unboxing then widening (Integer -> int -> long)
    if (allowBoxing && source.kind === TypeKind.Class) {
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

  function isAssignableTo(source: Type, target: Type, allowBoxing = true): boolean {
    if (isError(source) || isError(target)) return true; // degrade, never a false error
    if (source === target) return true;
    switch (target.kind) {
      case TypeKind.Primitive:
        return isAssignableToPrimitive(source, target, allowBoxing);
      case TypeKind.Class:
        return isAssignableToClass(source, target, allowBoxing);
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

  // --- overload resolution (JLS 15.12.2) ---------------------------------------------

  interface ParamInfo {
    type: Type;
    isVarArgs: boolean;
  }

  function methodParams(decl: MethodDeclaration): ParamInfo[] {
    return decl.parameters.map(p => ({
      type: resolveType(p.type, decl),
      isVarArgs: !!p.isVarArgs,
    }));
  }

  function paramSlotType(p: ParamInfo): Type {
    return p.isVarArgs ? arrayType(p.type) : p.type;
  }

  function applicable(
    params: ParamInfo[],
    args: Type[],
    allowBoxing: boolean,
    varargs: boolean,
  ): boolean {
    if (!varargs) {
      if (params.length !== args.length) return false;
      return params.every((p, i) => isAssignableTo(args[i]!, paramSlotType(p), allowBoxing));
    }
    if (params.length === 0) return false;
    const last = params[params.length - 1]!;
    if (!last.isVarArgs) return false;
    if (args.length < params.length - 1) return false;
    for (let i = 0; i < params.length - 1; i++) {
      if (!isAssignableTo(args[i]!, params[i]!.type, true)) return false;
    }
    for (let i = params.length - 1; i < args.length; i++) {
      if (!isAssignableTo(args[i]!, last.type, true)) return false;
    }
    return true;
  }

  function moreSpecific(a: MethodDeclaration, b: MethodDeclaration): boolean {
    const pa = methodParams(a);
    const pb = methodParams(b);
    if (pa.length !== pb.length) return false;
    return pa.every((p, i) => isAssignableTo(paramSlotType(p), paramSlotType(pb[i]!), true));
  }

  function chooseOverload(decls: MethodDeclaration[], args: Type[]): MethodDeclaration {
    const phases: ReadonlyArray<readonly [boolean, boolean]> = [
      [false, false], // strict
      [true, false], // boxing
      [true, true], // varargs
    ];
    for (const [allowBoxing, varargs] of phases) {
      const ok = decls.filter(d => applicable(methodParams(d), args, allowBoxing, varargs));
      if (ok.length > 0) {
        let best = ok[0]!;
        for (const d of ok.slice(1)) if (moreSpecific(d, best)) best = d;
        return best;
      }
    }
    return decls[0]!;
  }

  function resolveCall(call: CallExpression): MethodDeclaration | undefined {
    const callee = call.expression;
    let symbol: Symbol | undefined;
    if (callee.kind === SyntaxKind.Identifier)
      symbol = resolveIdentifier(callee as Identifier, program);
    else if (callee.kind === SyntaxKind.PropertyAccessExpression) {
      symbol = resolveMemberAccess(callee as PropertyAccessExpression);
    }
    if (!symbol) return undefined;
    const decls = (symbol.declarations ?? []).filter(
      d => d.kind === SyntaxKind.MethodDeclaration,
    ) as MethodDeclaration[];
    if (decls.length === 0) return undefined;
    if (decls.length === 1) return decls[0];
    return chooseOverload(decls, call.arguments.map(getTypeOfExpression));
  }

  // --- semantic diagnostics ----------------------------------------------------------

  // Only types we can fully reason about are checked, so a mismatch is never a
  // false positive: no error type and no type variable / wildcard / intersection
  // anywhere (those need substitution/inference we do not perform yet).
  function isConcrete(type: Type): boolean {
    switch (type.kind) {
      case TypeKind.Primitive:
      case TypeKind.Null:
        return true;
      case TypeKind.Class:
        return (type as ClassType).typeArguments.every(isConcrete);
      case TypeKind.Array:
        return isConcrete((type as ArrayType).elementType);
      default:
        return false;
    }
  }

  function enclosingReturnType(node: Node): Type | undefined {
    let current: Node | undefined = node;
    while (current) {
      if (current.kind === SyntaxKind.MethodDeclaration) {
        return resolveType((current as MethodDeclaration).returnType, current);
      }
      if (current.kind === SyntaxKind.LambdaExpression) return undefined; // lambda target typing: later
      current = current.parent;
    }
    return undefined;
  }

  function getSemanticDiagnostics(sourceFile: SourceFile): Diagnostic[] {
    const diagnostics: Diagnostic[] = [];

    const checkAssignment = (valueNode: Node, targetType: Type): void => {
      if (targetType.kind === TypeKind.Primitive && targetType.name === "void") return;
      if (!isConcrete(targetType)) return;
      const valueType = getTypeOfExpression(valueNode);
      if (!isConcrete(valueType)) return;
      // High-precision scope: only the primitive<->reference boundary, where an
      // incompatibility is unambiguous (e.g. int x = "s"). Primitive-to-primitive
      // is skipped because constant narrowing (JLS 5.2) is legal without a cast,
      // and reference-to-reference / generic cases depend on subtyping precision
      // we do not fully model yet. These are broadened in P11.
      const oneIsPrimitive =
        (targetType.kind === TypeKind.Primitive) !== (valueType.kind === TypeKind.Primitive);
      if (!oneIsPrimitive) return;
      if (!isAssignableTo(valueType, targetType)) {
        diagnostics.push(
          createDiagnostic(
            valueNode.pos,
            valueNode.end - valueNode.pos,
            Diagnostics.Incompatible_types_0_1,
            typeToString(valueType),
            typeToString(targetType),
          ),
        );
      }
    };

    const visit = (node: Node): void => {
      switch (node.kind) {
        case SyntaxKind.VariableDeclarator: {
          const d = node as VariableDeclarator;
          if (d.initializer && d.symbol && d.initializer.kind !== SyntaxKind.ArrayInitializer) {
            checkAssignment(d.initializer, getTypeOfSymbol(d.symbol));
          }
          break;
        }
        case SyntaxKind.AssignmentExpression: {
          const a = node as AssignmentExpression;
          if (a.operatorToken === SyntaxKind.EqualsToken) {
            checkAssignment(a.right, getTypeOfExpression(a.left));
          }
          break;
        }
        case SyntaxKind.ReturnStatement: {
          const r = node as ReturnStatement;
          if (r.expression) {
            const ret = enclosingReturnType(node);
            if (ret) checkAssignment(r.expression, ret);
          }
          break;
        }
        default:
          break;
      }
      forEachChild(node, child => {
        visit(child);
        return undefined;
      });
    };

    visit(sourceFile);
    return diagnostics;
  }

  return {
    resolveType,
    getTypeOfSymbol,
    getTypeOfExpression,
    resolveName,
    isAssignableTo,
    resolveCall,
    getSemanticDiagnostics,
  };
}
