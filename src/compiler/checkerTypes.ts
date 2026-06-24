// The type model used by the checker. Java types: primitives, class/interface
// types (with type arguments), arrays, type variables, wildcards, intersections,
// the null type and an error type for the unknown/unresolved (so analysis
// degrades gracefully instead of throwing or reporting false errors).

import type { Nullness } from "./nullness.ts";
import type { Symbol } from "./types.ts";

export const enum TypeKind {
  Primitive,
  Class,
  Array,
  TypeVariable,
  Wildcard,
  Intersection,
  Null,
  Error,
}

export interface PrimitiveType {
  readonly kind: TypeKind.Primitive;
  readonly name: string; // int, long, boolean, ..., void
}

// `nullness` is the jspecify nullness facet (nikeee/cappu#25), attached by
// resolveType only when nullness checking is enabled and read only by the nullness
// checks - typeToString, assignability and the emitter ignore it.
export interface ClassType {
  readonly kind: TypeKind.Class;
  readonly symbol: Symbol;
  readonly typeArguments: readonly Type[];
  readonly nullness?: Nullness;
}

export interface ArrayType {
  readonly kind: TypeKind.Array;
  readonly elementType: Type;
  readonly nullness?: Nullness;
}

export interface TypeVariable {
  readonly kind: TypeKind.TypeVariable;
  readonly symbol: Symbol;
  readonly nullness?: Nullness;
  /**
   * The leftmost declared bound (`T extends Comparable<T>` -> Comparable<T>),
   * the type a use of T erases to (JLS 4.6). Filled in lazily by the checker
   * after creation (the bound may reference T itself, so it cannot be resolved
   * inside the factory without recursing); absent for an unbounded parameter.
   */
  bound?: Type;
}

export interface WildcardType {
  readonly kind: TypeKind.Wildcard;
  readonly bound?: Type;
  readonly isExtends: boolean;
  readonly isSuper: boolean;
}

export interface IntersectionType {
  readonly kind: TypeKind.Intersection;
  readonly types: readonly Type[];
}

export interface NullType {
  readonly kind: TypeKind.Null;
}

export interface ErrorType {
  readonly kind: TypeKind.Error;
}

export type Type =
  | PrimitiveType
  | ClassType
  | ArrayType
  | TypeVariable
  | WildcardType
  | IntersectionType
  | NullType
  | ErrorType;

export const errorType: ErrorType = { kind: TypeKind.Error };
export const nullType: NullType = { kind: TypeKind.Null };

const primitiveCache = new Map<string, PrimitiveType>();
export function primitiveType(name: string): PrimitiveType {
  let t = primitiveCache.get(name);
  if (!t) {
    t = { kind: TypeKind.Primitive, name };
    primitiveCache.set(name, t);
  }
  return t;
}

export function classType(symbol: Symbol, typeArguments: readonly Type[] = []): ClassType {
  return { kind: TypeKind.Class, symbol, typeArguments };
}

export function arrayType(elementType: Type): ArrayType {
  return { kind: TypeKind.Array, elementType };
}

export function typeVariable(symbol: Symbol): TypeVariable {
  return { kind: TypeKind.TypeVariable, symbol };
}

/** The jspecify nullness facet of a type, or undefined when unknown/irrelevant. */
export function nullnessOf(type: Type): Nullness | undefined {
  return (type as { nullness?: Nullness }).nullness;
}

/**
 * A copy of `type` with its nullness facet set (nikeee/cappu#25). Only reference
 * types carry nullness; a no-op for primitives, null, error and an undefined value.
 */
export function withNullness<T extends Type>(type: T, nullness: Nullness | undefined): T {
  if (nullness === undefined) return type;
  if (
    type.kind === TypeKind.Class ||
    type.kind === TypeKind.Array ||
    type.kind === TypeKind.TypeVariable
  ) {
    return { ...type, nullness };
  }
  return type;
}

export function isError(type: Type): boolean {
  return type.kind === TypeKind.Error;
}

/** Human-readable form for hover/diagnostics. */
export function typeToString(type: Type): string {
  switch (type.kind) {
    case TypeKind.Primitive:
      return type.name;
    case TypeKind.Class: {
      const name = type.symbol.escapedName;
      if (type.typeArguments.length === 0) return name;
      return `${name}<${type.typeArguments.map(typeToString).join(", ")}>`;
    }
    case TypeKind.Array:
      return `${typeToString(type.elementType)}[]`;
    case TypeKind.TypeVariable:
      return type.symbol.escapedName;
    case TypeKind.Wildcard:
      if (type.isExtends && type.bound) return `? extends ${typeToString(type.bound)}`;
      if (type.isSuper && type.bound) return `? super ${typeToString(type.bound)}`;
      return "?";
    case TypeKind.Intersection:
      return type.types.map(typeToString).join(" & ");
    case TypeKind.Null:
      return "null";
    default:
      return "<error>";
  }
}
