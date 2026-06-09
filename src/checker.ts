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
  getSourceFileOfNode,
  lookupMember,
  Meaning,
  resolveIdentifier,
  resolveTypeEntityName,
} from "./resolver.ts";
import { entityNameToString, skipTrivia, tokenToString } from "./utilities.ts";
import {
  type Annotation,
  type ArrayCreationExpression,
  type ArrayType as AstArrayType,
  type AssignmentExpression,
  type BinaryExpression,
  type CallExpression,
  type CastExpression,
  type ConditionalExpression,
  type Diagnostic,
  type ElementAccessExpression,
  type EnumDeclaration,
  type Identifier,
  type LambdaExpression,
  type LiteralExpression,
  type MethodDeclaration,
  type MethodReferenceExpression,
  type Node,
  type ObjectCreationExpression,
  type ParenthesizedExpression,
  type PrefixUnaryExpression,
  type PropertyAccessExpression,
  type QualifiedName,
  type ReturnStatement,
  type SourceFile,
  type SwitchExpression,
  type Symbol,
  SymbolFlags,
  SyntaxKind,
  type TypeNode,
  type TypeParameter,
  type TypeReference,
  type VariableDeclarator,
  type WildcardType as AstWildcardType,
} from "./types.ts";

/** What the emitter needs to lower a lambda expression (see getLambdaInfo). */
export interface LambdaInfo {
  /** The target functional interface (a Class type). */
  readonly interfaceType: Type;
  /** The single abstract method's name (the invokedynamic call name). */
  readonly samName: string;
  /** SAM parameter/return types unsubstituted (type variables erase to Object). */
  readonly erasedParams: readonly Type[];
  readonly erasedReturn: Type;
  /** SAM parameter/return types with the target's type arguments substituted. */
  readonly instParams: readonly Type[];
  readonly instReturn: Type;
}

/** Method-reference lowering info: the SAM info plus the referenced method. */
export interface MethodRefInfo extends LambdaInfo {
  readonly kind: "static" | "bound" | "unbound" | "constructor" | "arrayConstructor";
  /** The type declaring the referenced method (or the constructed type). Absent
   * for an array constructor reference `T[]::new`, which has no class. */
  readonly ownerSymbol?: Symbol;
  /** The referenced method declaration (undefined for a constructor reference). */
  readonly target?: MethodDeclaration;
}

export interface Checker {
  resolveType(typeNode: TypeNode, fromNode: Node): Type;
  /** Lambda lowering info (target interface, SAM, erased + instantiated types). */
  getLambdaInfo(lambda: Node): LambdaInfo | undefined;
  /** Method-reference lowering info (kind, target method), or undefined. */
  getMethodRefInfo(node: Node): MethodRefInfo | undefined;
  getTypeOfSymbol(symbol: Symbol): Type;
  getTypeOfExpression(node: Node): Type;
  /** Resolve a name use OR a member access (a.b) to its symbol. */
  resolveName(identifier: Identifier): Symbol | undefined;
  /** JLS assignment conversion: can a value of `source` be assigned to `target`? */
  isAssignableTo(source: Type, target: Type): boolean;
  /** The chosen overload for a call (JLS 15.12.2), or undefined if unresolved. */
  resolveCall(call: CallExpression): MethodDeclaration | undefined;
  /**
   * Display string for a symbol's type (for hover). Falls back to the written
   * type syntax when the type cannot be resolved (e.g. a JDK type outside the
   * stub) instead of rendering the unhelpful "<error>".
   */
  typeStringOfSymbol(symbol: Symbol): string;
  /** Full signature of a method/constructor symbol (for hover), or undefined. */
  signatureOfSymbol(symbol: Symbol): string | undefined;
  /** Signature of a specific method/constructor declaration (e.g. a chosen overload). */
  signatureOfDeclaration(declaration: Node): string | undefined;
  /**
   * The Javadoc comment attached to a symbol's declaration, cleaned to plain
   * text, or undefined. Parsed lazily from the source on demand (not retained on
   * nodes), so it costs nothing until a hover asks for it.
   */
  getDocumentation(symbol: Symbol): string | undefined;
  /** The Javadoc comment attached to a specific declaration node. */
  getDocumentationOfNode(node: Node): string | undefined;
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

  // Synthetic symbol for the implicit `length` field of every array (JLS 10.7).
  const arrayLengthSymbol: Symbol = { flags: SymbolFlags.Field, escapedName: "length" };
  symbolTypes.set(arrayLengthSymbol, intType);

  // Members accessible on an array value: the implicit `length`, plus everything
  // inherited from Object (clone, equals, hashCode, getClass, toString).
  function arrayMember(name: string): Symbol | undefined {
    if (name === "length") return arrayLengthSymbol;
    return objectSymbol()?.members?.get(name);
  }

  function classTypeByFqn(fqn: string): Type {
    const symbol = program.getGlobalIndex().getType(fqn);
    return symbol ? classType(symbol) : errorType;
  }

  // --- type-variable substitution (JLS 4.5, 18) --------------------------------------

  function declarationOf(symbol: Symbol): Node | undefined {
    return symbol.valueDeclaration ?? symbol.declarations?.[0];
  }

  // The type-parameter symbols a generic class/interface declares, in order.
  function classTypeParameters(symbol: Symbol): Symbol[] {
    const declaration = declarationOf(symbol) as
      | { typeParameters?: readonly { symbol?: Symbol }[] }
      | undefined;
    const out: Symbol[] = [];
    for (const tp of declaration?.typeParameters ?? []) if (tp.symbol) out.push(tp.symbol);
    return out;
  }

  // Map a generic type's parameters to the arguments it was instantiated with.
  function substitutionFor(symbol: Symbol, args: readonly Type[]): Map<Symbol, Type> {
    const params = classTypeParameters(symbol);
    const map = new Map<Symbol, Type>();
    params.forEach((p, i) => {
      if (i < args.length) map.set(p, args[i]!);
    });
    return map;
  }

