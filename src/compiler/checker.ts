// Type checker. Resolves AST type nodes to the Type model, computes the type of
// expressions, and resolves member access (a.b). This milestone (P5) covers
// declared types, the common expression forms and member typing - enough for
// hover and as the base for assignability/overloads/inference (P6-P8).
// Everything unknown degrades to errorType.
import { type Brand } from "../brand.ts";
import { foldConstant } from "./constfold.ts";

import {
  type ArrayType,
  arrayType,
  type ClassType,
  classType,
  errorType,
  isError,
  nullnessOf,
  nullType,
  primitiveType,
  type Type,
  TypeKind,
  typeToString,
  typeVariable,
  type WildcardType,
  withNullness,
} from "./checkerTypes.ts";
import { createDiagnostic, Diagnostics } from "./diagnostics.ts";
import { checkDateTimePattern } from "./dateTimePattern.ts";
import { type ArgTypeDescriptor, conversionAccepts, parseFormatString } from "./formatString.ts";
import { isParseableInteger, MAX_RADIX, MIN_RADIX } from "./numberParse.ts";
import { forEachChild } from "./parser.ts";
import { validateRegex } from "./regexValidate.ts";
import type { Fqn, PackageName, Program } from "./program.ts";
import {
  getDirectSuperTypeSymbols,
  getSourceFileOfNode,
  lookupMember,
  Meaning,
  resolveIdentifier,
  resolveTypeEntityName,
} from "./resolver.ts";
import {
  type Annotation,
  type ArrayCreationExpression,
  type ArrayInitializer,
  type ArrayType as AstArrayType,
  type AssignmentExpression,
  type BinaryExpression,
  type CallExpression,
  type CastExpression,
  type ConditionalExpression,
  type Diagnostic,
  type ElementAccessExpression,
  type EnumDeclaration,
  type ForEachStatement,
  type Identifier,
  type ImportDeclaration,
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
  type SwitchClause,
  type SwitchExpression,
  type SwitchStatement,
  type Parameter,
  type Symbol,
  SymbolFlags,
  SyntaxKind,
  type TypeNode,
  type TypeParameter,
  type TypeReference,
  type VariableDeclarator,
  type WildcardType as AstWildcardType,
  type ClassDeclaration,
  type ConstructorDeclaration,
  type RecordDeclaration,
} from "./types.ts";
import { entityNameToString, skipTrivia, tokenToString } from "./utilities.ts";
import { type DeprecatedUse, readDeprecation } from "./deprecation.ts";
import {
  type Nullness,
  type NullnessAnnotations,
  type NullnessOptions,
  hasNullnessAnnotation,
  isReferenceTypeNode,
  readDeclaredNullness,
  resolveNullnessAnnotations,
  typeUseNullness,
} from "./nullness.ts";
import { narrowNullnessAt } from "./narrowing.ts";

/** A single abstract method's name (the invokedynamic call name for a lambda). */
export type SamName = Brand<string, "SamName">;

/** What the emitter needs to lower a lambda expression (see getLambdaInfo). */
export interface LambdaInfo {
  /** The target functional interface (a Class type). */
  readonly interfaceType: Type;
  /** The single abstract method's name (the invokedynamic call name). */
  readonly samName: SamName;
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
  /** Every overload declaration a call could bind to (for signature help). */
  resolveCallCandidates(call: CallExpression): MethodDeclaration[];
  /**
   * The resolved overload's signature with the receiver's type arguments (and
   * inferred method type arguments) substituted in - `String get(int index)` for
   * a call on a List<String> - or undefined when nothing instantiates.
   */
  instantiatedSignatureOfCall(call: CallExpression): string | undefined;
  /** The source text of each parameter of a method/constructor declaration. */
  parameterLabelsOf(declaration: Node): string[];
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
  /** Every use of a @Deprecated method or type in a source file (for the MCP). */
  getDeprecatedUses(sourceFile: SourceFile): DeprecatedUse[];
}

// Primitive widening (JLS 5.1.2) and boxing (JLS 5.1.7).
const WIDENING = {
  byte: ["short", "int", "long", "float", "double"],
  short: ["int", "long", "float", "double"],
  char: ["int", "long", "float", "double"],
  int: ["long", "float", "double"],
  long: ["float", "double"],
  float: ["double"],
} as const satisfies Record<string, readonly string[]>;
const BOX = {
  boolean: "java.lang.Boolean",
  byte: "java.lang.Byte",
  short: "java.lang.Short",
  char: "java.lang.Character",
  int: "java.lang.Integer",
  long: "java.lang.Long",
  float: "java.lang.Float",
  double: "java.lang.Double",
} as const satisfies Record<string, string>;
const UNBOX: Record<string, string> = Object.fromEntries(
  Object.entries(BOX).map(([prim, fqn]) => [fqn, prim]),
);

