// The type model used by the checker. Java types: primitives, class/interface
// types (with type arguments), arrays, type variables, wildcards, intersections,
// the null type and an error type for the unknown/unresolved (so analysis
// degrades gracefully instead of throwing or reporting false errors).

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

export interface ClassType {
  readonly kind: TypeKind.Class;
  readonly symbol: Symbol;
  readonly typeArguments: readonly Type[];
}

export interface ArrayType {
  readonly kind: TypeKind.Array;
  readonly elementType: Type;
}

export interface TypeVariable {
  readonly kind: TypeKind.TypeVariable;
  readonly symbol: Symbol;
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