  // Replace type variables in `type` according to `map` (identity if empty).
  function substitute(type: Type, map: Map<Symbol, Type>): Type {
    if (map.size === 0) return type;
    switch (type.kind) {
      case TypeKind.TypeVariable:
        return map.get(type.symbol) ?? type;
      case TypeKind.Class:
        return type.typeArguments.length === 0
          ? type
          : classType(
              type.symbol,
              type.typeArguments.map(a => substitute(a, map)),
            );
      case TypeKind.Array:
        return arrayType(substitute(type.elementType, map));
      case TypeKind.Wildcard:
        return type.bound ? { ...type, bound: substitute(type.bound, map) } : type;
      case TypeKind.Intersection:
        return { kind: TypeKind.Intersection, types: type.types.map(t => substitute(t, map)) };
      default:
        return type;
    }
  }

  // Direct super-type references of a type declaration (class extends/implements,
  // interface extends, enum/record implements). Read generically to avoid
  // importing every declaration interface.
  function superTypeNodesOf(declaration: Node): TypeNode[] {
    const d = declaration as {
      extendsType?: TypeNode;
      extendsTypes?: readonly TypeNode[];
      implementsTypes?: readonly TypeNode[];
    };
    const out: TypeNode[] = [];
    if (d.extendsType) out.push(d.extendsType);
    if (d.extendsTypes) out.push(...d.extendsTypes);
    if (d.implementsTypes) out.push(...d.implementsTypes);
    return out;
  }

  interface TypedMember {
    readonly symbol: Symbol;
    // Substitution that maps the declaring type's parameters to concrete arguments.
    readonly subst: Map<Symbol, Type>;
  }

  // Member lookup that also yields the substitution to apply to the member's
  // declared type, threading type arguments through the inheritance chain so that
  // e.g. ArrayList<String>.iterator() resolves Iterator<E> to Iterator<String>.
  function lookupTypedMember(
    receiver: ClassType,
    name: string,
    seen = new Set<Symbol>(),
  ): TypedMember | undefined {
    const symbol = receiver.symbol;
    if (seen.has(symbol)) return undefined;
    seen.add(symbol);
    const subst = substitutionFor(symbol, receiver.typeArguments);
    const own = symbol.members?.get(name);
    if (own) return { symbol: own, subst };
    const declaration = declarationOf(symbol);
    if (declaration) {
      for (const typeNode of superTypeNodesOf(declaration)) {
        if (typeNode.kind !== SyntaxKind.TypeReference) continue;
        const superType = substitute(resolveType(typeNode, declaration), subst);
        if (superType.kind === TypeKind.Class) {
          const found = lookupTypedMember(superType as ClassType, name, seen);
          if (found) return found;
        }
      }
      // An enum implicitly extends java.lang.Enum (JLS 8.9): name(), ordinal(), ...
      if (declaration.kind === SyntaxKind.EnumDeclaration) {
        const enumType = classTypeByFqn("java.lang.Enum");
        if (enumType.kind === TypeKind.Class) {
          const found = lookupTypedMember(enumType as ClassType, name, seen);
          if (found) return found;
        }
      }
    }
    // Every type implicitly extends java.lang.Object (JLS 8.1.4), so its members
    // are inherited even without an explicit `extends`.
    const object = objectSymbol();
    if (object && symbol !== object) {
      const inherited = object.members?.get(name);
      if (inherited) return { symbol: inherited, subst: new Map() };
    }
    return undefined;
  }

  // All method declarations named `name` reachable from `receiver` (its own
  // members plus every super type / Object), each paired with the substitution
  // for the type that declares it. Unlike lookupTypedMember this gathers every
  // overload across the hierarchy, so e.g. List.add(int,E) does not hide the
  // inherited Collection.add(E).
  function collectTypedOverloads(
    receiver: ClassType,
    name: string,
    seen = new Set<Symbol>(),
    out: { decl: MethodDeclaration; subst: Map<Symbol, Type> }[] = [],
    // Erased parameter signatures already collected. A more-derived type's members
    // are visited first, so a supertype method with the same signature is an
    // overridden method (JLS 8.4.8.1) and is dropped - it is not a separate
    // overload (e.g. String.length() hides the inherited CharSequence.length()).
    sigs = new Set<string>(),
  ): { decl: MethodDeclaration; subst: Map<Symbol, Type> }[] {
    const symbol = receiver.symbol;
    if (seen.has(symbol)) return out;
    seen.add(symbol);
    const subst = substitutionFor(symbol, receiver.typeArguments);
    const add = (sym: Symbol | undefined, s: Map<Symbol, Type>): void => {
      for (const d of sym?.declarations ?? []) {
        if (d.kind !== SyntaxKind.MethodDeclaration) continue;
        const sig = methodParams(d as MethodDeclaration)
          .map(p => typeToString(substitute(paramSlotType(p), s)))
          .join(",");
        if (sigs.has(sig)) continue;
        sigs.add(sig);
        out.push({ decl: d as MethodDeclaration, subst: s });
      }
    };
    add(symbol.members?.get(name), subst);
    const declaration = declarationOf(symbol);
    if (declaration) {
      for (const typeNode of superTypeNodesOf(declaration)) {
        if (typeNode.kind !== SyntaxKind.TypeReference) continue;
        const superType = substitute(resolveType(typeNode, declaration), subst);
        if (superType.kind === TypeKind.Class)
          collectTypedOverloads(superType as ClassType, name, seen, out, sigs);
      }
      if (declaration.kind === SyntaxKind.EnumDeclaration) {
        const e = classTypeByFqn("java.lang.Enum");
        if (e.kind === TypeKind.Class) collectTypedOverloads(e as ClassType, name, seen, out, sigs);
      }
    }
    const object = objectSymbol();
    if (object && symbol !== object) add(object.members?.get(name), new Map());
    return out;
  }