function primitiveWidens(from: string, to: string): boolean {
  const wider: readonly string[] | undefined = WIDENING[from as keyof typeof WIDENING];
  return from === to || (wider?.includes(to) ?? false);
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

/**
 * Single-type imports (non-static, non-on-demand) whose simple name never
 * appears in the file body. Conservative: ANY identifier occurrence counts as
 * a use, so a used import is never flagged (the same rule "Organize imports"
 * applies when dropping imports).
 */
export function findUnusedImports(sourceFile: SourceFile): ImportDeclaration[] {
  if (sourceFile.imports.length === 0) return [];
  const used = new Set<string>();
  const collect = (node: Node): void => {
    if (node.kind === SyntaxKind.Identifier) used.add((node as Identifier).text);
    forEachChild(node, child => {
      collect(child);
      return undefined;
    });
  };
  for (const statement of sourceFile.statements) collect(statement);

  const unused: ImportDeclaration[] = [];
  const seen = new Set<string>();
  for (const imp of sourceFile.imports) {
    const fqn = entityNameToString(imp.name);
    // An exact repeat of an earlier import is redundant whatever the usage.
    const key = `${imp.isStatic ? "static " : ""}${fqn}${imp.isOnDemand ? ".*" : ""}`;
    if (seen.has(key)) {
      unused.push(imp);
      continue;
    }
    seen.add(key);
    // On-demand imports stay unjudged: what they contribute is open-ended.
    // For single imports the simple name is what becomes referencable - the
    // type name, or the member name for a static import.
    if (imp.isOnDemand) continue;
    if (!used.has(fqn.slice(fqn.lastIndexOf(".") + 1))) unused.push(imp);
  }
  return unused;
}

// Lookup tables for getSemanticDiagnostics, hoisted so they are not rebuilt per file.
const NARROWING_RANGE: Record<string, readonly [bigint, bigint]> = {
  byte: [-128n, 127n],
  short: [-32768n, 32767n],
  char: [0n, 65535n],
};
const FORMAT_METHODS = new Map<string, { fmtIsReceiver: boolean }>([
  ["java.lang.String#format", { fmtIsReceiver: false }],
  ["java.lang.String#formatted", { fmtIsReceiver: true }],
  ["java.io.PrintStream#format", { fmtIsReceiver: false }],
  ["java.io.PrintStream#printf", { fmtIsReceiver: false }],
  ["java.io.PrintWriter#format", { fmtIsReceiver: false }],
  ["java.io.PrintWriter#printf", { fmtIsReceiver: false }],
  ["java.io.Console#format", { fmtIsReceiver: false }],
  ["java.io.Console#printf", { fmtIsReceiver: false }],
  ["java.util.Formatter#format", { fmtIsReceiver: false }],
]);
const REGEX_METHODS = new Set([
  "java.util.regex.Pattern#compile",
  "java.util.regex.Pattern#matches",
  "java.lang.String#matches",
  "java.lang.String#split",
  "java.lang.String#replaceAll",
  "java.lang.String#replaceFirst",
]);
const PARSE_METHODS = new Map<string, string>([
  ["java.lang.Integer#parseInt", "int"],
  ["java.lang.Integer#valueOf", "int"],
  ["java.lang.Long#parseLong", "long"],
  ["java.lang.Long#valueOf", "long"],
  ["java.lang.Short#parseShort", "short"],
  ["java.lang.Short#valueOf", "short"],
  ["java.lang.Byte#parseByte", "byte"],
  ["java.lang.Byte#valueOf", "byte"],
]);

export function createChecker(program: Program, nullness?: NullnessOptions): Checker {
  const symbolTypes = new WeakMap<Symbol, Type>();
  const expressionTypes = new WeakMap<Node, Type>();

  // jspecify nullness checking (nikeee/cappu#25); undefined when disabled. When
  // enabled, resolveType attaches a nullness facet to reference types and the
  // semantic pass warns on a possibly-null value reaching a non-null position.
  const nullnessAnnotations: NullnessAnnotations | undefined = nullness?.enabled
    ? resolveNullnessAnnotations(nullness)
    : undefined;
  // Cross-file @NullMarked: whether a package's package-info.java marks it, cached.
  const packageNullMarked = new Map<string, boolean>();

  // Whether a package is marked by its package-info.java (a different source file).
  // Java only allows package annotations there, so match the file name exactly.
  function packageIsNullMarked(packageName: string): boolean {
    const a = nullnessAnnotations!;
    let marked = packageNullMarked.get(packageName);
    if (marked === undefined) {
      marked = false;
      for (const uri of program.getAllUris()) {
        if (!uri.endsWith("package-info.java")) continue;
        const sf = program.getSourceFile(uri);
        const pkg = sf?.packageDeclaration;
        if (!pkg || entityNameToString(pkg.name) !== packageName) continue;
        if (hasNullnessAnnotation(pkg, a.nullUnmarked)) marked = false;
        else if (hasNullnessAnnotation(pkg, a.nullMarked)) marked = true;
        break;
      }
      packageNullMarked.set(packageName, marked);
    }
    return marked;
  }

  // Is `node` inside a @NullMarked scope? The nearest enclosing @NullMarked /
  // @NullUnmarked on the declaration, an enclosing type, this file's package
  // declaration, or the package's package-info.java wins.
  function isNullMarked(node: Node): boolean {
    const a = nullnessAnnotations!;
    for (let n: Node | undefined = node; n; n = n.parent) {
      if (hasNullnessAnnotation(n, a.nullUnmarked)) return false;
      if (hasNullnessAnnotation(n, a.nullMarked)) return true;
      if (n.kind === SyntaxKind.SourceFile) {
        const pkg = (n as SourceFile).packageDeclaration;
        if (hasNullnessAnnotation(pkg, a.nullUnmarked)) return false;
        if (hasNullnessAnnotation(pkg, a.nullMarked)) return true;
        return packageIsNullMarked(pkg ? entityNameToString(pkg.name) : "");
      }
    }
    return false;
  }

  // The nullness a type node carries at a use site: an explicit type-use
  // annotation (List<@Nullable T>) wins, else a reference type in a @NullMarked
  // scope is non-null, else unknown.
  function typeNodeNullness(typeNode: TypeNode, fromNode: Node): Nullness | undefined {
    const explicit = typeUseNullness(typeNode, nullnessAnnotations!);
    if (explicit) return explicit;
    if (!isReferenceTypeNode(typeNode)) return undefined;
    return isNullMarked(fromNode) ? "nonNull" : undefined;
  }

  // The provable nullness of a value expression, used when narrowing a reassignment
  // (x = e) - the `null` literal and the intrinsically non-null forms are decided
  // syntactically; everything else falls back to the value's type facet.
  function exprNullness(node: Node): Nullness | undefined {
    switch (node.kind) {
      case SyntaxKind.NullKeyword:
        return "nullable";
      case SyntaxKind.StringLiteral:
      case SyntaxKind.TextBlockLiteral:
      case SyntaxKind.CharacterLiteral:
      case SyntaxKind.NumericLiteral:
      case SyntaxKind.TrueKeyword:
      case SyntaxKind.FalseKeyword:
      case SyntaxKind.ObjectCreationExpression:
      case SyntaxKind.ArrayCreationExpression:
      case SyntaxKind.ThisExpression:
        return "nonNull";
      default:
        return nullnessOf(getTypeOfExpression(node));
    }
  }

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

  // The trusted dotted-name boundary: literal JDK names and BOX entries enter here.
  function classTypeByFqn(fqn: string): Type {
    const symbol = program.getGlobalIndex().getType(fqn as Fqn);
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
          : withNullness(
              classType(
                type.symbol,
                type.typeArguments.map(a => substitute(a, map)),
              ),
              type.nullness,
            );
      case TypeKind.Array:
        return withNullness(arrayType(substitute(type.elementType, map)), type.nullness);
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
  // transitive super type resolve to a declaration we modeled. Records are
  // closed (the binder synthesizes the component accessors; equals/hashCode/
  // toString come from Object) and so are enums (name/ordinal/... come from
  // java.lang.Enum, the constants are bound; the only synthesized members we do
  // not bind are the static values()/valueOf(), special-cased at the use site).
  // Annotations are excluded: their element methods are not modeled. Only closed
  // types are eligible for the unresolved-member diagnostic, so an unmodeled
  // super type never yields a false positive.
  function isClosedType(type: ClassType): boolean {
    if (type.symbol.flags & SymbolFlags.Annotation) {
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

  // values() and valueOf(String) are synthesized by the compiler for every enum
  // (JLS 8.9.3) and are not present in source, so the binder never records them.
  // Recognize them so member checking on a closed enum does not flag them.
  function isSynthesizedEnumMember(type: ClassType, name: string): boolean {
    return (
      Boolean(type.symbol.flags & SymbolFlags.Enum) && (name === "values" || name === "valueOf")
    );
  }

  function resolveType(typeNode: TypeNode, fromNode: Node): Type {
    // jspecify nullness facet (nikeee/cappu#25), attached only when enabled.
    const withTypeNullness = <T extends Type>(type: T): T =>
      nullnessAnnotations ? withNullness(type, typeNodeNullness(typeNode, fromNode)) : type;
    switch (typeNode.kind) {
      case SyntaxKind.PrimitiveType:
        return primitiveType(
          tokenToString((typeNode as { keyword: SyntaxKind }).keyword) ?? "<error>",
        );
      case SyntaxKind.ArrayType:
        return withTypeNullness(
          arrayType(resolveType((typeNode as AstArrayType).elementType, fromNode)),
        );
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
        if (symbol.flags & SymbolFlags.TypeParameter)
          return withTypeNullness(getTypeOfSymbol(symbol));
        const args = ref.typeArguments?.map(a => resolveType(a as TypeNode, fromNode)) ?? [];
        return withTypeNullness(classType(symbol, args));
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
          // C-style array brackets after the name (char buf[]) add rank the
          // type node does not carry (JLS 10.2).
          let rank = (declared.from as { arrayRankAfterName?: number }).arrayRankAfterName ?? 0;
          while (rank-- > 0) type = arrayType(type);
        }
      } else {
        // A concise lambda parameter (x -> ...) has no written type; infer it
        // from the target functional interface.
        type = inferLambdaParameterType(symbol);
      }
    }
    // A declaration-level @Nullable/@NonNull (on the modifiers, e.g. `@NonNull
    // String s`, not the type node) overrides the type's facet (nikeee/cappu#25).
    if (nullnessAnnotations) {
      const decl = symbol.valueDeclaration ?? symbol.declarations?.[0];
      type = withNullness(type, readDeclaredNullness(decl, nullnessAnnotations));
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
  // --- method-level type-argument inference (a pragmatic JLS 18 subset) ------
  // A generic METHOD's type parameters (Collectors.toMap's T, Stream.toArray's
  // A) are not bound by the receiver substitution, so the lambdas and method
  // references of stream pipelines never got parameter types. Bindings come
  // from structurally unifying (1) each non-functional argument's declared
  // parameter type against its actual type and (2) the declared return type
  // against the call's context type (outer call parameter, assigned variable,
  // enclosing return). Whatever does not unify stays unbound - never a guess.

  function methodTypeParamSymbols(decl: MethodDeclaration): Set<Symbol> | undefined {
    if (!decl.typeParameters || decl.typeParameters.length === 0) return undefined;
    const out = new Set<Symbol>();
    for (const p of decl.typeParameters) if (p.symbol) out.add(p.symbol);
    return out.size > 0 ? out : undefined;
  }

  function unifyInto(
    pattern: Type,
    actual: Type,
    vars: ReadonlySet<Symbol>,
    bindings: Map<Symbol, Type>,
  ): void {
    // wildcards on either side: their bounds carry the information
    if (pattern.kind === TypeKind.Wildcard) {
      if (pattern.bound) unifyInto(pattern.bound, actual, vars, bindings);
      return;
    }
    if (actual.kind === TypeKind.Wildcard) {
      if (actual.bound) unifyInto(pattern, actual.bound, vars, bindings);
      return;
    }
    if (pattern.kind === TypeKind.TypeVariable && vars.has(pattern.symbol)) {
      if (!bindings.has(pattern.symbol) && isConcrete(actual)) bindings.set(pattern.symbol, actual);
      return;
    }
    if (
      pattern.kind === TypeKind.Class &&
      actual.kind === TypeKind.Class &&
      pattern.symbol === actual.symbol
    ) {
      const shared = Math.min(pattern.typeArguments.length, actual.typeArguments.length);
      for (let i = 0; i < shared; i++) {
        unifyInto(pattern.typeArguments[i]!, actual.typeArguments[i]!, vars, bindings);
      }
      return;
    }
    if (pattern.kind === TypeKind.Array && actual.kind === TypeKind.Array) {
      unifyInto(pattern.elementType, actual.elementType, vars, bindings);
    }
  }

  // The type the call's RESULT flows into, as far as it is locally evident.
  function contextualTypeOfCall(call: CallExpression): Type | undefined {
    const parent = call.parent;
    if (parent.kind === SyntaxKind.VariableDeclarator && parent.symbol) {
      return getTypeOfSymbol(parent.symbol);
    }
    if (parent.kind === SyntaxKind.ReturnStatement) {
      return enclosingReturnType(parent);
    }
    if (parent.kind === SyntaxKind.CallExpression) {
      const outer = parent as CallExpression;
      const index = outer.arguments.indexOf(call);
      if (index < 0) return undefined;
      const info = resolveCallInfo(outer);
      const param = info?.decl.parameters[index] as { type?: TypeNode } | undefined;
      return param?.type
        ? substitute(resolveType(param.type, info!.decl), info!.receiverSubst)
        : undefined;
    }
    return undefined;
  }

  function inferredMethodSubst(call: CallExpression, info: CallInfo): Map<Symbol, Type> {
    const bindings = new Map<Symbol, Type>();
    const vars = methodTypeParamSymbols(info.decl);
    if (!vars) return bindings;
    call.arguments.forEach((argument, i) => {
      // functional arguments depend on this very inference: no constraints
      if (
        argument.kind === SyntaxKind.LambdaExpression ||
        argument.kind === SyntaxKind.MethodReferenceExpression
      ) {
        return;
      }
      const param = info.decl.parameters[i] as { type?: TypeNode } | undefined;
      if (!param?.type) return;
      unifyInto(
        substitute(resolveType(param.type, info.decl), info.receiverSubst),
        getTypeOfExpression(argument),
        vars,
        bindings,
      );
    });
    const expected = contextualTypeOfCall(call);
    if (expected) {
      unifyInto(
        substitute(resolveType(info.decl.returnType, info.decl), info.receiverSubst),
        expected,
        vars,
        bindings,
      );
    }
    return bindings;
  }

  function lambdaTargetType(lambda: Node): Type | undefined {
    const parent = lambda.parent;
    // T f = () -> ...;
    if (parent.kind === SyntaxKind.VariableDeclarator && parent.symbol) {
      return getTypeOfSymbol(parent.symbol);
    }
    // return () -> ...;  -> the enclosing method's return type, or (when the
    // lambda is itself returned from another lambda) that lambda's SAM return.
    if (parent.kind === SyntaxKind.ReturnStatement) {
      return enclosingReturnType(parent);
    }
    // new T(() -> ...) -> the matching constructor's parameter type,
    // instantiated with the created type's arguments.
    if (parent.kind === SyntaxKind.ObjectCreationExpression) {
      const creation = parent as ObjectCreationExpression;
      const args = creation.arguments ?? [];
      const index = args.indexOf(lambda);
      if (index < 0) return undefined;
      const created = getTypeOfExpression(creation);
      if (created.kind !== TypeKind.Class) return undefined;
      const declaration = declarationOf(created.symbol);
      if (!declaration || declaration.kind !== SyntaxKind.ClassDeclaration) return undefined;
      const ctors = (declaration as ClassDeclaration).members.filter(
        m => m.kind === SyntaxKind.ConstructorDeclaration,
      ) as ConstructorDeclaration[];
      const ctor =
        ctors.find(c => c.parameters.length === args.length) ??
        (ctors.length === 1 ? ctors[0] : undefined);
      const param = ctor?.parameters[index] as { type?: TypeNode } | undefined;
      if (!ctor || !param?.type) return undefined;
      return substitute(
        resolveType(param.type, ctor),
        substitutionFor(created.symbol, (created as ClassType).typeArguments),
      );
    }
    // m(() -> ...)  -> the resolved method's parameter type at the lambda's
    // index, instantiated with the receiver's type arguments (so the lambda
    // passed to Map<String, Integer>.forEach sees BiConsumer<? super String,
    // ? super Integer>, not the declared K/V).
    if (parent.kind === SyntaxKind.CallExpression) {
      const call = parent as CallExpression;
      const index = call.arguments.indexOf(lambda);
      const info = index >= 0 ? resolveCallInfo(call) : undefined;
      const param = info?.decl.parameters[index] as { type?: TypeNode } | undefined;
      if (!param?.type) return undefined;
      const declared = substitute(resolveType(param.type, info!.decl), info!.receiverSubst);
      // generic METHOD (Collectors.toMap, Stream.toArray, ...): bind its type
      // parameters from the sibling arguments and the call's context
      const methodSubst = inferredMethodSubst(call, info!);
      return methodSubst.size > 0 ? substitute(declared, methodSubst) : declared;
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
      samName: sam.name.text as SamName,
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

    const asType = (e: Node): Symbol | undefined => {
      if (e.kind === SyntaxKind.Identifier) {
        return resolveTypeEntityName(e as Identifier, e, program);
      }
      // Outer.Nested::m (e.g. Map.Entry::getKey): the qualifier parses as a
      // property access, but names a nested type.
      if (e.kind === SyntaxKind.PropertyAccessExpression) {
        const access = e as PropertyAccessExpression;
        const outer = asType(access.expression);
        const nested = outer && lookupMember(outer, access.name.text, Meaning.Type, program);
        return nested && nested.flags & SymbolFlags.Type ? nested : undefined;
      }
      return undefined;
    };
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
    const substituted = substitute(paramType, substitutionFor(target.symbol, target.typeArguments));
    // A wildcard target argument (BiConsumer<? super String, ...>): the lambda
    // parameter takes the wildcard's bound (JLS 18.5.3 descriptor inference).
    if (substituted.kind === TypeKind.Wildcard && substituted.bound) return substituted.bound;
    return substituted;
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
      const iterableSymbol = program.getGlobalIndex().getType("java.lang.Iterable" as Fqn);
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

  // The dotted name a chain of identifier/member accesses spells out
  // (gen.Greeting), or undefined if any link is not a plain name.
  function dottedTypeName(node: Node): string | undefined {
    if (node.kind === SyntaxKind.Identifier) return (node as Identifier).text;
    if (node.kind === SyntaxKind.PropertyAccessExpression) {
      const pa = node as PropertyAccessExpression;
      const left = dottedTypeName(pa.expression);
      return left ? `${left}.${pa.name.text}` : undefined;
    }
    return undefined;
  }

  function typeOfMemberAccess(access: PropertyAccessExpression): Type {
    const receiver = getTypeOfExpression(access.expression);
    if (receiver.kind === TypeKind.Array) {
      const member = arrayMember(access.name.text);
      return member ? getTypeOfSymbol(member) : errorType;
    }
    if (receiver.kind !== TypeKind.Class) {
      // gen.Greeting in expression position names the qualified type itself, not
      // a member access on a value: resolve the whole dotted chain as a type so
      // a following static access (gen.Greeting.text()) resolves.
      const fqn = dottedTypeName(access);
      if (fqn) {
        const asType = classTypeByFqn(fqn);
        if (asType.kind === TypeKind.Class) return asType;
      }
      return errorType;
    }
    const found = lookupTypedMember(receiver as ClassType, access.name.text);
    if (!found) return errorType;
    const memberType = substitute(getTypeOfSymbol(found.symbol), found.subst);
    // Narrow a `this.f` read of a final field, the same way a bare `f` narrows in
    // the Identifier arm. `this`-only keeps it sound: the receiver is fixed.
    if (
      nullnessAnnotations &&
      access.expression.kind === SyntaxKind.ThisExpression &&
      found.symbol.flags & SymbolFlags.Field &&
      isFinalField(found.symbol)
    ) {
      const narrowed = narrowNullnessAt(access, found.symbol, resolveRefNode, exprNullness);
      if (narrowed) return withNullness(memberType, narrowed);
    }
    return memberType;
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

  function instantiatedSignatureOfCall(call: CallExpression): string | undefined {
    const info = resolveCallInfo(call);
    if (!info) return undefined;
    const decl = info.decl;
    // Substitutions, in typeOfCall's order: the receiver's type arguments, then
    // the method's own inferred type arguments.
    const methodSubst = (() => {
      const vars = methodTypeParameters(decl);
      if (vars.size === 0) return undefined;
      const argTypes = call.arguments.map(getTypeOfExpression);
      return inferMethodTypeArguments(decl, argTypes, info.receiverSubst, vars);
    })();
    const renderType = (typeNode: TypeNode | undefined): string => {
      if (!typeNode) return "?";
      let t = substitute(resolveType(typeNode, decl), info.receiverSubst);
      if (methodSubst) t = substitute(t, methodSubst);
      // An unresolvable type (outside the stub) keeps its written form rather
      // than rendering "<error>"; a still-bare variable also reads fine as-is.
      return isError(t) ? nodeSourceText(typeNode) : typeToString(t);
    };
    const params = decl.parameters
      .map(p => {
        const param = p as Parameter;
        const name = param.name ? ` ${param.name.text}` : "";
        return `${renderType(param.type)}${param.isVarArgs ? "..." : ""}${name}`;
      })
      .join(", ");
    return `${renderType(decl.returnType)} ${decl.name.text}(${params})`;
  }

  function parameterLabelsOf(declaration: Node): string[] {
    if (
      declaration.kind !== SyntaxKind.MethodDeclaration &&
      declaration.kind !== SyntaxKind.ConstructorDeclaration
    ) {
      return [];
    }
    return (declaration as MethodDeclaration).parameters.map(nodeSourceText);
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
    const doc = cleanJavadoc(blocks.at(-1)!); // nearest to the declaration
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
    return index.getType(prefix as Fqn) ?? index.getPackageByName(prefix as PackageName);
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
  // Unary numeric promotion (JLS 5.6.1): byte/short/char promote to int; other
  // types (numeric or not) pass through - the caller decides what an error is.
  function unaryPromoted(type: Type): Type {
    return type.kind === TypeKind.Primitive &&
      (type.name === "byte" || type.name === "short" || type.name === "char")
      ? intType
      : type;
  }

  function widerNumeric(a: Type, b: Type): Type {
    const order = ["int", "long", "float", "double"];
    const rank = (t: Type) => {
      if (t.kind !== TypeKind.Primitive) return -1;
      const promoted =
        t.name === "byte" || t.name === "short" || t.name === "char" ? "int" : t.name;
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

  // A field that can never be reassigned: a `final` field, or a record component
  // (implicitly final). Only such fields are safe to narrow - a non-final field
  // could be written by another thread or an intervening call between guard and use.
  function isFinalField(symbol: Symbol): boolean {
    const decl = symbol.valueDeclaration ?? symbol.declarations?.[0];
    if (!decl) return false;
    if (decl.kind === SyntaxKind.RecordComponent) return true;
    if (decl.kind === SyntaxKind.VariableDeclarator) {
      const field = decl.parent as { modifiers?: ReadonlyArray<Node> } | undefined;
      return field?.modifiers?.some(m => m.kind === SyntaxKind.FinalKeyword) ?? false;
    }
    return false;
  }

  // Symbols whose nullness flow-narrows: locals and parameters, plus final fields
  // (this.f / bare f - a single, stable receiver). See narrowNullnessAt.
  function isNarrowable(symbol: Symbol): boolean {
    if (symbol.flags & (SymbolFlags.Parameter | SymbolFlags.LocalVariable)) return true;
    return Boolean(symbol.flags & SymbolFlags.Field) && isFinalField(symbol);
  }

  // Resolve a narrowing reference: a bare identifier or a `this.f` member access.
  const resolveRefNode = (n: Node): Symbol | undefined =>
    n.kind === SyntaxKind.PropertyAccessExpression
      ? resolveMemberAccess(n as PropertyAccessExpression)
      : resolveIdentifier(n as Identifier, program);

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
        if (!symbol) return errorType;
        const type = getTypeOfSymbol(symbol);
        // Flow-aware narrowing (nikeee/cappu#25): refine a local/parameter/final-field's
        // nullness facet from what the preceding control flow has proven at this use.
        if (nullnessAnnotations && isNarrowable(symbol)) {
          const narrowed = narrowNullnessAt(node, symbol, resolveRefNode, exprNullness);
          if (narrowed) return withNullness(type, narrowed);
        }
        return type;
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
      case SyntaxKind.CastExpression: {
        const cast = node as CastExpression;
        const target = resolveType(cast.type, node);
        // A cast does not launder nullness (nikeee/cappu#25): unless the cast type
        // is explicitly annotated, the value keeps the operand's nullness, so
        // `(String) null` stays possibly-null rather than the @NullMarked default.
        if (nullnessAnnotations && !typeUseNullness(cast.type, nullnessAnnotations)) {
          const operand = getTypeOfExpression(cast.expression);
          if (operand.kind === TypeKind.Null || nullnessOf(operand) === "nullable") {
            return withNullness(target, "nullable");
          }
        }
        return target;
      }
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
        if (u.operator === SyntaxKind.ExclamationToken) return booleanType;
        const operand = getTypeOfExpression(u.operand);
        // Unary +/-/~ apply unary numeric promotion (JLS 15.15.3-5): -byte is
        // an int. ++/-- keep the variable's type (JLS 15.15.1/2).
        return u.operator === SyntaxKind.PlusToken ||
          u.operator === SyntaxKind.MinusToken ||
          u.operator === SyntaxKind.TildeToken
          ? unaryPromoted(operand)
          : operand;
      }
      case SyntaxKind.PostfixUnaryExpression:
        return getTypeOfExpression((node as unknown as { operand: Node }).operand);
      case SyntaxKind.ConditionalExpression: {
        // The conditional's type (JLS 15.25): binary numeric promotion for numeric
        // arms, otherwise a simplified reference lub (the more general arm, or a
        // null arm yields the other, else java.lang.Object).
        const t = getTypeOfExpression((node as ConditionalExpression).whenTrue);
        const f = getTypeOfExpression((node as ConditionalExpression).whenFalse);
        const base = ((): Type => {
          if (t.kind === TypeKind.Error) return f;
          if (f.kind === TypeKind.Error) return t;
          if (t.kind === TypeKind.Null) return f;
          if (f.kind === TypeKind.Null) return t;
          const num = widerNumeric(t, f);
          if (num.kind !== TypeKind.Error) return num;
          if (isAssignableTo(f, t, false)) return t;
          if (isAssignableTo(t, f, false)) return f;
          return classTypeByFqn("java.lang.Object");
        })();
        // A null/nullable arm makes the whole conditional possibly-null (nikeee/cappu#25).
        const mayBeNull = (x: Type): boolean =>
          x.kind === TypeKind.Null || nullnessOf(x) === "nullable";
        if (nullnessAnnotations && (mayBeNull(t) || mayBeNull(f))) {
          return withNullness(base, "nullable");
        }
        return base;
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
    return program.getGlobalIndex().getType("java.lang.Object" as Fqn);
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
        const boxed = BOX[source.name as keyof typeof BOX];
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
    const last = params.at(-1)!;
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

  // Every overload declaration a call could bind to (for signature help): the
  // same candidate gathering as resolveCallInfoWorker, without picking a winner.
  function resolveCallCandidates(call: CallExpression): MethodDeclaration[] {
    const callee = call.expression;
    if (callee.kind === SyntaxKind.PropertyAccessExpression) {
      const access = callee as PropertyAccessExpression;
      const receiver = receiverClassType(getTypeOfExpression(access.expression));
      if (receiver) return collectTypedOverloads(receiver, access.name.text).map(c => c.decl);
      const symbol = resolveMemberAccess(access);
      return (symbol?.declarations ?? []).filter(
        d => d.kind === SyntaxKind.MethodDeclaration,
      ) as MethodDeclaration[];
    }
    if (callee.kind === SyntaxKind.Identifier) {
      const symbol = resolveIdentifier(callee as Identifier, program);
      return (symbol?.declarations ?? []).filter(
        d => d.kind === SyntaxKind.MethodDeclaration,
      ) as MethodDeclaration[];
    }
    return [];
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
      const boxed = BOX[type.name as keyof typeof BOX];
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

  // The symbol of the innermost enclosing type declaration if it is an enum,
  // else undefined - so unqualified values()/valueOf() resolve only inside the
  // enum's own body (not a nested class).
  function enclosingEnumSymbol(node: Node): Symbol | undefined {
    for (let n: Node | undefined = node.parent; n; n = n.parent) {
      switch (n.kind) {
        case SyntaxKind.EnumDeclaration:
          return n.symbol;
        case SyntaxKind.ClassDeclaration:
        case SyntaxKind.InterfaceDeclaration:
        case SyntaxKind.RecordDeclaration:
        case SyntaxKind.AnnotationTypeDeclaration:
          return undefined;
        default:
          break;
      }
    }
    return undefined;
  }

  // The type of the synthesized enum statics E.values() (E[]) and
  // E.valueOf(String) (E), which have no source declaration.
  function enumStaticCallType(call: CallExpression): Type | undefined {
    const callee = call.expression;
    // Unqualified values()/valueOf(...) inside an enum's own body resolve to the
    // enclosing enum's synthetic statics (the innermost enclosing type is the enum).
    if (callee.kind === SyntaxKind.Identifier) {
      const name = (callee as Identifier).text;
      const wantsValues = name === "values" && call.arguments.length === 0;
      const wantsValueOf = name === "valueOf" && call.arguments.length === 1;
      if (wantsValues || wantsValueOf) {
        const enumSym = enclosingEnumSymbol(call);
        if (enumSym) {
          const t = classType(enumSym);
          return wantsValues ? arrayType(t) : t;
        }
      }
      return undefined;
    }
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
    // A method's @Nullable/@NonNull return annotation sits on the method modifiers,
    // not the return type node, so merge it onto the result (nikeee/cappu#25).
    if (nullnessAnnotations) {
      returnType = withNullness(returnType, readDeclaredNullness(info.decl, nullnessAnnotations));
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
      // A `return` inside a lambda targets the SAM's return type (JLS 15.27.2 /
      // 9.8), instantiated with the target's type arguments.
      if (current.kind === SyntaxKind.LambdaExpression) return getLambdaInfo(current)?.instReturn;
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
    const objectSymbol = program.getGlobalIndex().getType("java.lang.Object" as Fqn);
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

  // A use of a @Deprecated method (a call) or type (a type reference), with the
  // referenced name's span and the annotation's since/forRemoval, or undefined.
  function deprecatedUseAt(node: Node): DeprecatedUse | undefined {
    if (node.kind === SyntaxKind.CallExpression) {
      const info = resolveCallInfo(node as CallExpression);
      const dep = info && readDeprecation(info.decl);
      if (!dep) return undefined;
      const callee = (node as CallExpression).expression;
      const nameNode =
        callee.kind === SyntaxKind.PropertyAccessExpression
          ? (callee as PropertyAccessExpression).name
          : callee.kind === SyntaxKind.Identifier
            ? (callee as Identifier)
            : undefined;
      if (!nameNode) return undefined;
      const text = getSourceFileOfNode(nameNode).text;
      return {
        pos: skipTrivia(text, nameNode.pos),
        end: nameNode.end,
        name: nameNode.text,
        kind: "method",
        since: dep.since,
        forRemoval: dep.forRemoval,
      };
    }
    if (node.kind === SyntaxKind.TypeReference) {
      const ref = node as TypeReference;
      const sym = resolveTypeEntityName(ref.typeName, node, program);
      const dep = sym && readDeprecation(sym.valueDeclaration ?? sym.declarations?.[0]);
      if (!dep) return undefined;
      const text = getSourceFileOfNode(node).text;
      return {
        pos: skipTrivia(text, ref.typeName.pos),
        end: ref.typeName.end,
        name: entityNameToString(ref.typeName),
        kind: "type",
        since: dep.since,
        forRemoval: dep.forRemoval,
      };
    }
    if (node.kind === SyntaxKind.PropertyAccessExpression) {
      const access = node as PropertyAccessExpression;
      // A call's callee (obj.m()) is the CallExpression's method use, reported
      // above - don't also report it as a field access here.
      if (
        access.parent?.kind === SyntaxKind.CallExpression &&
        (access.parent as CallExpression).expression === access
      ) {
        return undefined;
      }
      const sym = resolveName(access.name);
      if (!sym || !(sym.flags & SymbolFlags.Field)) return undefined;
      // A field's declaration node is the VariableDeclarator; @Deprecated sits on
      // the enclosing FieldDeclaration, so read the annotation from there.
      let fieldDecl = sym.valueDeclaration ?? sym.declarations?.[0];
      if (fieldDecl?.kind === SyntaxKind.VariableDeclarator) fieldDecl = fieldDecl.parent;
      const dep = readDeprecation(fieldDecl);
      if (!dep) return undefined;
      const text = getSourceFileOfNode(access.name).text;
      return {
        pos: skipTrivia(text, access.name.pos),
        end: access.name.end,
        name: access.name.text,
        kind: "field",
        since: dep.since,
        forRemoval: dep.forRemoval,
      };
    }
    return undefined;
  }

  // Every use of a deprecated method or type in a (cleanly parsed) source file.
  function getDeprecatedUses(sourceFile: SourceFile): DeprecatedUse[] {
    if (sourceFile.parseDiagnostics.length > 0) return [];
    const uses: DeprecatedUse[] = [];
    const walk = (node: Node): void => {
      const use = deprecatedUseAt(node);
      if (use) uses.push(use);
      forEachChild(node, child => {
        walk(child);
        return undefined;
      });
    };
    walk(sourceFile);
    return uses;
  }

  function getSemanticDiagnostics(sourceFile: SourceFile): Diagnostic[] {
    const diagnostics: Diagnostic[] = [];

    // jspecify nullness (nikeee/cappu#25). The value/target nullness both come
    // from the type model: a value is possibly-null when its type is the `null`
    // literal or carries a @Nullable facet (incl. a @Nullable generic element via
    // substitution); a target is non-null when its type carries @NonNull.
    const valueMayBeNull = (node: Node): boolean => {
      const t = getTypeOfExpression(node);
      return t.kind === TypeKind.Null || nullnessOf(t) === "nullable";
    };

    const checkNullness = (valueNode: Node, targetType: Type, name: string): void => {
      if (!nullnessAnnotations) return;
      if (nullnessOf(targetType) !== "nonNull") return;
      if (!valueMayBeNull(valueNode)) return;
      diagnostics.push(
        createDiagnostic(
          valueNode.pos,
          valueNode.end - valueNode.pos,
          Diagnostics.Possibly_null_value_assigned_to_non_null_0,
          name,
        ),
      );
    };

    // Dereferencing a possibly-null receiver (x.foo(), x.field, x[i]). Flow-aware:
    // a receiver narrowed non-null by a preceding guard is not flagged.
    const checkDereference = (receiver: Node): void => {
      if (!nullnessAnnotations) return;
      if (receiver.kind === SyntaxKind.SuperExpression) return;
      if (!valueMayBeNull(receiver)) return;
      const text = getSourceFileOfNode(receiver).text;
      const start = skipTrivia(text, receiver.pos);
      diagnostics.push(
        createDiagnostic(
          start,
          receiver.end - start,
          Diagnostics.Dereference_of_possibly_null_value_0,
          text.slice(start, receiver.end),
        ),
      );
    };

    // A switch on a null selector throws NPE - except under JEP 441, where a
    // `case null` label handles it. The selector is dereferenced only when no
    // such label is present.
    const switchHasNullCase = (clauses: readonly SwitchClause[]): boolean =>
      clauses.some(c => c.labels?.some(l => l.kind === SyntaxKind.NullKeyword) ?? false);
    const checkSwitchSelector = (sw: SwitchStatement | SwitchExpression): void => {
      if (!switchHasNullCase(sw.clauses)) checkDereference(sw.expression);
    };

    // Argument nullness against a resolved signature: each parameter type is
    // instantiated with `subst` (the receiver's / created type's type arguments),
    // so a null into the non-null element of List<@NonNull String>.add(E) is caught.
    const checkParamNullness = (
      args: readonly Node[],
      parameters: readonly Node[],
      subst: Map<Symbol, Type>,
    ): void => {
      if (!nullnessAnnotations) return;
      const last = parameters.at(-1) as Parameter | undefined;
      const fixed = last?.isVarArgs ? parameters.length - 1 : parameters.length;
      for (let i = 0; i < Math.min(args.length, fixed); i++) {
        const p = parameters[i] as Parameter;
        if (!p.symbol) continue;
        const targetType = substitute(getTypeOfSymbol(p.symbol), subst);
        checkNullness(args[i]!, targetType, p.name?.text ?? `parameter ${i + 1}`);
      }
    };

    const checkCallNullness = (call: CallExpression): void => {
      const info = nullnessAnnotations ? resolveCallInfo(call) : undefined;
      if (info) checkParamNullness(call.arguments, info.decl.parameters, info.receiverSubst);
    };

    const checkAssignment = (valueNode: Node, targetType: Type): void => {
      if (targetType.kind === TypeKind.Primitive && targetType.name === "void") return;
      if (!isConcrete(targetType)) return;
      const valueType = getTypeOfExpression(valueNode);
      if (!isConcrete(valueType)) return;
      // A call whose resolution is not trustworthy: an OVERLOADED method (the
      // picked return type is best-effort, e.g. IOUtils.copy has int- and
      // long-returning overloads), or a declaration whose arity does not even
      // match the call (the name walk found a sibling overload, e.g. a bare
      // toString() landing on toString(boolean, StringBuilder) while the real
      // target is Object.toString()).
      if (valueNode.kind === SyntaxKind.CallExpression) {
        const call = valueNode as CallExpression;
        const declarations = resolveCall(call)?.symbol?.declarations ?? [];
        if (declarations.length > 1) return;
        const declaration = declarations[0];
        if (declaration?.kind === SyntaxKind.MethodDeclaration) {
          const parameters = (declaration as MethodDeclaration).parameters;
          const last = parameters.at(-1) as Parameter | undefined;
          const accepts = last?.isVarArgs
            ? call.arguments.length >= parameters.length - 1
            : call.arguments.length === parameters.length;
          if (!accepts) return;
        }
      }
      // High-precision scope: the primitive<->reference boundary (int x = "s"),
      // and primitive-to-primitive below. Reference-to-reference / generic cases
      // depend on subtyping precision we do not fully model yet.
      if (targetType.kind === TypeKind.Primitive && valueType.kind === TypeKind.Primitive) {
        checkPrimitiveAssignment(valueNode, valueType.name, targetType.name);
        return;
      }
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

    // Assignment between primitives (JLS 5.2): identity and widening are fine;
    // a byte/short/char target also takes a CONSTANT byte/short/char/int that
    // fits. constfold does not resolve constant variables (final x = ...), so an
    // unfoldable value in the constant-narrowing position stays silent rather
    // than risking a false positive; everything else is a definite error.
    const checkPrimitiveAssignment = (valueNode: Node, value: string, target: string): void => {
      if (value === target || primitiveWidens(value, target)) return;
      const range = NARROWING_RANGE[target];
      const constNarrowable =
        range !== undefined && ["byte", "short", "char", "int"].includes(value);
      if (constNarrowable) {
        const folded = foldConstant(valueNode);
        if (!folded) return; // possibly a constant variable: no false positives
        if (folded.kind === "int" && folded.value >= range[0] && folded.value <= range[1]) return;
      }
      diagnostics.push(
        createDiagnostic(
          valueNode.pos,
          valueNode.end - valueNode.pos,
          Diagnostics.Incompatible_types_0_1,
          value,
          target,
        ),
      );
    };

    // --- call arity (JLS 15.12.2.1 applicability by arity) ---------------------
    // High-precision: report only when the COMPLETE overload set is provably
    // known and none of it can accept the argument count. The JDK stub and
    // classpath stubs are incomplete by design (the real types have more
    // overloads), so any hierarchy that leaves project source aborts the check
    // - except java.lang.Object as the implicit terminal.
    const arityAccepts = (parameters: readonly Node[], argc: number): boolean => {
      const last = parameters.at(-1) as Parameter | undefined;
      return last?.isVarArgs ? argc >= parameters.length - 1 : argc === parameters.length;
    };
    const isProjectSymbol = (typeSymbol: Symbol): boolean => {
      const declaration = typeSymbol.valueDeclaration ?? typeSymbol.declarations?.[0];
      if (!declaration) return false;
      const fileName = getSourceFileOfNode(declaration).fileName;
      return !fileName.startsWith("jdk:") && !fileName.startsWith("classpath:");
    };
    // Every overload of `name` across the hierarchy of `start`, or undefined
    // when the hierarchy is not fully project-known (or declares no such
    // method at all - a bare call may target an ENCLOSING type instead).
    const projectOverloads = (start: Symbol, name: string): MethodDeclaration[] | undefined => {
      const overloads: MethodDeclaration[] = [];
      const seen = new Set<Symbol>();
      const queue = [start];
      while (queue.length > 0) {
        const current = queue.shift()!;
        if (seen.has(current)) continue;
        seen.add(current);
        if (current === program.getGlobalIndex().getType("java.lang.Object" as Fqn)) {
          // implicit terminal: its stub is complete for its own members
          continue;
        }
        if (!isProjectSymbol(current)) return undefined;
        const member = current.members?.get(name);
        for (const declaration of member?.declarations ?? []) {
          if (declaration.kind === SyntaxKind.MethodDeclaration) {
            overloads.push(declaration as MethodDeclaration);
          }
        }
        // Every WRITTEN supertype must resolve: one that does not (outside the
        // stub, a typo, ...) may carry the overload that would have accepted
        // the call - silence instead of a false positive.
        const declaration = current.valueDeclaration ?? current.declarations?.[0];
        if (!declaration) return undefined;
        for (const typeNode of superTypeNodesOf(declaration)) {
          if (typeNode.kind !== SyntaxKind.TypeReference) return undefined;
          const superSymbol = resolveTypeEntityName(
            (typeNode as TypeReference).typeName,
            declaration,
            program,
          );
          if (!superSymbol) return undefined;
          queue.push(superSymbol);
        }
      }
      return overloads.length > 0 ? overloads : undefined;
    };
    const describeArities = (parameterLists: readonly (readonly Node[])[]): string => {
      const arities = new Set<string>();
      for (const parameters of parameterLists) {
        const last = parameters.at(-1) as Parameter | undefined;
        arities.add(last?.isVarArgs ? `${parameters.length - 1}+` : `${parameters.length}`);
      }
      return [...arities].sort((a, b) => Number.parseInt(a) - Number.parseInt(b)).join(" or ");
    };
    const reportArity = (after: Node, end: number, expected: string, argc: number): void => {
      diagnostics.push(
        createDiagnostic(
          after.end,
          Math.max(1, end - after.end),
          Diagnostics.Invalid_number_of_arguments_expected_0_got_1,
          expected,
          String(argc),
        ),
      );
    };

    const checkCallArity = (call: CallExpression): void => {
      const callee = call.expression;
      const nameNode =
        callee.kind === SyntaxKind.Identifier
          ? (callee as Identifier)
          : callee.kind === SyntaxKind.PropertyAccessExpression
            ? (callee as PropertyAccessExpression).name
            : undefined; // super()/this() and friends resolve elsewhere
      if (!nameNode) return;
      const symbol = resolveName(nameNode);
      if (!symbol || !(symbol.flags & SymbolFlags.Method)) return;
      // The overload set starts at the receiver's static type for a member
      // call, and at the enclosing type for a bare call (a subtype may add
      // overloads the declaring type does not have).
      let start: Symbol | undefined;
      if (callee.kind === SyntaxKind.PropertyAccessExpression) {
        const receiver = getTypeOfExpression((callee as PropertyAccessExpression).expression);
        start = receiver.kind === TypeKind.Class ? (receiver as ClassType).symbol : undefined;
      } else {
        start = enclosingTypeSymbol(call);
      }
      if (!start) return;
      const overloads = projectOverloads(start, nameNode.text);
      if (!overloads) return;
      const argc = call.arguments.length;
      const applicable = overloads.filter(o => arityAccepts(o.parameters, argc));
      if (applicable.length === 0) {
        reportArity(callee, call.end, describeArities(overloads.map(o => o.parameters)), argc);
        return;
      }
      // With exactly one arity-surviving overload there is nothing to choose
      // between: every argument must convert to its declared parameter type.
      if (applicable.length === 1) checkArgumentTypes(call.arguments, applicable[0]!.parameters);
    };

    // Argument types against the single applicable signature. The same
    // high-precision rules as checkAssignment apply (only the primitive
    // boundary is judged), so a target-typed or unresolved argument stays
    // silent; the trailing varargs position is skipped (array vs component).
    const checkArgumentTypes = (args: readonly Node[], parameters: readonly Node[]): void => {
      const last = parameters.at(-1) as Parameter | undefined;
      const fixed = last?.isVarArgs ? parameters.length - 1 : parameters.length;
      for (let i = 0; i < Math.min(args.length, fixed); i++) {
        const parameter = parameters[i] as Parameter;
        if (!parameter.symbol) continue;
        checkAssignment(args[i]!, getTypeOfSymbol(parameter.symbol));
      }
    };

    const checkCreationArity = (node: ObjectCreationExpression): void => {
      if (node.type.kind !== SyntaxKind.TypeReference) return;
      const symbol = resolveTypeEntityName((node.type as TypeReference).typeName, node, program);
      if (!symbol || !isProjectSymbol(symbol)) return; // stubs are incomplete by design
      const declaration = symbol.valueDeclaration ?? symbol.declarations?.[0];
      if (!declaration) return;
      const argc = (node.arguments ?? []).length;
      if (declaration.kind === SyntaxKind.ClassDeclaration) {
        const ctors = (declaration as ClassDeclaration).members.filter(
          m => m.kind === SyntaxKind.ConstructorDeclaration,
        ) as ConstructorDeclaration[];
        // no declared constructor: the implicit default takes no arguments
        const applicable = ctors.filter(c => arityAccepts(c.parameters, argc));
        const ok = ctors.length === 0 ? argc === 0 : applicable.length > 0;
        if (!ok) {
          reportArity(
            node.type,
            node.classBody ? (node.arguments?.at(-1)?.end ?? node.type.end) : node.end,
            ctors.length === 0 ? "0" : describeArities(ctors.map(c => c.parameters)),
            argc,
          );
        } else if (applicable.length === 1 && !node.classBody) {
          // an anonymous body changes nothing about ctor argument conversion,
          // but stay conservative and only judge the plain creation
          checkArgumentTypes(node.arguments ?? [], applicable[0]!.parameters);
          const created = resolveType(node.type, node);
          const subst =
            created.kind === TypeKind.Class
              ? substitutionFor(symbol, (created as ClassType).typeArguments)
              : new Map<Symbol, Type>();
          checkParamNullness(node.arguments ?? [], applicable[0]!.parameters, subst);
        }
      } else if (declaration.kind === SyntaxKind.RecordDeclaration) {
        const record = declaration as RecordDeclaration;
        const hasDeclaredCtor = record.members.some(
          m => m.kind === SyntaxKind.ConstructorDeclaration,
        );
        // a compact constructor's effective arity is the component count, which
        // the declaration node does not carry - only the ctor-less case is safe
        if (hasDeclaredCtor) return;
        if (argc !== record.recordComponents.length) {
          reportArity(node.type, node.end, String(record.recordComponents.length), argc);
        }
        // The canonical constructor's parameters are the record components, so a
        // possibly-null argument into a non-null component is caught here. Each
        // component carries its own nullness (annotation or @NullMarked default).
        const created = resolveType(node.type, node);
        const subst =
          created.kind === TypeKind.Class
            ? substitutionFor(symbol, (created as ClassType).typeArguments)
            : new Map<Symbol, Type>();
        checkParamNullness(node.arguments ?? [], record.recordComponents, subst);
      }
    };

    // --- format-string arity/type check (String.format & friends) -----------
    // The java.util.Formatter %-syntax methods take `(..., String, Object...)`,
    // so a wrong argument count or type is arity-valid against the declaration
    // yet throws at runtime. When the format string is a literal we parse its
    // conversion specifiers and warn - staying silent on anything unprovable.
    const argTypeDescriptor = (t: Type): ArgTypeDescriptor | undefined => {
      if (t.kind === TypeKind.Primitive) return { primitive: t.name };
      if (t.kind === TypeKind.Class) return { fqn: fqnOf(t) };
      return undefined; // array / type-variable / null / error: unprovable
    };
    const checkFormatCall = (call: CallExpression): void => {
      const callee = call.expression;
      if (callee.kind !== SyntaxKind.PropertyAccessExpression) return;
      const access = callee as PropertyAccessExpression;
      const receiver = receiverClassType(getTypeOfExpression(access.expression));
      if (!receiver) return;
      const entry = FORMAT_METHODS.get(`${fqnOf(receiver)}#${access.name.text}`);
      if (!entry) return;

      // Locate the format-string node and where the format arguments begin.
      let fmtNode: Node;
      let argsStart: number;
      if (entry.fmtIsReceiver) {
        fmtNode = access.expression; // "text".formatted(args)
        argsStart = 0;
      } else {
        // The format string is the fixed parameter right before the Object...
        // varargs; the resolved overload gives its position (handling the
        // Locale-first String.format overload with no special-casing).
        const params = resolveCallInfo(call)?.decl.parameters;
        const last = params?.at(-1) as Parameter | undefined;
        if (!params || !last?.isVarArgs || params.length < 2) return;
        const fmtPos = params.length - 2;
        if (fmtPos >= call.arguments.length) return;
        fmtNode = call.arguments[fmtPos]!;
        argsStart = params.length - 1;
      }

      if (
        fmtNode.kind !== SyntaxKind.StringLiteral &&
        fmtNode.kind !== SyntaxKind.TextBlockLiteral
      ) {
        return; // non-literal format string: cannot analyze
      }
      const parsed = parseFormatString((fmtNode as LiteralExpression).value);
      if (!parsed) return;

      const provided = call.arguments.length - argsStart;
      if (provided < 0) return;
      const span = Math.max(1, call.end - callee.end);
      if (provided < parsed.maxIndex) {
        diagnostics.push(
          createDiagnostic(
            callee.end,
            span,
            Diagnostics.Format_not_enough_arguments_0_1,
            String(parsed.maxIndex),
            String(provided),
          ),
        );
        return; // too few is the headline; a type pass would just add noise
      }
      if (provided > parsed.maxIndex) {
        diagnostics.push(
          createDiagnostic(
            callee.end,
            span,
            Diagnostics.Format_too_many_arguments_0_1,
            String(parsed.maxIndex),
            String(provided),
          ),
        );
      }
      // Each consuming specifier against the static type of its mapped argument.
      for (const c of parsed.consumers) {
        const argNode = call.arguments[argsStart + c.argIndex - 1];
        if (!argNode) continue;
        const argType = getTypeOfExpression(argNode);
        const desc = argTypeDescriptor(argType);
        if (!desc) continue;
        if (conversionAccepts(c.conversion, desc) === "no") {
          diagnostics.push(
            createDiagnostic(
              argNode.pos,
              argNode.end - argNode.pos,
              Diagnostics.Format_conversion_incompatible_0_1,
              c.conversion,
              typeToString(argType),
            ),
          );
        }
      }
    };

    // Shared entry for the "known JDK method with a literal argument" checks
    // below: returns the receiver's class FQN and the method name for a member
    // call, or undefined when the callee is not a resolvable member access.
    const memberCallTarget = (
      call: CallExpression,
    ): { fqn: string; name: string; access: PropertyAccessExpression } | undefined => {
      const callee = call.expression;
      if (callee.kind !== SyntaxKind.PropertyAccessExpression) return undefined;
      const access = callee as PropertyAccessExpression;
      const receiver = receiverClassType(getTypeOfExpression(access.expression));
      if (!receiver) return undefined;
      return { fqn: fqnOf(receiver), name: access.name.text, access };
    };

    const literalStringArg = (
      call: CallExpression,
      index: number,
    ): LiteralExpression | undefined => {
      const arg = call.arguments[index];
      if (
        arg &&
        (arg.kind === SyntaxKind.StringLiteral || arg.kind === SyntaxKind.TextBlockLiteral)
      ) {
        return arg as LiteralExpression;
      }
      return undefined;
    };

    // --- regex literal validation (nikeee/cappu#30) --------------------------
    // A malformed literal regex throws PatternSyntaxException at runtime; we
    // flag only the provably-broken ones (see validateRegex). The regex is
    // argument 0 for every method here.
    const checkRegexCall = (call: CallExpression): void => {
      const target = memberCallTarget(call);
      if (!target || !REGEX_METHODS.has(`${target.fqn}#${target.name}`)) return;
      const arg = literalStringArg(call, 0);
      if (!arg) return;
      const reason = validateRegex(arg.value);
      if (reason) {
        diagnostics.push(
          createDiagnostic(
            arg.pos,
            arg.end - arg.pos,
            Diagnostics.Invalid_regular_expression_0,
            reason,
          ),
        );
      }
    };

    // --- date/time pattern validation ----------------------------------------
    // DateTimeFormatter.ofPattern(pattern) - unknown letters throw, and the
    // classic Y/D/h footguns compile but produce wrong output.
    const checkDateTimeCall = (call: CallExpression): void => {
      const target = memberCallTarget(call);
      if (
        !target ||
        `${target.fqn}#${target.name}` !== "java.time.format.DateTimeFormatter#ofPattern"
      ) {
        return;
      }
      const arg = literalStringArg(call, 0);
      if (!arg) return;
      const report = checkDateTimePattern(arg.value);
      for (const letter of report.invalidLetters) {
        diagnostics.push(
          createDiagnostic(
            arg.pos,
            arg.end - arg.pos,
            Diagnostics.Invalid_date_time_pattern_letter_0,
            letter,
          ),
        );
      }
      for (const f of report.footguns) {
        diagnostics.push(
          createDiagnostic(
            arg.pos,
            arg.end - arg.pos,
            Diagnostics.Suspicious_date_time_pattern_letter_0_1_2,
            f.letter,
            f.meaning,
            f.suggest,
          ),
        );
      }
    };

    // --- integer parsing (Integer/Long/Short/Byte parse*/valueOf) ------------
    // A non-parseable literal or an out-of-range radix throws
    // NumberFormatException. The string is argument 0; a second numeric-literal
    // argument, when present, is the radix.
    const checkNumberParseCall = (call: CallExpression): void => {
      const target = memberCallTarget(call);
      if (!target) return;
      const typeName = PARSE_METHODS.get(`${target.fqn}#${target.name}`);
      if (!typeName) return;
      const arg = literalStringArg(call, 0);
      if (!arg) return;
      let radix = 10;
      const radixArg = call.arguments[1];
      if (radixArg) {
        if (radixArg.kind !== SyntaxKind.NumericLiteral) return; // unknown radix: bail
        radix = Number((radixArg as LiteralExpression).value);
        if (!Number.isInteger(radix)) return;
        if (radix < MIN_RADIX || radix > MAX_RADIX) {
          diagnostics.push(
            createDiagnostic(
              radixArg.pos,
              radixArg.end - radixArg.pos,
              Diagnostics.Radix_0_out_of_range,
              String(radix),
            ),
          );
          return;
        }
      }
      if (!isParseableInteger(arg.value, radix)) {
        diagnostics.push(
          createDiagnostic(
            arg.pos,
            arg.end - arg.pos,
            Diagnostics.String_0_is_not_a_valid_1,
            arg.value,
            typeName,
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
            checkNullness(d.initializer, getTypeOfSymbol(d.symbol), d.name.text);
          } else if (d.initializer?.kind === SyntaxKind.ArrayInitializer && d.symbol) {
            // Each element initializes a slot of the array, so a null element into a
            // non-null element type is flagged (nikeee/cappu#25).
            const t = getTypeOfSymbol(d.symbol);
            if (t.kind === TypeKind.Array) {
              for (const el of (d.initializer as ArrayInitializer).elements) {
                if (el.kind !== SyntaxKind.ArrayInitializer) {
                  checkNullness(el, t.elementType, d.name.text);
                }
              }
            }
          }
          break;
        }
        case SyntaxKind.AssignmentExpression: {
          const a = node as AssignmentExpression;
          if (a.operatorToken === SyntaxKind.EqualsToken) {
            checkAssignment(a.right, getTypeOfExpression(a.left));
            const leftName =
              a.left.kind === SyntaxKind.Identifier
                ? (a.left as Identifier)
                : a.left.kind === SyntaxKind.PropertyAccessExpression
                  ? (a.left as PropertyAccessExpression).name
                  : undefined;
            if (leftName) checkNullness(a.right, getTypeOfExpression(a.left), leftName.text);
          }
          break;
        }
        case SyntaxKind.CallExpression:
          // A recovered parse has unreliable call shapes: no arity judgments.
          if (sourceFile.parseDiagnostics.length === 0) {
            checkCallArity(node as CallExpression);
            checkCallNullness(node as CallExpression);
            checkFormatCall(node as CallExpression);
            checkRegexCall(node as CallExpression);
            checkDateTimeCall(node as CallExpression);
            checkNumberParseCall(node as CallExpression);
          }
          break;
        case SyntaxKind.ObjectCreationExpression:
          if (sourceFile.parseDiagnostics.length === 0) {
            checkCreationArity(node as ObjectCreationExpression);
          }
          break;
        case SyntaxKind.ReturnStatement: {
          const r = node as ReturnStatement;
          if (r.expression) {
            const ret = enclosingReturnType(node);
            if (ret) checkAssignment(r.expression, ret);
            // The return targets the nearest enclosing function: a method's declared
            // return, or (inside a lambda) the SAM's instantiated return.
            let fn: Node | undefined = node;
            while (
              fn &&
              fn.kind !== SyntaxKind.MethodDeclaration &&
              fn.kind !== SyntaxKind.LambdaExpression
            ) {
              fn = fn.parent;
            }
            if (fn?.kind === SyntaxKind.MethodDeclaration && fn.symbol) {
              checkNullness(
                r.expression,
                getTypeOfSymbol(fn.symbol),
                (fn as MethodDeclaration).name.text,
              );
            } else if (fn?.kind === SyntaxKind.LambdaExpression) {
              const info = getLambdaInfo(fn);
              if (info) checkNullness(r.expression, info.instReturn, info.samName);
            }
          }
          break;
        }
        case SyntaxKind.LambdaExpression: {
          // An expression-bodied lambda (() -> e) implicitly returns e, so check it
          // against the SAM's return nullness (block bodies go through ReturnStatement).
          const lam = node as LambdaExpression;
          if (nullnessAnnotations && lam.body.kind !== SyntaxKind.Block) {
            const info = getLambdaInfo(lam);
            if (info) checkNullness(lam.body, info.instReturn, info.samName);
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
        case SyntaxKind.ElementAccessExpression:
          checkDereference((node as ElementAccessExpression).expression);
          break;
        // Implicit-dereference positions that unconditionally NPE on null: the
        // thrown value, the synchronized lock, and the iterated collection.
        case SyntaxKind.ThrowStatement:
        case SyntaxKind.SynchronizedStatement:
          checkDereference((node as unknown as { expression: Node }).expression);
          break;
        case SyntaxKind.ForEachStatement: {
          const fe = node as ForEachStatement;
          checkDereference(fe.expression);
          // The loop binds each element to the variable; a nullable element into a
          // non-null (e.g. explicitly typed) loop variable is an unsafe binding.
          if (nullnessAnnotations && fe.parameter.symbol) {
            const elem = elementTypeOf(getTypeOfExpression(fe.expression));
            const varType = getTypeOfSymbol(fe.parameter.symbol);
            if (
              nullnessOf(varType) === "nonNull" &&
              (elem.kind === TypeKind.Null || nullnessOf(elem) === "nullable")
            ) {
              const text = getSourceFileOfNode(fe.parameter).text;
              const start = skipTrivia(text, fe.parameter.pos);
              diagnostics.push(
                createDiagnostic(
                  start,
                  fe.parameter.end - start,
                  Diagnostics.Possibly_null_value_assigned_to_non_null_0,
                  fe.parameter.name?.text ?? "variable",
                ),
              );
            }
          }
          break;
        }
        case SyntaxKind.PropertyAccessExpression: {
          const access = node as PropertyAccessExpression;
          checkDereference(access.expression);
          // super.* is modeled imprecisely (super resolves to Object), so skip it
          // to avoid false positives on inherited members.
          if (access.expression.kind !== SyntaxKind.SuperExpression) {
            const receiver = getTypeOfExpression(access.expression);
            if (
              receiver.kind === TypeKind.Class &&
              isClosedType(receiver as ClassType) &&
              !lookupTypedMember(receiver as ClassType, access.name.text) &&
              !isSynthesizedEnumMember(receiver as ClassType, access.name.text)
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
        case SyntaxKind.SwitchStatement:
          checkSwitchSelector(node as SwitchStatement);
          break;
        case SyntaxKind.SwitchExpression: {
          const sw = node as SwitchExpression;
          checkSwitchSelector(sw);
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

    // Unused imports (warnings). A recovered parse may have dropped the very
    // identifiers that would prove an import used - judge clean parses only.
    if (sourceFile.parseDiagnostics.length === 0) {
      for (const imp of findUnusedImports(sourceFile)) {
        const start = skipTrivia(sourceFile.text, imp.pos);
        diagnostics.push(
          createDiagnostic(
            start,
            imp.end - start,
            Diagnostics.Unused_import_0,
            entityNameToString(imp.name),
          ),
        );
      }
    }
    // Uses of @Deprecated methods/types (warnings).
    for (const use of getDeprecatedUses(sourceFile)) {
      diagnostics.push(
        createDiagnostic(use.pos, use.end - use.pos, Diagnostics._0_is_deprecated, use.name),
      );
    }
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
    resolveCallCandidates,
    instantiatedSignatureOfCall,
    parameterLabelsOf,
    typeStringOfSymbol,
    signatureOfSymbol,
    signatureOfDeclaration,
    getDocumentation,
    getDocumentationOfNode,
    getSemanticDiagnostics,
    getDeprecatedUses,
  };
}