  // A class type is "closed" when its full member set is known: it and every
  // transitive super type resolve to a declaration we modeled. Enums and records
  // are excluded because the compiler synthesizes members (values/valueOf,
  // component accessors) we do not bind. Only closed types are eligible for the
  // unresolved-member diagnostic, so an unmodeled super type never yields a false
  // positive.
  function isClosedType(type: ClassType): boolean {
    if (type.symbol.flags & (SymbolFlags.Enum | SymbolFlags.Record | SymbolFlags.Annotation)) {
      return false;
    }
    const supertypesResolve = (symbol: Symbol, seen: Set<Symbol>): boolean => {
      if (seen.has(symbol)) return true;
      seen.add(symbol);
      const declaration = declarationOf(symbol);
      if (!declaration) return false;
      for (const typeNode of superTypeNodesOf(declaration)) {
        if (typeNode.kind !== SyntaxKind.TypeReference) return false;
        const superSymbol = resolveTypeEntityName(
          (typeNode as TypeReference).typeName,
          declaration,
          program,
        );
        if (!superSymbol || !supertypesResolve(superSymbol, seen)) return false;
      }
      return true;
    };
    return supertypesResolve(type.symbol, new Set());
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
        // Through getTypeOfSymbol so the variable carries its bound (cached once).
        if (symbol.flags & SymbolFlags.TypeParameter) return getTypeOfSymbol(symbol);
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
      case SyntaxKind.RecordComponent:
      case SyntaxKind.TypePattern: {
        // TypePattern: a pattern binding variable, e.g. `case Circle(double r)`.
        const t = (declaration as { type?: TypeNode }).type;
        return t ? { typeNode: t, from: declaration } : undefined;
      }
      case SyntaxKind.MethodDeclaration:
        return { typeNode: (declaration as MethodDeclaration).returnType, from: declaration };
      case SyntaxKind.Resource: {
        // A try-with-resources resource carries its type on the node itself.
        const t = (declaration as { type?: TypeNode }).type;
        return t ? { typeNode: t, from: declaration } : undefined;
      }
      case SyntaxKind.Identifier: {
        const parent = declaration.parent as {
          kind: SyntaxKind;
          catchTypes?: readonly TypeNode[];
          type?: TypeNode;
        };
        // A catch parameter `catch (E e)`: its type is the (first) catch type.
        if (parent.kind === SyntaxKind.CatchClause && parent.catchTypes?.length) {
          return { typeNode: parent.catchTypes[0]!, from: declaration };
        }
        // A type-pattern binding `x instanceof T t`: its type is the pattern type.
        if (parent.kind === SyntaxKind.InstanceofExpression && parent.type) {
          return { typeNode: parent.type, from: declaration };
        }
        return undefined;
      }
      default:
        return undefined;
    }
  }

  function getTypeOfSymbol(symbol: Symbol): Type {
    const cached = symbolTypes.get(symbol);
    if (cached) return cached;

    let type: Type = errorType;
    if (symbol.flags & SymbolFlags.TypeParameter) {
      const tv = typeVariable(symbol);
      // Cache before resolving the bound: `T extends Comparable<T>` mentions T,
      // so the nested resolution must see the (still unbounded) variable instead
      // of recursing forever. The bound is patched onto the cached object.
      symbolTypes.set(symbol, tv);
      const declaration = symbol.valueDeclaration ?? symbol.declarations?.[0];
      const constraint =
        declaration?.kind === SyntaxKind.TypeParameter
          ? (declaration as TypeParameter).constraint?.[0]
          : undefined;
      if (constraint) tv.bound = resolveType(constraint, declaration!);
      return tv;
    } else if (symbol.flags & SymbolFlags.Type) {
      type = classType(symbol);
    } else if (symbol.flags & SymbolFlags.EnumConstant) {
      type = symbol.parent ? classType(symbol.parent) : errorType;
    } else {
      const declared = declaredTypeNodeOf(symbol);
      if (declared) {
        if (declared.typeNode.kind === SyntaxKind.VarType) {
          // Break cycles like `var x = x;` while inferring from the initializer.
          symbolTypes.set(symbol, errorType);
          type = inferVarType(declared.from);
        } else {
          type = resolveType(declared.typeNode, declared.from);
          // A varargs parameter `T... x` has type `T[]` (the written node is `T`).
          if (
            declared.from.kind === SyntaxKind.Parameter &&
            (declared.from as { isVarArgs?: boolean }).isVarArgs
          ) {
            type = arrayType(type);
          }
        }
      } else {
        // A concise lambda parameter (x -> ...) has no written type; infer it
        // from the target functional interface.
        type = inferLambdaParameterType(symbol);
      }
    }
    symbolTypes.set(symbol, type);
    return type;
  }

  // The single abstract method of a functional interface (its SAM), searched
  // through inherited interfaces.
  function functionalMethod(
    typeSymbol: Symbol,
    seen = new Set<Symbol>(),
  ): MethodDeclaration | undefined {
    if (seen.has(typeSymbol)) return undefined;
    seen.add(typeSymbol);
    for (const member of typeSymbol.members?.values() ?? []) {
      if (member.flags & SymbolFlags.Method) {
        const decl = member.declarations?.find(d => d.kind === SyntaxKind.MethodDeclaration);
        if (decl) return decl as MethodDeclaration;
      }
    }
    for (const superSymbol of getDirectSuperTypeSymbols(typeSymbol, program)) {
      const found = functionalMethod(superSymbol, seen);
      if (found) return found;
    }
    return undefined;
  }

  // The functional-interface type a lambda is being assigned/converted to
  // (JLS 15.27.3 - assignment, return, and invocation contexts).
  function lambdaTargetType(lambda: Node): Type | undefined {
    const parent = lambda.parent;
    // T f = () -> ...;
    if (parent.kind === SyntaxKind.VariableDeclarator && parent.symbol) {
      return getTypeOfSymbol(parent.symbol);
    }
    // return () -> ...;  -> the enclosing method's declared return type.
    if (parent.kind === SyntaxKind.ReturnStatement) {
      let n: Node | undefined = parent.parent;
      while (
        n &&
        n.kind !== SyntaxKind.MethodDeclaration &&
        n.kind !== SyntaxKind.LambdaExpression
      ) {
        n = n.parent;
      }
      return n && n.kind === SyntaxKind.MethodDeclaration
        ? resolveType((n as MethodDeclaration).returnType, n)
        : undefined;
    }
    // m(() -> ...)  -> the resolved method's parameter type at the lambda's index.
    if (parent.kind === SyntaxKind.CallExpression) {
      const call = parent as CallExpression;
      const index = call.arguments.indexOf(lambda);
      const decl = index >= 0 ? resolveCall(call) : undefined;
      const param = decl?.parameters[index] as { type?: TypeNode } | undefined;
      return param?.type ? resolveType(param.type, decl!) : undefined;
    }
    return undefined;
  }

  // Everything the emitter needs to lower a lambda: the target functional
  // interface, its SAM, and the SAM's parameter/return types both unsubstituted
  // (for the erased SAM method type) and instantiated with the target's type
  // arguments (for the lambda's own signature). Undefined when the target type
  // or its SAM cannot be resolved (the emitter then falls back).
  function getLambdaInfo(lambda: Node): LambdaInfo | undefined {
    if (lambda.kind !== SyntaxKind.LambdaExpression) return undefined;
    const target = lambdaTargetType(lambda);
    if (!target || target.kind !== TypeKind.Class) return undefined;
    const sam = functionalMethod(target.symbol);
    return sam ? functionalInfo(target, sam) : undefined;
  }

  // Build the SAM info (erased + instantiated parameter/return types) for a
  // functional interface, shared by lambdas and method references.
  function functionalInfo(target: ClassType, sam: MethodDeclaration): LambdaInfo {
    const subst = substitutionFor(target.symbol, target.typeArguments);
    const erasedParams = sam.parameters.map(p => resolveType((p as { type: TypeNode }).type, sam));
    const erasedReturn = resolveType(sam.returnType, sam);
    return {
      interfaceType: target,
      samName: sam.name.text,
      erasedParams,
      erasedReturn,
      instParams: erasedParams.map(t => substitute(t, subst)),
      instReturn: substitute(erasedReturn, subst),
    };
  }

  // A method reference (JLS 15.13): the target functional-interface info plus
  // the referenced method's kind, declaring type, and declaration.
  function getMethodRefInfo(node: Node): MethodRefInfo | undefined {
    if (node.kind !== SyntaxKind.MethodReferenceExpression) return undefined;
    const ref = node as MethodReferenceExpression;
    const target = lambdaTargetType(node);
    if (!target || target.kind !== TypeKind.Class) return undefined;
    const sam = functionalMethod(target.symbol);
    if (!sam) return undefined;
    const fi = functionalInfo(target, sam);

    const asType = (e: Node): Symbol | undefined =>
      e.kind === SyntaxKind.Identifier
        ? resolveTypeEntityName(e as Identifier, e, program)
        : undefined;
    const overloads = (typeSymbol: Symbol, name: string): MethodDeclaration[] => {
      const m = lookupMember(typeSymbol, name, Meaning.Value, program);
      return (m?.declarations?.filter(d => d.kind === SyntaxKind.MethodDeclaration) ??
        []) as MethodDeclaration[];
    };
    const isStaticDecl = (d: Node): boolean =>
      ((d as { modifiers?: readonly Node[] }).modifiers ?? []).some(
        m => m.kind === SyntaxKind.StaticKeyword,
      );
    const arity = fi.instParams.length;

    // Type::new (and T[]::new, an array constructor reference, JLS 15.13.3).
    if (ref.isConstructorRef) {
      if (ref.expression.kind === SyntaxKind.ClassLiteralExpression) {
        const t = (ref.expression as { type?: TypeNode }).type;
        if (t?.kind === SyntaxKind.ArrayType) return { ...fi, kind: "arrayConstructor" };
      }
      const owner = asType(ref.expression);
      return owner ? { ...fi, kind: "constructor", ownerSymbol: owner } : undefined;
    }
    const name = ref.name!.text;
    // Type::method - a static method (arity == SAM arity) or an unbound instance
    // method (the SAM's first parameter is the receiver, so arity == SAM-1).
    const typeSym = asType(ref.expression);
    if (typeSym) {
      const cands = overloads(typeSym, name);
      const staticM = cands.find(d => isStaticDecl(d) && d.parameters.length === arity);
      if (staticM) return { ...fi, kind: "static", ownerSymbol: typeSym, target: staticM };
      const unbound =
        cands.find(d => !isStaticDecl(d) && d.parameters.length === arity - 1) ??
        (cands.length === 1 ? cands[0] : undefined);
      return unbound
        ? { ...fi, kind: "unbound", ownerSymbol: typeSym, target: unbound }
        : undefined;
    }
    // expr::method - bound to the value of `expr` (arity == SAM arity).
    const recv = getTypeOfExpression(ref.expression);
    if (recv.kind !== TypeKind.Class) return undefined;
    const cands = overloads(recv.symbol, name);
    const decl =
      cands.find(d => d.parameters.length === arity) ?? (cands.length === 1 ? cands[0] : undefined);
    return decl ? { ...fi, kind: "bound", ownerSymbol: recv.symbol, target: decl } : undefined;
  }

  function inferLambdaParameterType(symbol: Symbol): Type {
    const declaration = symbol.valueDeclaration ?? symbol.declarations?.[0];
    if (!declaration || declaration.kind !== SyntaxKind.Identifier) return errorType;
    const lambda = declaration.parent;
    if (!lambda || lambda.kind !== SyntaxKind.LambdaExpression) return errorType;
    const index = (lambda as LambdaExpression).parameters.indexOf(declaration);
    const target = lambdaTargetType(lambda);
    if (index < 0 || !target || target.kind !== TypeKind.Class) return errorType;
    const sam = functionalMethod(target.symbol);
    if (!sam || index >= sam.parameters.length) return errorType;
    const paramType = resolveType(sam.parameters[index]!.type, sam);
    return substitute(paramType, substitutionFor(target.symbol, target.typeArguments));
  }

  // Infer the type of a `var` declaration: from the initializer for a local, or
  // from the iterable's element type for an enhanced-for variable.
  function inferVarType(declaration: Node): Type {
    if (declaration.kind === SyntaxKind.VariableDeclarator) {
      const init = (declaration as VariableDeclarator).initializer;
      return init ? getTypeOfExpression(init) : errorType;
    }
    if (
      declaration.kind === SyntaxKind.Parameter &&
      declaration.parent.kind === SyntaxKind.ForEachStatement
    ) {
      const iterable = getTypeOfExpression(
        (declaration.parent as unknown as { expression: Node }).expression,
      );
      return elementTypeOf(iterable);
    }
    return errorType;
  }

  // The element type of an array, or the E of Iterable<E> (threading type
  // arguments through the inheritance chain), else errorType.
  function elementTypeOf(iterable: Type): Type {
    if (iterable.kind === TypeKind.Array) return iterable.elementType;
    if (iterable.kind === TypeKind.Class) {
      const iterableSymbol = program.getGlobalIndex().getType("java.lang.Iterable");
      if (iterableSymbol) {
        const instance = asInstanceOf(iterable as ClassType, iterableSymbol);
        if (instance && instance.typeArguments.length > 0) return instance.typeArguments[0]!;
      }
    }
    return errorType;
  }

  // The instantiation of `targetSymbol` that `receiver` is a subtype of, with
  // type arguments substituted along the way (e.g. how ArrayList<String> sees
  // Iterable<String>), or undefined if `receiver` is not such a subtype.
  function asInstanceOf(
    receiver: ClassType,
    targetSymbol: Symbol,
    seen = new Set<Symbol>(),
  ): ClassType | undefined {
    if (receiver.symbol === targetSymbol) return receiver;
    if (seen.has(receiver.symbol)) return undefined;
    seen.add(receiver.symbol);
    const declaration = declarationOf(receiver.symbol);
    if (!declaration) return undefined;
    const subst = substitutionFor(receiver.symbol, receiver.typeArguments);
    for (const typeNode of superTypeNodesOf(declaration)) {
      if (typeNode.kind !== SyntaxKind.TypeReference) continue;
      const superType = substitute(resolveType(typeNode, declaration), subst);
      if (superType.kind === TypeKind.Class) {
        const found = asInstanceOf(superType as ClassType, targetSymbol, seen);
        if (found) return found;
      }
    }
    return undefined;
  }

  function typeOfMemberAccess(access: PropertyAccessExpression): Type {
    const receiver = getTypeOfExpression(access.expression);
    if (receiver.kind === TypeKind.Array) {
      const member = arrayMember(access.name.text);
      return member ? getTypeOfSymbol(member) : errorType;
    }
    if (receiver.kind !== TypeKind.Class) return errorType;
    const found = lookupTypedMember(receiver as ClassType, access.name.text);
    if (!found) return errorType;
    return substitute(getTypeOfSymbol(found.symbol), found.subst);
  }

  function nodeSourceText(node: Node): string {
    const text = getSourceFileOfNode(node).text;
    // Start at the token, not node.pos, which includes leading trivia (e.g. a
    // Javadoc comment before the return type would otherwise be captured).
    return text.slice(skipTrivia(text, node.pos), node.end).trim().replace(/\s+/g, " ");
  }

  function typeStringOfSymbol(symbol: Symbol): string {
    // For a symbol declared with explicit type syntax, show that syntax: it is
    // always correct and clean, and never degrades to "<error>" when a type
    // (or a type argument) lies outside the modeled set - the common case for
    // JDK types the stub does not include.
    const declared = declaredTypeNodeOf(symbol);
    if (declared && declared.typeNode.kind !== SyntaxKind.VarType) {
      const text = nodeSourceText(declared.typeNode);
      if (text) return text;
    }
    // No written type (var, enum constant, ...): use the computed type, which
    // also surfaces var inference (var x = "s" -> String).
    const type = getTypeOfSymbol(symbol);
    if (!isError(type)) return typeToString(type);
    if (declared && declared.typeNode.kind === SyntaxKind.VarType) return "var";
    return typeToString(type);
  }

  function signatureOfSymbol(symbol: Symbol): string | undefined {
    const declaration = symbol.valueDeclaration ?? symbol.declarations?.[0];
    return declaration ? signatureOfDeclaration(declaration) : undefined;
  }

  function signatureOfDeclaration(declaration: Node): string | undefined {
    if (
      declaration.kind !== SyntaxKind.MethodDeclaration &&
      declaration.kind !== SyntaxKind.ConstructorDeclaration
    ) {
      return undefined;
    }
    const m = declaration as MethodDeclaration;
    const parts: string[] = [];
    if (m.typeParameters && m.typeParameters.length > 0) {
      parts.push(`<${m.typeParameters.map(nodeSourceText).join(", ")}>`);
    }
    if (declaration.kind === SyntaxKind.MethodDeclaration) parts.push(nodeSourceText(m.returnType));
    const params = m.parameters.map(nodeSourceText).join(", ");
    let signature = `${parts.join(" ")}${parts.length > 0 ? " " : ""}${m.name.text}(${params})`;
    if (m.throws && m.throws.length > 0) {
      signature += ` throws ${m.throws.map(nodeSourceText).join(", ")}`;
    }
    return signature;
  }

  // Clean a raw `/** ... */` block to plain text: drop the delimiters and the
  // leading "* " on each line, collapse the blank edges.
  function cleanJavadoc(raw: string): string {
    const body = raw.slice(3, -2); // strip "/**" and "*/"
    return body
      .split("\n")
      .map(line => line.replace(/^\s*\*? ?/, "").trimEnd())
      .join("\n")
      .trim();
  }

  function getDocumentationOfNode(node: Node): string | undefined {
    const text = getSourceFileOfNode(node).text;
    // The doc comment sits in the declaration's leading trivia: [pos, tokenStart).
    const leading = text.slice(node.pos, skipTrivia(text, node.pos));
    const blocks = leading.match(/\/\*\*[\s\S]*?\*\//g);
    if (!blocks) return undefined;
    const doc = cleanJavadoc(blocks[blocks.length - 1]!); // nearest to the declaration
    return doc.length > 0 ? doc : undefined;
  }

  function getDocumentation(symbol: Symbol): string | undefined {
    const declaration = symbol.valueDeclaration ?? symbol.declarations?.[0];
    return declaration ? getDocumentationOfNode(declaration) : undefined;
  }

  // The class type a member access on `type` resolves against: the type itself,
  // or - for a type variable - its leftmost bound (JLS 4.4: the members of T are
  // the members of its bound), unwrapped recursively for `U extends T` chains.
  function receiverClassType(type: Type, depth = 0): ClassType | undefined {
    if (type.kind === TypeKind.Class) return type as ClassType;
    if (type.kind === TypeKind.TypeVariable && type.bound && depth < 8) {
      return receiverClassType(type.bound, depth + 1);
    }
    return undefined;
  }

  function resolveMemberAccess(access: PropertyAccessExpression): Symbol | undefined {
    const targetType = getTypeOfExpression(access.expression);
    if (targetType.kind === TypeKind.Array) return arrayMember(access.name.text);
    const receiver = receiverClassType(targetType);
    if (!receiver) return undefined;
    return lookupMember(receiver.symbol, access.name.text, Meaning.Any, program);
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
    const direct = resolveIdentifier(identifier, program);
    if (direct) return direct;
    // Fallback: a segment of a qualified name (java.util.List) - resolve it as
    // the type it names, or as a package / package prefix.
    return resolveQualifiedSegment(identifier);
  }

  // The dotted prefix a qualified-name segment denotes: the whole left..id chain
  // when id is the `right`, or just id when it is the leftmost root.
  function qualifiedPrefix(identifier: Identifier): string | undefined {
    const parent = identifier.parent;
    if (!parent || parent.kind !== SyntaxKind.QualifiedName) return undefined;
    const qn = parent as QualifiedName;
    if (qn.right === identifier) return entityNameToString(qn);
    if (qn.left === identifier) return identifier.text;
    return undefined;
  }

  function resolveQualifiedSegment(identifier: Identifier): Symbol | undefined {
    const prefix = qualifiedPrefix(identifier);
    if (!prefix) return undefined;
    const index = program.getGlobalIndex();
    return index.getType(prefix) ?? index.getPackageByName(prefix);
  }

  function numericLiteralType(value: string): Type {
    const v = value.replace(/_/g, "");
    // In hex/binary integer literals the letters a-f are digits, not type
    // suffixes; only a trailing L counts (and a 'p' marks a hex float).
    if (/^0[xXbB]/.test(v)) {
      if (/[pP]/.test(v)) return primitiveType("double");
      return /[lL]$/.test(v) ? primitiveType("long") : intType;
    }
    if (/[lL]$/.test(v)) return primitiveType("long");
    if (/[fF]$/.test(v)) return primitiveType("float");
    if (/[dD]$/.test(v) || /[.eE]/.test(v)) return primitiveType("double");
    return intType;
  }

  // Binary numeric promotion (JLS 5.6.2): byte/short/char promote to int, then the
  // result is the wider of the two operand types.
  function widerNumeric(a: Type, b: Type): Type {
    const order = ["int", "long", "float", "double"];
    const rank = (t: Type) => {
      if (t.kind !== TypeKind.Primitive) return -1;
      const promoted = t.name === "byte" || t.name === "short" || t.name === "char" ? "int" : t.name;
      return order.indexOf(promoted);
    };
    const ra = rank(a);
    const rb = rank(b);
    if (ra < 0 && rb < 0) return errorType;
    return primitiveType(order[Math.max(ra, rb)]!);
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
        // Qualified `Outer.this` has the type of the named enclosing class.
        const qualifier = (node as { qualifier?: Node }).qualifier;
        if (qualifier) return getTypeOfExpression(qualifier);
        const enclosing = enclosingTypeSymbol(node);
        return enclosing ? classType(enclosing) : errorType;
      }
      case SyntaxKind.SuperExpression: {
        // `super` has the type of the enclosing class's direct superclass, so a
        // member lookup on it finds the inherited (overridden) member.
        const enclosing = enclosingTypeSymbol(node);
        const decl = enclosing?.valueDeclaration ?? enclosing?.declarations?.[0];
        const ext = decl ? (decl as { extendsType?: TypeNode }).extendsType : undefined;
        if (ext) {
          const base = resolveType(ext, decl!);
          if (base.kind === TypeKind.Class) return base;
        }
        return classTypeByFqn("java.lang.Object");
      }
      case SyntaxKind.ParenthesizedExpression:
        return getTypeOfExpression((node as ParenthesizedExpression).expression);
      case SyntaxKind.CastExpression:
        return resolveType((node as CastExpression).type, node);
      case SyntaxKind.PropertyAccessExpression:
        return typeOfMemberAccess(node as PropertyAccessExpression);
      case SyntaxKind.CallExpression:
        return typeOfCall(node as CallExpression);
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
        // The conditional's type (JLS 15.25): binary numeric promotion for numeric
        // arms, otherwise a simplified reference lub (the more general arm, or a
        // null arm yields the other, else java.lang.Object).
        const t = getTypeOfExpression((node as ConditionalExpression).whenTrue);
        const f = getTypeOfExpression((node as ConditionalExpression).whenFalse);
        if (t.kind === TypeKind.Error) return f;
        if (f.kind === TypeKind.Error) return t;
        if (t.kind === TypeKind.Null) return f;
        if (f.kind === TypeKind.Null) return t;
        const num = widerNumeric(t, f);
        if (num.kind !== TypeKind.Error) return num;
        if (isAssignableTo(f, t, false)) return t;
        if (isAssignableTo(t, f, false)) return f;
        return classTypeByFqn("java.lang.Object");
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
        // A shift's type is the unary-promoted LEFT operand; the distance never
        // widens the result (JLS 15.19), so `int << long` is int, not long.
        if (
          b.operatorToken === SyntaxKind.LessThanLessThanToken ||
          b.operatorToken === SyntaxKind.GreaterThanGreaterThanToken ||
          b.operatorToken === SyntaxKind.GreaterThanGreaterThanGreaterThanToken
        ) {
          return widerNumeric(left, intType);
        }
        // `&`, `|`, `^` on boolean operands are the boolean logical operators and
        // yield boolean (JLS 15.22.2); on integral operands they are bitwise.
        const isBool = (t: Type): boolean =>
          t.kind === TypeKind.Primitive && (t as { name: string }).name === "boolean";
        if (
          (b.operatorToken === SyntaxKind.AmpersandToken ||
            b.operatorToken === SyntaxKind.BarToken ||
            b.operatorToken === SyntaxKind.CaretToken) &&
          isBool(left) &&
          isBool(right)
        ) {
          return booleanType;
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
        // A value is assignable to a type-variable target when inference can bind
        // it (we do not run full inference, so accept any reference/boxable value
        // - lenient, never a false error). This also makes a generic parameter
        // applicable during overload resolution, e.g. List.add(E) for add("x").
        return source.kind !== TypeKind.Primitive || allowBoxing;
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
        // Most-specific (JLS 15.12.2.5). Candidates are ordered most-derived-first
        // (a type's own members before inherited ones), so only a *strictly* more
        // specific method displaces the current best; equally-specific methods
        // (e.g. an override and the method it overrides - String.length() vs
        // CharSequence.length()) keep the more-derived one already chosen.
        let best = ok[0]!;
        for (const d of ok.slice(1)) if (moreSpecific(d, best)) best = d;
        return best;
      }
    }
    return decls[0]!;
  }

  interface CallInfo {
    readonly decl: MethodDeclaration;
    // Substitution from the receiver's type arguments (for instance calls).
    readonly receiverSubst: Map<Symbol, Type>;
  }

  // Overload resolution is deterministic per node, and the emitter asks for the
  // same call's target at least twice (once to dispatch, once for its type), so
  // memoize like getTypeOfExpression does. `null` = resolved to nothing.
  const callInfoCache = new WeakMap<CallExpression, CallInfo | null>();

  function resolveCallInfo(call: CallExpression): CallInfo | undefined {
    const cached = callInfoCache.get(call);
    if (cached !== undefined) return cached ?? undefined;
    const result = resolveCallInfoWorker(call);
    callInfoCache.set(call, result ?? null);
    return result;
  }

  function resolveCallInfoWorker(call: CallExpression): CallInfo | undefined {
    const callee = call.expression;
    let symbol: Symbol | undefined;
    let receiverSubst = new Map<Symbol, Type>();
    if (callee.kind === SyntaxKind.Identifier) {
      symbol = resolveIdentifier(callee as Identifier, program);
    } else if (callee.kind === SyntaxKind.PropertyAccessExpression) {
      const access = callee as PropertyAccessExpression;
      // A type-variable receiver resolves members against its bound (JLS 4.4).
      const receiver = receiverClassType(getTypeOfExpression(access.expression));
      if (receiver) {
        // Gather overloads across the whole hierarchy and pick by applicability,
        // so an override/overload in a subtype does not hide inherited ones.
        const cands = collectTypedOverloads(receiver, access.name.text);
        if (cands.length === 0) return undefined;
        const decl =
          cands.length === 1
            ? cands[0]!.decl
            : chooseOverload(
                cands.map(c => c.decl),
                call.arguments.map(getTypeOfExpression),
              );
        return { decl, receiverSubst: cands.find(c => c.decl === decl)!.subst };
      }
      symbol = resolveMemberAccess(access);
    }
    if (!symbol) return undefined;
    const decls = (symbol.declarations ?? []).filter(
      d => d.kind === SyntaxKind.MethodDeclaration,
    ) as MethodDeclaration[];
    if (decls.length === 0) return undefined;
    const decl =
      decls.length === 1
        ? decls[0]!
        : chooseOverload(decls, call.arguments.map(getTypeOfExpression));
    return { decl, receiverSubst };
  }

  function resolveCall(call: CallExpression): MethodDeclaration | undefined {
    return resolveCallInfo(call)?.decl;
  }

  // The method type-parameter symbols a generic method declares (its own <T>).
  function methodTypeParameters(decl: MethodDeclaration): Set<Symbol> {
    const out = new Set<Symbol>();
    for (const tp of decl.typeParameters ?? []) if (tp.symbol) out.add(tp.symbol);
    return out;
  }

  // A primitive argument can never bind a type variable (those are reference
  // types), so box it - id(1) infers T = Integer, matching JLS 18.
  function boxIfPrimitive(type: Type): Type {
    if (type.kind === TypeKind.Primitive) {
      const boxed = BOX[type.name];
      if (boxed) return classTypeByFqn(boxed);
    }
    return type;
  }

  // Collect bindings for `vars` by matching a parameter type against an argument
  // type (JLS 18, pragmatic): first binding wins, gaps stay unbound.
  function unify(param: Type, arg: Type, vars: Set<Symbol>, out: Map<Symbol, Type>): void {
    switch (param.kind) {
      case TypeKind.TypeVariable:
        if (
          vars.has(param.symbol) &&
          !out.has(param.symbol) &&
          !isError(arg) &&
          arg.kind !== TypeKind.Null
        ) {
          out.set(param.symbol, arg);
        }
        return;
      case TypeKind.Class:
        if (arg.kind === TypeKind.Class && param.symbol === arg.symbol) {
          param.typeArguments.forEach((pa, i) =>
            unify(pa, arg.typeArguments[i] ?? errorType, vars, out),
          );
        }
        return;
      case TypeKind.Array:
        if (arg.kind === TypeKind.Array) unify(param.elementType, arg.elementType, vars, out);
        return;
      default:
        return;
    }
  }

  function inferMethodTypeArguments(
    decl: MethodDeclaration,
    argTypes: Type[],
    receiverSubst: Map<Symbol, Type>,
    vars: Set<Symbol>,
  ): Map<Symbol, Type> {
    const out = new Map<Symbol, Type>();
    decl.parameters.forEach((p, i) => {
      if (i >= argTypes.length) return;
      const paramType = substitute(resolveType(p.type, decl), receiverSubst);
      unify(paramType, boxIfPrimitive(argTypes[i]!), vars, out);
    });
    return out;
  }

  // The type of the synthesized enum statics E.values() (E[]) and
  // E.valueOf(String) (E), which have no source declaration.
  function enumStaticCallType(call: CallExpression): Type | undefined {
    const callee = call.expression;
    if (callee.kind !== SyntaxKind.PropertyAccessExpression) return undefined;
    const access = callee as PropertyAccessExpression;
    if (access.expression.kind !== SyntaxKind.Identifier) return undefined;
    const sym = resolveTypeEntityName(access.expression as Identifier, access.expression, program);
    if (!sym || !(sym.flags & SymbolFlags.Enum)) return undefined;
    const t = classType(sym);
    if (access.name.text === "values" && call.arguments.length === 0) return arrayType(t);
    if (access.name.text === "valueOf" && call.arguments.length === 1) return t;
    return undefined;
  }

  function typeOfCall(call: CallExpression): Type {
    // An array's clone() is covariant (JLS 10.7): it returns the array's type.
    const callee = call.expression;
    if (callee.kind === SyntaxKind.PropertyAccessExpression && call.arguments.length === 0) {
      const pa = callee as PropertyAccessExpression;
      if (pa.name.text === "clone") {
        const recv = getTypeOfExpression(pa.expression);
        if (recv.kind === TypeKind.Array) return recv;
      }
    }
    // values()/valueOf(String) take precedence over the inherited Enum.valueOf.
    const enumType = enumStaticCallType(call);
    if (enumType) return enumType;
    const info = resolveCallInfo(call);
    if (!info) return errorType;
    let returnType = substitute(resolveType(info.decl.returnType, info.decl), info.receiverSubst);
    const vars = methodTypeParameters(info.decl);
    if (vars.size > 0) {
      const argTypes = call.arguments.map(getTypeOfExpression);
      returnType = substitute(
        returnType,
        inferMethodTypeArguments(info.decl, argTypes, info.receiverSubst, vars),
      );
    }
    return returnType;
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
      // TODO: a `return` inside a lambda targets the SAM's return type (JLS 15.27.2
      // / 9.8). We bail to undefined instead of inferring it from the lambda's
      // target functional interface.
      if (current.kind === SyntaxKind.LambdaExpression) return undefined;
      current = current.parent;
    }
    return undefined;
  }

  // --- @Override (JLS 9.6.4.4) -------------------------------------------------------

  function hasOverrideAnnotation(decl: MethodDeclaration): boolean {
    for (const modifier of decl.modifiers ?? []) {
      if (modifier.kind !== SyntaxKind.Annotation) continue;
      const name = entityNameToString((modifier as Annotation).typeName);
      if (name === "Override" || name.endsWith(".Override")) return true;
    }
    return false;
  }

  // "ok" if some supertype (incl. Object) declares a method of the same name,
  // "missing" if the whole hierarchy is known and none does, "unknown" if any
  // supertype is unresolved (so we never flag on incomplete information).
  function overrideStatus(decl: MethodDeclaration): "ok" | "missing" | "unknown" {
    const enclosing = enclosingTypeSymbol(decl);
    if (!enclosing) return "unknown";
    const name = decl.name.text;
    const seen = new Set<Symbol>();
    let incomplete = false;

    const declaresMethod = (typeSymbol: Symbol): boolean => {
      const member = typeSymbol.members?.get(name);
      return !!member && (member.flags & SymbolFlags.Method) !== 0;
    };
    const search = (typeSymbol: Symbol): boolean => {
      if (seen.has(typeSymbol)) return false;
      seen.add(typeSymbol);
      const declaration = declarationOf(typeSymbol);
      if (!declaration) {
        incomplete = true;
        return false;
      }
      for (const typeNode of superTypeNodesOf(declaration)) {
        if (typeNode.kind !== SyntaxKind.TypeReference) {
          incomplete = true;
          continue;
        }
        const superSymbol = resolveTypeEntityName(
          (typeNode as TypeReference).typeName,
          declaration,
          program,
        );
        if (!superSymbol) {
          incomplete = true;
          continue;
        }
        if (declaresMethod(superSymbol) || search(superSymbol)) return true;
      }
      return false;
    };

    if (search(enclosing)) return "ok";
    // Every type implicitly extends Object; require it to be known to decide.
    const objectSymbol = program.getGlobalIndex().getType("java.lang.Object");
    if (!objectSymbol) return "unknown";
    if (declaresMethod(objectSymbol)) return "ok";
    return incomplete ? "unknown" : "missing";
  }

  // --- switch-expression exhaustiveness over enums (JLS 14.11.1.1) -------------------

  function missingEnumLabels(sw: SwitchExpression): string[] | undefined {
    const selector = getTypeOfExpression(sw.expression);
    if (selector.kind !== TypeKind.Class || !(selector.symbol.flags & SymbolFlags.Enum)) {
      return undefined;
    }
    const declaration = declarationOf(selector.symbol);
    if (!declaration || declaration.kind !== SyntaxKind.EnumDeclaration) return undefined;
    const constants = (declaration as EnumDeclaration).enumConstants.map(c => c.name.text);
    if (constants.length === 0) return undefined;

    const covered = new Set<string>();
    for (const clause of sw.clauses) {
      if (clause.isDefault || clause.guard) return undefined; // default / guard: do not reason
      for (const label of clause.labels ?? []) {
        if (label.kind !== SyntaxKind.Identifier) return undefined; // non-constant label
        covered.add((label as Identifier).text);
      }
    }
    return constants.filter(c => !covered.has(c));
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
        case SyntaxKind.MethodDeclaration: {
          const m = node as MethodDeclaration;
          if (hasOverrideAnnotation(m) && overrideStatus(m) === "missing") {
            diagnostics.push(
              createDiagnostic(
                m.name.pos,
                m.name.end - m.name.pos,
                Diagnostics.Method_does_not_override_a_supertype_method,
              ),
            );
          }
          break;
        }
        case SyntaxKind.PropertyAccessExpression: {
          const access = node as PropertyAccessExpression;
          // super.* is modeled imprecisely (super resolves to Object), so skip it
          // to avoid false positives on inherited members.
          if (access.expression.kind !== SyntaxKind.SuperExpression) {
            const receiver = getTypeOfExpression(access.expression);
            if (
              receiver.kind === TypeKind.Class &&
              isClosedType(receiver as ClassType) &&
              !lookupTypedMember(receiver as ClassType, access.name.text)
            ) {
              const start = skipTrivia(getSourceFileOfNode(access.name).text, access.name.pos);
              diagnostics.push(
                createDiagnostic(
                  start,
                  access.name.end - start,
                  Diagnostics.Cannot_resolve_member_0_in_1,
                  access.name.text,
                  typeToString(receiver),
                ),
              );
            }
          }
          break;
        }
        case SyntaxKind.SwitchExpression: {
          const sw = node as SwitchExpression;
          const missing = missingEnumLabels(sw);
          if (missing && missing.length > 0) {
            diagnostics.push(
              createDiagnostic(
                sw.expression.pos,
                sw.expression.end - sw.expression.pos,
                Diagnostics.Switch_expression_not_exhaustive_0,
                typeToString(getTypeOfExpression(sw.expression)),
              ),
            );
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
    getLambdaInfo,
    getMethodRefInfo,
    getTypeOfSymbol,
    getTypeOfExpression,
    resolveName,
    isAssignableTo,
    resolveCall,
    typeStringOfSymbol,
    signatureOfSymbol,
    signatureOfDeclaration,
    getDocumentation,
    getDocumentationOfNode,
    getSemanticDiagnostics,
  };
}
